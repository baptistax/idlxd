package instagram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram/auth"
	"github.com/baptistax/idl/internal/models"
	"github.com/baptistax/idl/internal/transport"
)

// One provider is one serialized session. Profile evidence, cursors and the jar
// never cross providers. No metadata redirect is followed.
type CookieProvider struct {
	cookiesPath       string
	base              *http.Client
	slot              chan struct{}
	session           *transport.Client
	jar               http.CookieJar
	page              profilePage
	allowedHighlights map[string]bool
	requests          int
	stopped           error
}

func NewCookieProvider(path string) *CookieProvider { return NewCookieProviderWithClient(path, nil) }
func NewCookieProviderWithClient(path string, base *http.Client) *CookieProvider {
	return &CookieProvider{cookiesPath: path, base: base, slot: make(chan struct{}, 1)}
}

func (c *CookieProvider) prepareSession() error {
	if c.session != nil {
		return nil
	}
	jar, err := auth.LoadSessionJar(c.cookiesPath)
	if err != nil {
		return err
	}
	c.session, err = transport.New(auth.SessionOrigin, c.base, jar, 0)
	c.jar = jar
	return err
}
func (c *CookieProvider) CheckSessionFile() error {
	c.slot <- struct{}{}
	defer func() { <-c.slot }()
	return c.prepareSession()
}

func (c *CookieProvider) Capabilities() map[string]models.Capability {
	return map[string]models.Capability{"posts": {Supported: true}, "reels": {Supported: true}, "stories": {Supported: true}, "highlights": {Supported: true}}
}

func (c *CookieProvider) MediaClient() *http.Client { return transport.MediaClient(c.base) }

// The profile request establishes the identity used by later media requests.
func (c *CookieProvider) begin(ctx context.Context, username, id string, profile bool) error {
	select {
	case c.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	err := ctx.Err()
	switch {
	case err != nil:
	case c.stopped != nil:
		err = c.stopped
	case profile:
		err = c.prepareSession()
	case c.page.profile.UserID == "":
		err = fault.New(fault.Authentication, "media", "profile has not been verified")
	case username != c.page.profile.Username || id != "" && id != c.page.profile.UserID:
		err = mismatch("request does not match verified profile")
	}
	if err != nil {
		<-c.slot
	}
	return err
}
func (c *CookieProvider) finish(err *error) {
	if fault.Global(*err) {
		c.stopped = *err
	}
	<-c.slot
}

func (c *CookieProvider) read(ctx context.Context, path string, form url.Values) ([]byte, error) {
	var payload io.Reader
	method := http.MethodGet
	if form != nil {
		payload = strings.NewReader(form.Encode())
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, auth.SessionOrigin+path, payload)
	if err != nil {
		return nil, schema("invalid web request")
	}
	req.Header.Set("User-Agent", "IDL/2.0")
	// Media info is a same-origin web API GET. Its successful captured
	// requests use the web application identifier and session CSRF binding.
	mediaInfo := strings.HasPrefix(path, "/api/v1/media/")
	if mediaInfo {
		req.Header.Set("X-IG-App-ID", "936619743392459")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "application/json")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", auth.SessionOrigin)
		if c.page.lsd != "" {
			req.Header.Set("X-FB-LSD", c.page.lsd)
		}
	}
	if form != nil || mediaInfo {
		for _, cookie := range c.jar.Cookies(req.URL) {
			if cookie.Name == "csrftoken" {
				req.Header.Set("X-CSRFToken", cookie.Value)
				break
			}
		}
	}
	if c.requests >= 128 {
		return nil, fault.New(fault.Budget, "web session", "metadata request limit reached")
	}
	c.requests++
	var body []byte
	err = c.session.Exchange(req, func(resp transport.Response) error {
		if status := resp.StatusError(); status != nil {
			// An ambiguous client error may carry a session-wide rejection
			// in its envelope. Known denials stop immediately on headers.
			if resp.StatusCode() >= 400 && resp.StatusCode() < 500 && !fault.Global(status) {
				raw, err := resp.ReadBody()
				if err != nil {
					return err
				}
				if _, err := webResponse(raw); fault.Global(err) {
					return err
				}
				if mediaInfo {
					return mediaInfoError(raw, status)
				}
			}
			if mediaInfo && fault.Is(status, fault.Upstream) {
				return &fault.Error{Kind: fault.Download, Op: "media info", Detail: "item lookup failed", Cause: status}
			}
			return status
		}
		if resp.StatusCode() != http.StatusOK {
			return schema("incomplete metadata response")
		}
		var err error
		body, err = resp.ReadBody()
		return err
	})
	return body, err
}

// Only the reviewed read operations call this method; neither the capture nor
// a server-provided next URL is ever used as a request template.
func (c *CookieProvider) query(ctx context.Context, doc string, variables object) (object, error) {
	addWebQuerySelections(doc, variables)
	raw, err := json.Marshal(variables)
	if err != nil {
		return nil, schema("invalid query variables")
	}
	form := url.Values{"doc_id": {doc}, "variables": {string(raw)}, "__a": {"1"}}
	if c.page.dtsg != "" {
		form.Set("fb_dtsg", c.page.dtsg)
	}
	if c.page.lsd != "" {
		form.Set("lsd", c.page.lsd)
	}
	body, err := c.read(ctx, "/api/graphql", form)
	if err != nil {
		return nil, err
	}
	return webResponse(body)
}

func (c *CookieProvider) FetchProfile(ctx context.Context, username string) (profile Profile, err error) {
	username, err = models.NormalizeUsername(username)
	if err != nil {
		return Profile{}, err
	}
	if err = c.begin(ctx, "", "", true); err != nil {
		return Profile{}, err
	}
	defer func() {
		if err != nil {
			c.page = profilePage{}
			c.stopped = err
		}
		c.finish(&err)
	}()
	c.page = profilePage{}
	c.allowedHighlights = nil
	body, err := c.read(ctx, "/"+username+"/", nil)
	if err != nil {
		return Profile{}, err
	}
	c.page, err = parseProfilePage(body, username)
	if err == ErrTargetProfileMissing && c.page.routeID != "" {
		root, err := c.query(ctx, profileDoc, object{"id": c.page.routeID, "enable_integrity_filters": true})
		if err != nil {
			return Profile{}, err
		}
		if err := c.page.receive(root, username); err != nil {
			return Profile{}, err
		}
		if err := c.page.verifyProfile(); err != nil {
			return Profile{}, err
		}
	} else if err != nil {
		return Profile{}, err
	}
	return c.page.profile, nil
}

// OwnProfile is evidence from the last authenticated server response. This
// stricter condition is used by the one-file developer smoke.
func (c *CookieProvider) OwnProfile() bool {
	c.slot <- struct{}{}
	defer func() { <-c.slot }()
	return c.stopped == nil && c.page.viewer != "" && c.page.viewer == c.page.profile.UserID
}

func (c *CookieProvider) FetchPostsPage(ctx context.Context, username, after string) (items []Media, info PageInfo, owner string, err error) {
	if err = c.begin(ctx, username, "", false); err != nil {
		return
	}
	defer c.finish(&err)
	root, partial := c.page.timeline, c.page.timelineErr
	if after != "" {
		root, partial = c.query(ctx, postsNextDoc, object{"username": username, "after": after, "first": 12, "include_multi_captions": false, "data": object{"count": 12}})
	} else if root == nil && partial == nil {
		root, partial = c.query(ctx, postsFirstDoc, object{"username": username, "data": object{"count": 12}})
	}
	if root == nil {
		err = partial
		return
	}
	items, info, err = mediaPage(root, "posts", c.page.profile, partial)
	return items, info, c.page.profile.UserID, err
}

func (c *CookieProvider) FetchReelsPage(ctx context.Context, username, id, after string) (items []Media, info PageInfo, err error) {
	if err = c.begin(ctx, username, id, false); err != nil {
		return
	}
	defer c.finish(&err)
	id = c.page.profile.UserID
	doc := reelsFirstDoc
	vars := object{"user_id": id, "data": object{"target_user_id": id, "page_size": 12, "include_feed_video": true}}
	if after != "" {
		doc = reelsNextDoc
		delete(vars, "user_id")
		vars["id"] = id
		vars["after"] = after
		vars["first"] = 12
	}
	root, partial := c.query(ctx, doc, vars)
	if root == nil {
		return nil, PageInfo{}, partial
	}
	return mediaPage(root, "reels", c.page.profile, partial)
}

func (c *CookieProvider) FetchStories(ctx context.Context, username, id string) (items []Media, err error) {
	if err = c.begin(ctx, username, id, false); err != nil {
		return nil, err
	}
	defer c.finish(&err)
	root, partial := c.query(ctx, storiesDoc, object{"reel_ids_arr": []string{c.page.profile.UserID}})
	if root == nil {
		return nil, partial
	}
	return stories(root, c.page.profile, partial)
}

func (c *CookieProvider) FetchHighlightsTray(ctx context.Context, username, id string) (titles []Highlight, err error) {
	if err = c.begin(ctx, username, id, false); err != nil {
		return nil, err
	}
	defer c.finish(&err)
	root, partial := c.page.tray, c.page.trayErr
	if root == nil {
		root, partial = c.query(ctx, trayDoc, object{"user_id": c.page.profile.UserID})
	}
	if root == nil {
		return nil, partial
	}
	titles, _, info, err := highlightGroups(root, c.page.profile, true, partial)
	if err != nil && !fault.OnlyPartial(err) {
		return nil, err
	}
	if info.HasNextPage {
		return nil, fault.New(fault.Unsupported, "highlights", "tray has an unsupported continuation")
	}
	c.allowedHighlights = map[string]bool{}
	for _, h := range titles {
		c.allowedHighlights[h.ID] = true
	}
	return titles, err
}

func (c *CookieProvider) FetchHighlightsPage(ctx context.Context, username string, ids []string, after string, first int) (groups []HighlightMedia, info PageInfo, err error) {
	if err = c.begin(ctx, username, "", false); err != nil {
		return
	}
	defer c.finish(&err)
	if len(ids) == 0 || first < 1 || first > 12 {
		return nil, PageInfo{}, schema("invalid highlight selection")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if !c.allowedHighlights[id] || selected[id] {
			return nil, PageInfo{}, mismatch("unverified highlight selection")
		}
		selected[id] = true
	}
	doc := highlightsFirstDoc
	vars := object{"first": first, "initial_reel_id": ids[0], "reel_ids": ids}
	if after != "" {
		doc = highlightsNextDoc
		vars["after"] = after
		vars["is_highlight"] = true
	} else {
		vars["last"] = first
	}
	root, partial := c.query(ctx, doc, vars)
	if root == nil {
		return nil, PageInfo{}, partial
	}
	_, groups, info, err = highlightGroups(root, c.page.profile, false, partial)
	for _, g := range groups {
		if !selected[g.ID] {
			return nil, PageInfo{}, mismatch("unrequested highlight collection")
		}
	}
	return
}

func (c *CookieProvider) FetchMediaInfo(ctx context.Context, username, pk, code string) (m Media, err error) {
	if err = c.begin(ctx, username, "", false); err != nil {
		return
	}
	defer c.finish(&err)
	if !models.IsNumericID(pk) {
		return Media{}, schema("invalid media identity")
	}
	body, err := c.read(ctx, "/api/v1/media/"+pk+"/info/", nil)
	if err != nil {
		if fault.Is(err, fault.Unsupported) {
			return Media{}, &fault.Error{Kind: fault.Unavailable, Op: "media info", Cause: err}
		}
		return Media{}, err
	}
	return parseMediaInfo(body, c.page.profile, pk, code)
}

func parseMediaInfo(body []byte, profile Profile, pk, code string) (Media, error) {
	root, err := webResponse(body)
	if err != nil {
		return Media{}, mediaInfoError(body, err)
	}
	list, ok := root["items"].([]any)
	if !ok || len(list) != 1 {
		return Media{}, schema("expected exactly one selected item")
	}
	o, _ := list[0].(object)
	m, err := normalizeWebMedia(o, profile, nil)
	if err != nil {
		return Media{}, err
	}
	if m.PK != pk || code != "" && m.Code != "" && m.Code != code {
		return Media{}, mismatch("selected item mismatch")
	}
	return m, nil
}

func mediaInfoError(body []byte, err error) error {
	if !fault.Is(err, fault.Upstream) || fault.Global(err) {
		return err
	}
	// Classify only known control messages. Never print the provider's text.
	root, _ := readObject(body)
	switch strings.ToLower(strings.TrimSpace(str(root["message"]))) {
	case "useragent mismatch", "user agent mismatch":
		return schema("media info client is incompatible")
	case "media not found", "media unavailable":
		return fault.New(fault.Unavailable, "media info", "selected item unavailable")
	default:
		return &fault.Error{Kind: fault.Download, Op: "media info", Detail: "item lookup failed", Cause: err}
	}
}
