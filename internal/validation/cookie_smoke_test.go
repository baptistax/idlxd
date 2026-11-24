package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/instagram/auth"
	"github.com/baptistax/idl/internal/models"
	"github.com/baptistax/idl/internal/transport"
)

const cookieSmokeBudget = 4
const reelSmokeBudget = 5 // profile, optional profile details, first grid, one detail, one MP4

// Allow one profile page, its two possible deferred reads and one owned photo.
func runCookieSmoke(ctx context.Context, path, username, dir string, base http.RoundTripper) (d Diagnostic, err error) {
	return runCookieSelection(ctx, path, username, dir, base, false)
}

func runCookieSelection(ctx context.Context, path, username, dir string, base http.RoundTripper, reel bool) (d Diagnostic, err error) {
	d = Diagnostic{Provider: "cookies", Authentication: "unverified", Profile: "unverified", Category: "one feed photo", Metadata: "not attempted", Download: "not attempted", Validation: "not attempted"}
	username, err = models.NormalizeUsername(username)
	if err != nil {
		return d, err
	}
	start := time.Now()
	var selected, detailURL string
	budget := cookieSmokeBudget
	if reel {
		d.Category = "one reel"
		budget = reelSmokeBudget
	}
	g := &requestGuard{base: base, limit: budget, allow: func(r *http.Request) bool {
		if r.Header.Get("Authorization") != "" {
			return false
		}
		if selected == "" {
			return r.Header.Get("Cookie") != "" && (r.Method == http.MethodGet && (r.URL.String() == auth.SessionOrigin+"/"+username+"/" || r.URL.String() == detailURL) ||
				r.Method == http.MethodPost && r.URL.String() == auth.SessionOrigin+"/api/graphql")
		}
		return r.Method == http.MethodGet && transport.MediaHost(r.URL) && r.URL.String() == selected && r.Header.Get("Cookie") == "" && r.Header.Get("X-CSRFToken") == ""
	}}
	defer func() {
		d.Requests, d.LastHTTPStatus = g.stats()
		d.Duration = time.Since(start)
		if err != nil {
			d.Classification = classification(err)
			d.Cancellation = "stopped"
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	p := instagram.NewCookieProviderWithClient(path, &http.Client{Transport: g, Timeout: 15 * time.Second})
	profile, err := p.FetchProfile(ctx, username)
	if err == nil || errors.Is(err, instagram.ErrTargetProfileMissing) {
		d.Authentication = "signed-in viewer verified"
	}
	if err != nil {
		return d, err
	}
	if !p.OwnProfile() {
		return d, fault.New(fault.Permission, "smoke", "target is not the signed-in owner")
	}
	d.Profile = "matched owner"
	if reel {
		var items []models.Media
		items, _, err = p.FetchReelsPage(ctx, username, profile.UserID, "")
		if err != nil {
			return d, err
		}
		if len(items) == 0 {
			return d, fault.New(fault.Unavailable, "smoke", "no reel on the initial page")
		}
		m := items[0]
		if m.MediaType != 2 || !m.IsReel() || m.User.PK != profile.UserID {
			return d, fault.New(fault.Permission, "smoke", "unverified reel")
		}
		selected = instagram.BestVideoURL(m)
		if selected == "" {
			detailURL = auth.SessionOrigin + "/api/v1/media/" + m.PK + "/info/"
			m, err = p.FetchMediaInfo(ctx, username, m.PK, m.Code)
			if err != nil {
				return d, err
			}
			if m.MediaType != 2 {
				return d, fault.New(fault.Schema, "smoke", "expected a video")
			}
			selected = instagram.BestVideoURL(m)
		}
		if selected == "" {
			return d, fault.New(fault.Unavailable, "smoke", "no complete video representation")
		}
		d.Metadata = "one owned reel selected"
	} else {
		items, _, _, err := p.FetchPostsPage(ctx, username, "")
		if err != nil {
			return d, err
		}
	selection:
		for _, post := range items {
			photos := post.CarouselMedia
			if post.MediaType == 1 && (post.ProductType == "feed" || post.ProductType == "") {
				photos = []instagram.Media{post}
			}
			for _, item := range photos {
				if item.MediaType == 1 {
					selected = instagram.BestImageURL(item)
					if selected != "" {
						break selection
					}
				}
			}
		}
		if selected == "" {
			return d, fault.New(fault.Unsupported, "smoke", "no eligible photo on initial page")
		}
		d.Metadata = "one owned photo selected"
	}
	dl := downloader.New(downloader.Options{OutputDir: dir, UserAgent: "IDL/2.0", HTTPClient: p.MediaClient()})
	d.Download = "attempted"
	if _, err = dl.DownloadMedia(ctx, selected, "selected.media", reel); err != nil {
		return d, err
	}
	d.Download = "one file saved"
	d.Validation = "one fully decoded photo"
	if reel {
		d.Validation = "one MP4 with a video track and media data"
	}
	return d, nil
}

func TestCookieSmokeBudgetAndOwnership(t *testing.T) {
	const photo = `{"pk":45,"user":{"id":"123","username":"owner"},"media_type":1,"image_versions2":{"candidates":[{"url":"https://test.cdninstagram.com/selected.webp?sig=synthetic","width":2,"height":2}]}}`
	for _, tt := range []struct {
		name, viewer string
		status       int
		corrupt      bool
		kind         fault.Kind
		calls        int
	}{
		{"viewer only", "123", 200, false, fault.Schema, 1},
		{"carousel photo", "123", 200, false, "", 2},
		{"success", "123", 200, false, "", 2}, {"other viewer", "456", 200, false, fault.Permission, 1}, {"expired", "123", 401, false, fault.Authentication, 1}, {"rate", "123", 429, false, fault.RateLimited, 1}, {"redirect", "123", 302, false, fault.Upstream, 1}, {"bad file", "123", 200, true, fault.Schema, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cookies.txt")
			if err := os.WriteFile(path, []byte(".instagram.com TRUE / TRUE 0 sessionid synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
			n := 0
			jpeg := photoBytes(t)
			base := localTransport(t, func(w http.ResponseWriter, r *http.Request) {
				n++
				switch n {
				case 1:
					if r.URL.Path != "/owner/" || r.Header.Get("Cookie") != "sessionid=synthetic" {
						t.Error("unexpected profile request")
					}
					w.Header().Set("Location", "https://never.invalid/")
					w.WriteHeader(tt.status)
					if tt.name == "viewer only" {
						fmt.Fprint(w, `<script type="application/json">{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"define":[["PolarisViewer",[],{"id":"123","data":{"id":"123","username":"owner"}},1]]}}]]]}</script>`)
						return
					}
					post := photo
					if tt.name == "carousel photo" {
						post = `{"pk":"88","media_type":8,"user":{"id":"123"},"carousel_media":[` + photo + `]}`
					}
					fmt.Fprintf(w, `<script type="application/json">{"require":[["RelayPrefetchedStreamCache","next",[],["q",{"__bbox":{"result":{"data":{"user":{"id":"123","username":"owner"},"viewer":{"user":{"pk":"%s"}},"xdt_api__v1__feed__user_timeline_graphql_connection":{"edges":[{"node":%s}],"page_info":{"has_next_page":true,"end_cursor":"opaque"}}}}}}]]]}</script>`, tt.viewer, post)
				case 2:
					if r.URL.Path != "/selected.webp" || r.Header.Get("Cookie") != "" {
						t.Error("credential or selection mismatch")
					}
					if tt.corrupt {
						fmt.Fprint(w, "invalid")
						return
					}
					w.Write(jpeg)
				default:
					t.Error("unexpected extra request")
				}
			})
			dir := t.TempDir()
			d, err := runCookieSmoke(context.Background(), path, "owner", dir, base)
			if tt.name == "viewer only" && (d.Authentication != "signed-in viewer verified" || d.Profile != "unverified" || d.Download != "not attempted") {
				t.Fatal("incorrect authentication or target status", d)
			}
			if (err == nil) != (tt.kind == "") || d.Classification != tt.kind || d.Requests != tt.calls || n != tt.calls {
				t.Fatalf("diagnostic %+v", d)
			}
			files, _ := os.ReadDir(dir)
			if err != nil && len(files) != 0 || err == nil && len(files) != 1 {
				t.Fatal("unexpected file count")
			}
			if err == nil {
				b, e := os.ReadFile(filepath.Join(dir, "selected.jpg"))
				if e != nil || !bytes.Equal(b, jpeg) {
					t.Fatal("original bytes or extension lost")
				}
			}
		})
	}
}

func TestCookieReelSmokeSelectsOneDetailAndRejectsPoster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(".instagram.com TRUE / TRUE 0 sessionid synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	const profile = `<script type="application/json">{"require":[["RelayPrefetchedStreamCache","next",[],["q",{"__bbox":{"result":{"data":{"user":{"id":"123","username":"owner"},"viewer":{"user":{"id":"123"}}}}}}]]]}</script>`
	const grid = `{"data":{"clips_connection":{"edges":[{"node":{"media":{"pk":"77","user":{"pk":"123"},"media_type":2,"product_type":"clips"}}}],"page_info":{"has_next_page":true,"end_cursor":"more"}}}}`
	const info = `{"items":[{"pk":"77","user":{"pk":"123"},"media_type":2,"product_type":"clips","video_versions":[{"url":"https://synthetic.cdninstagram.com/file.mp4"}]}]}`
	calls := 0
	body := photoBytes(t)
	base := localTransport(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/owner/":
			fmt.Fprint(w, profile)
		case "/api/graphql":
			fmt.Fprint(w, grid)
		case "/api/v1/media/77/info/":
			fmt.Fprint(w, info)
		case "/file.mp4":
			if r.Header.Get("Cookie") != "" {
				t.Error("credentials sent with media request")
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(body)
		default:
			t.Error("unexpected request")
		}
	})
	dir := t.TempDir()
	d, err := runCookieSelection(t.Context(), path, "owner", dir, base, true)
	files, readErr := os.ReadDir(dir)
	if !fault.Is(err, fault.Schema) || calls != 4 || d.Requests != calls || d.Validation != "not attempted" || readErr != nil || len(files) != 0 {
		t.Fatal("invalid reel smoke outcome", d, readErr)
	}
}
