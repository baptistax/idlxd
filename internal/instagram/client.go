package instagram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
	"github.com/baptistax/idl/internal/transport"
)

type Client struct {
	httpClient *transport.Client
	baseURL    string
	token      string
	accountID  string
	profile    Profile
	blocked    error
}

type Options struct {
	AccessToken string
	AccountID   string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

func NewClient(opts Options) (*Client, error) {
	return newGraphClient(opts, graphBase+"/"+GraphVersion)
}

func newGraphClient(opts Options, baseURL string) (*Client, error) {
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fault.New(fault.Authentication, "graph provider", "explicit Graph selection requires IDL_ACCESS_TOKEN; no switch to cookies performed")
	}
	if !models.IsNumericID(opts.AccountID) {
		return nil, fault.New(fault.InvalidInput, "provider", "IDL_ACCOUNT_ID must identify the authorized Instagram professional account")
	}
	hc, err := transport.New(baseURL, opts.HTTPClient, nil, opts.Timeout)
	if err != nil {
		return nil, err
	}
	return &Client{httpClient: hc, baseURL: baseURL, token: opts.AccessToken, accountID: opts.AccountID}, nil
}

func (c *Client) Capabilities() map[string]models.Capability {
	return map[string]models.Capability{
		"posts":      {Supported: true},
		"reels":      {Supported: true},
		"stories":    {Supported: true},
		"highlights": {Reason: "no live highlights operation configured"},
	}
}

func (c *Client) authorized(username, userID string) error {
	u, err := models.NormalizeUsername(username)
	if err != nil {
		return err
	}
	if c.blocked != nil {
		return c.blocked
	}
	if c.profile.UserID == "" {
		return fault.New(fault.Authentication, "media", "verify account identity before requesting media")
	}
	if u != c.profile.Username || userID != "" && userID != c.profile.UserID {
		return fault.New(fault.Permission, "media", "request does not match verified account")
	}
	return nil
}

func (c *Client) get(ctx context.Context, path, fields, after string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.blocked != nil {
		return c.blocked
	}
	u, err := url.Parse(c.baseURL + "/" + path)
	if err != nil {
		return fault.New(fault.InvalidInput, "request", "invalid local provider configuration")
	}
	q := u.Query()
	q.Set("fields", fields)
	if after != "" {
		q.Set("after", after)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fault.New(fault.InvalidInput, "request", "invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "IDL/2.0")
	req.Header.Set("Accept", "application/json")
	err = c.httpClient.Exchange(req, func(resp transport.Response) error {
		statusErr := resp.StatusError()
		body, err := resp.ReadBody()
		if err != nil {
			if statusErr != nil && ctx.Err() == nil {
				return statusErr
			}
			return err
		}
		err = decodeGraphQLResponse(body, out)
		if fault.Is(err, fault.RateLimited) {
			return &fault.Error{Kind: fault.RateLimited, Op: "request", Detail: "provider rate limit", RetryAfter: resp.RetryAfter(), Cause: err}
		}
		if statusErr == nil || fault.Global(err) {
			return err
		}
		if resp.StatusCode() == http.StatusNotFound && path == c.accountID && fields == profileFields {
			return &fault.Error{Kind: fault.Unsupported, Op: "profile", Detail: "HTTP 404", Cause: ErrProfileNotFound}
		}
		return statusErr
	})
	if fault.Global(err) {
		c.blocked = err
	}
	return err
}

// Graph responses deliberately use raw ID fields to avoid float64 conversion.
type graphMedia struct {
	ID        json.RawMessage `json:"id"`
	Type      string          `json:"media_type"`
	Product   string          `json:"media_product_type"`
	URL       string          `json:"media_url"`
	Timestamp string          `json:"timestamp"`
	Username  string          `json:"username"`
	Code      string          `json:"shortcode"`
	Owner     struct {
		ID json.RawMessage `json:"id"`
	} `json:"owner"`
}

type graphPage struct {
	Data   []graphMedia `json:"data"`
	Paging *struct {
		Next    string `json:"next"`
		Cursors struct {
			After string `json:"after"`
		} `json:"cursors"`
	} `json:"paging"`
}

func graphPageInfo(page graphPage) (PageInfo, error) {
	p := PageInfo{}
	if page.Data == nil {
		return p, fault.New(fault.Schema, "media page", "missing data array")
	}
	if page.Paging != nil && page.Paging.Next != "" {
		p.HasNextPage = true
		p.EndCursor = page.Paging.Cursors.After
		if p.EndCursor == "" {
			return PageInfo{}, fault.New(fault.Schema, "pagination", "next page has no cursor")
		}
	}
	// The Graph contract terminates by absence of paging.next, even on short pages.
	return p, nil
}

func (c *Client) normalizeGraphMedia(ctx context.Context, g graphMedia, owner, username string, child bool) (Media, error) {
	id := exactID(g.ID)
	if id == "" {
		return Media{}, fault.New(fault.Schema, "media", "missing media ID")
	}
	if got := exactID(g.Owner.ID); got != "" && got != owner {
		return Media{}, fault.New(fault.Permission, "media", "owner does not match verified account")
	}
	if g.Username != "" && !strings.EqualFold(g.Username, username) {
		return Media{}, fault.New(fault.Permission, "media", "username does not match verified account")
	}
	// Child ownership comes from the verified parent's children edge; top-level
	// media must explicitly identify its owner.
	if !child && exactID(g.Owner.ID) != owner {
		return Media{}, fault.New(fault.Schema, "media", "missing owner identity")
	}
	m := Media{PK: id, ID: id, Code: g.Code, User: IGUser{PK: owner, Username: username}, ProductType: strings.ToLower(g.Product), Provenance: "graph-facebook/" + GraphVersion}
	if g.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, g.Timestamp)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04:05-0700", g.Timestamp)
		}
		if err != nil {
			return Media{}, fault.New(fault.Schema, "media", "invalid timestamp")
		}
		m.TakenAt = t.Unix()
	}
	switch g.Type {
	case "IMAGE":
		m.MediaType = 1
		if g.URL != "" {
			m.ImageVersions2.Candidates = []Candidate{{URL: g.URL}}
		}
	case "VIDEO":
		m.MediaType = 2
		if g.URL != "" {
			m.VideoVersions = []Candidate{{URL: g.URL}}
		}
	case "CAROUSEL_ALBUM":
		if child {
			return Media{}, fault.New(fault.Schema, "carousel", "nested album")
		}
		m.MediaType = 8
		seen := NewPagination()
		after := ""
		for {
			var page graphPage
			if err := c.get(ctx, id+"/children", "id,media_type,media_url,timestamp", after, &page); err != nil {
				return Media{}, err
			}
			info, err := graphPageInfo(page)
			if err != nil {
				return Media{}, err
			}
			ids := make([]string, 0, len(page.Data))
			for _, item := range page.Data {
				ids = append(ids, exactID(item.ID))
			}
			if err := seen.Accept(ctx, ids, info); err != nil {
				return Media{}, err
			}
			for _, item := range page.Data {
				cm, err := c.normalizeGraphMedia(ctx, item, owner, username, true)
				if err != nil {
					return Media{}, err
				}
				if cm.TakenAt == 0 {
					cm.TakenAt = m.TakenAt
				}
				m.CarouselMedia = append(m.CarouselMedia, cm)
			}
			if !info.HasNextPage {
				break
			}
			after = info.EndCursor
		}
		if len(m.CarouselMedia) == 0 {
			return Media{}, fault.New(fault.Schema, "carousel", "empty album")
		}
		count := len(m.CarouselMedia)
		m.CarouselMediaCount = &count
	default:
		return Media{}, fault.New(fault.Unsupported, "media", "unknown media type")
	}
	return m, nil
}

func (c *Client) mediaPage(ctx context.Context, username, edge, after, category string) ([]Media, PageInfo, error) {
	if err := c.authorized(username, ""); err != nil {
		return nil, PageInfo{}, err
	}
	var page graphPage
	if err := c.get(ctx, c.accountID+"/"+edge, mediaFields, after, &page); err != nil {
		return nil, PageInfo{}, err
	}
	info, err := graphPageInfo(page)
	if err != nil {
		return nil, PageInfo{}, err
	}
	var items []Media
	ids := map[string]bool{}
	for _, g := range page.Data {
		id := exactID(g.ID)
		if id == "" || ids[id] {
			return nil, PageInfo{}, fault.New(fault.Schema, "media page", "missing or duplicate media ID")
		}
		ids[id] = true
		if exactID(g.Owner.ID) != c.profile.UserID {
			if exactID(g.Owner.ID) == "" {
				return nil, PageInfo{}, fault.New(fault.Schema, "media", "missing owner identity")
			}
			return nil, PageInfo{}, fault.New(fault.Permission, "media", "owner does not match verified account")
		}
		if g.Username != "" && !strings.EqualFold(g.Username, c.profile.Username) {
			return nil, PageInfo{}, fault.New(fault.Permission, "media", "username mismatch")
		}
		// Classification must be explicit so reels never silently become posts.
		if category != "stories" && g.Product != "FEED" && g.Product != "REELS" {
			return nil, PageInfo{}, fault.New(fault.Schema, "media", "missing or unknown media_product_type")
		}
		if category == "reels" && g.Product != "REELS" || category == "posts" && g.Product == "REELS" {
			continue
		}
		m, err := c.normalizeGraphMedia(ctx, g, c.profile.UserID, c.profile.Username, false)
		if err != nil {
			return nil, PageInfo{}, err
		}
		items = append(items, m)
	}
	return items, info, nil
}
