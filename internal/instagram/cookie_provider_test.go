package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func testPage(user, viewer, timeline, tray string) string {
	data := "{\"user\":" + user + ",\"viewer\":{\"user\":" + viewer + "}"
	if timeline != "" {
		data += ",\"" + postsConnection + "\":" + timeline
	}
	if tray != "" {
		data += ",\"highlights\":" + tray
	}
	return "<html><script type=\"application/json\">{\"require\":[[\"RelayPrefetchedStreamCache\",\"next\",[],[\"query\",{\"__bbox\":{\"result\":{\"data\":" + data + "}}}}]]]}</script></html>"
}
func localCookieProvider(t *testing.T, handler http.HandlerFunc) (*CookieProvider, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	destination, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		local := r.Clone(r.Context())
		local.URL.Host = destination.Host
		return server.Client().Transport.RoundTrip(local)
	})}
	path := filepath.Join(t.TempDir(), "cookies.txt")
	const export = "#HttpOnly_.instagram.com TRUE / TRUE 0 sessionid fixture\n" +
		"instagram.com FALSE / TRUE 0 parent_only fixture\n" +
		".instagram.com TRUE /other TRUE 0 other_path fixture\n" +
		".instagram.com TRUE / TRUE 1 expired fixture\n" +
		".example.invalid TRUE / TRUE 0 unrelated fixture\n"
	if err := os.WriteFile(path, []byte(export), 0600); err != nil {
		t.Fatal(err)
	}
	return NewCookieProviderWithClient(path, base), &attempts
}

func TestCookieProviderExchangesScopedSession(t *testing.T) {
	var calls atomic.Int32
	p, attempts := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
		wantCookie := "sessionid=fixture"
		if calls.Add(1) > 1 {
			wantCookie = "sessionid=rotated"
		}
		if r.Method != http.MethodGet || r.URL.Path != "/owner/" ||
			r.Header.Get("Cookie") != wantCookie || r.Header.Get("Authorization") != "" {
			t.Error("unexpected method, target, or credential scope")
		}
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "rotated", Domain: "instagram.com", Secure: true, Path: "/"})
		fmt.Fprint(w, testPage(`{"id":"123","username":"owner"}`, `{"pk":"123"}`, "", ""))
	})
	if err := p.CheckSessionFile(); err != nil || attempts.Load() != 0 {
		t.Fatal("session setup", err)
	}
	// The check and both requests must use the same validated in-memory jar.
	if err := os.Remove(p.cookiesPath); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		profile, err := p.FetchProfile(context.Background(), "@OWNER")
		if err != nil || profile.UserID != "123" || profile.Username != "owner" {
			t.Fatal("profile exchange", err)
		}
	}
	if attempts.Load() != 2 || calls.Load() != 2 {
		t.Fatal("unexpected number of requests")
	}
}

func TestCookieProviderStopsOnRejection(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		kind   fault.Kind
	}{
		{"redirect", 302, "", fault.Upstream},
		{"authentication", 401, "", fault.Authentication},
		{"forbidden", 403, "", fault.Permission},
		{"rate limit", 429, "", fault.RateLimited},
		{"challenge", 200, `{"error":{"message":"challenge_required"}}`, fault.Verification},
		{"public profile", 200, testPage(`{"id":"123","username":"owner"}`, `null`, "", ""), fault.Authentication},
		{"wrong account", 200, testPage(`{"id":"123","username":"other"}`, `{"pk":"123"}`, "", ""), fault.Permission},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, attempts := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://elsewhere.invalid/private")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			for range 2 {
				profile, err := p.FetchProfile(context.Background(), "owner")
				if !fault.Is(err, tt.kind) || profile.UserID != "" || attempts.Load() != 1 {
					t.Fatalf("requests=%d error=%v", attempts.Load(), err)
				}
			}
		})
	}
}

func TestItemFailureScope(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		kind   fault.Kind
		global bool
	}{
		{"malformed item", 200, `{"items":[{}]}`, fault.Schema, false},
		{"gone item", 410, "", fault.Unavailable, false},
		{"unavailable item", 200, `{"status":"fail","message":"Media not found"}`, fault.Unavailable, false},
		{"incompatible request", 400, `{"status":"fail","message":"useragent mismatch"}`, fault.Schema, false},
		{"item lookup failure", 500, "", fault.Download, false},
		{"authentication envelope", 400, `{"message":"login_required"}`, fault.Authentication, true},
		{"challenge envelope", 400, `{"challenge_required":true}`, fault.Verification, true},
		{"wrong selected item", 200, `{"items":[{"pk":"9","media_type":2,"user":{"pk":"123"}}]}`, fault.Permission, true},
		{"HTTP rate limit", 429, "", fault.RateLimited, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, calls := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/owner/":
					fmt.Fprint(w, testPage(`{"username":"owner","pk":"123"}`, `{"pk":"123"}`, "", ""))
				case "/api/v1/media/7/info/":
					w.WriteHeader(tt.status)
					fmt.Fprint(w, tt.body)
				case "/api/graphql":
					switch r.FormValue("doc_id") {
					case storiesDoc:
						fmt.Fprint(w, `{"data":{"reels_media":[]}}`)
					case trayDoc:
						fmt.Fprint(w, `{"data":{"highlights":{"edges":[],"page_info":{"has_next_page":false}}}}`)
					default:
						t.Error("unexpected category")
					}
				default:
					t.Error("unexpected request")
				}
			})
			ctx := t.Context()
			if _, err := p.FetchProfile(ctx, "owner"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.FetchMediaInfo(ctx, "owner", "7", ""); !fault.Is(err, tt.kind) {
				t.Fatal("item classification", err)
			}
			_, storiesErr := p.FetchStories(ctx, "owner", "123")
			_, trayErr := p.FetchHighlightsTray(ctx, "owner", "123")
			if tt.global {
				if !fault.Is(storiesErr, tt.kind) || !fault.Is(trayErr, tt.kind) || calls.Load() != 2 {
					t.Fatal("global stop lost")
				}
			} else if storiesErr != nil || trayErr != nil || calls.Load() != 4 {
				t.Fatal("independent categories blocked", storiesErr, trayErr)
			}
		})
	}
}

func TestMetadataBudgetStopsLaterCategories(t *testing.T) {
	p, calls := localCookieProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, testPage(`{"username":"owner","pk":"123"}`, `{"pk":"123"}`, "", ""))
	})
	if _, err := p.FetchProfile(t.Context(), "owner"); err != nil {
		t.Fatal(err)
	}
	p.requests = 128
	_, err := p.FetchMediaInfo(t.Context(), "owner", "7", "")
	_, next := p.FetchStories(t.Context(), "owner", "123")
	if !fault.Is(err, fault.Budget) || !fault.Is(next, fault.Budget) || calls.Load() != 1 {
		t.Fatal("metadata budget exceeded")
	}
}

func TestCookieCancellationWhileReadingAndWaiting(t *testing.T) {
	reading := make(chan struct{})
	p, attempts := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(reading)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.FetchProfile(ctx, "owner"); done <- err }()
	select {
	case <-reading:
	case <-ctx.Done():
		t.Fatal("server did not start response")
	}
	wait, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, err := p.FetchProfile(wait, "owner"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued request", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
			t.Fatal("active request", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not stop")
	}
}

func TestCookieMediaReads(t *testing.T) {
	const photo = `{"id":"10_123","pk":"10","media_type":1,"user":{"id":"123"}}`
	const group = `{"id":"highlight:7","title":"sample","user":{"pk":"123"},"items":[` + photo + `]}`
	page := func(node string, next bool) string {
		return fmt.Sprintf(`{"edges":[{"node":%s}],"page_info":{"has_next_page":%t,"end_cursor":"opaque"}}`, node, next)
	}
	ctx := context.Background()
	p, n := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/owner/":
			http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "synthetic", Path: "/", Secure: true})
			fmt.Fprint(w, testPage(`{"username":"owner","pk":"123"}`, `{"id":123}`, page(photo, true), page(group, false)))
		case "/api/graphql":
			if r.Method != http.MethodPost || r.Header.Get("X-CSRFToken") != "synthetic" {
				t.Error("missing same-origin session binding")
			}
			var v object
			if json.Unmarshal([]byte(r.FormValue("variables")), &v) != nil {
				t.Error("invalid variables")
			}
			switch r.FormValue("doc_id") {
			case postsNextDoc:
				if v["username"] != "owner" || v["after"] != "opaque" {
					t.Error("incorrect pagination")
				}
				fmt.Fprint(w, `{"data":{"`+postsConnection+`":{"edges":[],"page_info":{"has_next_page":false}}}}`)
			case reelsFirstDoc:
				if v["user_id"] != "123" {
					t.Error("unexpected clips target")
				}
				fmt.Fprint(w, `{"data":{"fetch__XDTUserDict":{"id":"123","clips_connection":`+page(`{"media":{"pk":"22","media_type":2,"product_type":"clips","user":{"pk":"123"},"image_versions2":{"candidates":[{"url":"https://test.cdninstagram.com/poster.jpg"}]}}}`, false)+`}}}`)
			case storiesDoc:
				fmt.Fprint(w, `{"data":{"`+storiesContainer+`":{"reels_media":[{"id":"123","items":[`+photo+`]}]}}}`)
			case highlightsFirstDoc:
				fmt.Fprint(w, `{"data":{"`+highlightsConnection+`":`+page(group, false)+`}}`)
			default:
				t.Error("unrecognized read operation")
				w.WriteHeader(400)
			}
		case "/api/v1/media/22/info/":
			if r.Method != http.MethodGet || r.Header.Get("X-IG-App-ID") != "936619743392459" ||
				r.Header.Get("X-CSRFToken") != "synthetic" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("missing same-origin media info request binding")
			}
			fmt.Fprint(w, `{"items":[{"pk":"22","media_type":2,"user":{"id":"123"},"video_versions":[{"url":"https://test.cdninstagram.com/video.mp4"}]}],"status":"ok"}`)
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	})
	if _, err := p.FetchStories(ctx, "owner", "123"); !fault.Is(err, fault.Authentication) || n.Load() != 0 {
		t.Fatal("media before authentication")
	}
	if _, err := p.FetchProfile(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	items, info, owner, err := p.FetchPostsPage(ctx, "owner", "")
	if err != nil || owner != "123" || len(items) != 1 || !info.HasNextPage || n.Load() != 1 {
		t.Fatal("prefetched posts", err)
	}
	if items, info, _, err = p.FetchPostsPage(ctx, "owner", info.EndCursor); err != nil || len(items) != 0 || info.HasNextPage {
		t.Fatal("posts continuation", err)
	}
	reels, _, err := p.FetchReelsPage(ctx, "owner", "123", "")
	if err != nil || len(reels) != 1 || BestVideoURL(reels[0]) != "" {
		t.Fatal("unhydrated clip", err)
	}
	video, err := p.FetchMediaInfo(ctx, "owner", reels[0].PK, "")
	if err != nil || BestVideoURL(video) == "" {
		t.Fatal("selected item", err)
	}
	if items, err = p.FetchStories(ctx, "owner", "123"); err != nil || len(items) != 1 {
		t.Fatal("stories", err)
	}
	tray, err := p.FetchHighlightsTray(ctx, "owner", "123")
	if err != nil || len(tray) != 1 {
		t.Fatal("highlight tray", err)
	}
	groups, _, err := p.FetchHighlightsPage(ctx, "owner", []string{tray[0].ID}, "", 2)
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 || n.Load() != 6 {
		t.Fatal("highlight items", err)
	}
	if _, _, err = p.FetchHighlightsPage(ctx, "owner", []string{"highlight:99"}, "", 2); !fault.Is(err, fault.Permission) || n.Load() != 6 {
		t.Fatal("unselected highlight requested", err)
	}
}

func TestCookieMetadataLimit(t *testing.T) {
	p, n := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testPage(`{"username":"owner","pk":"123"}`, `{"id":"123"}`, "", ""))
	})
	p.requests = 127
	if _, err := p.FetchProfile(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FetchStories(context.Background(), "owner", "123"); !fault.Is(err, fault.Budget) || n.Load() != 1 {
		t.Fatal("request limit missed", err)
	}
}

func TestDeferredProfileAndTimeline(t *testing.T) {
	const shell = `<script type="application/json">{"require":[["ScheduledServerJS","handle",[],[{"__bbox":{"define":[["PolarisViewer",[],{"id":"12","data":{"id":"12","username":"viewer"}}],["DTSGInitialData",[],{"token":"form-test"}],["LSD",[],{"token":"lsd-test"}]],"require":[["CometPlatformRootClient","initialize",[],[{"initialRouteInfo":{"route":{"params":{"username":"owner"},"rootView":{"resource":{"__dr":"PolarisProfilePostsTabRoot.react"},"props":{"id":"34"}}}}}]]]}}]]]}</script>`
	for _, tc := range []struct {
		name, account string
		kind          fault.Kind
	}{
		{"public profile", `"id":"34","is_private":false`, ""},
		{"different ID", `"id":"56"`, fault.Permission},
		{"private without access", `"id":"34","is_private":true`, fault.Permission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p, count := localCookieProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/owner/" {
					fmt.Fprint(w, shell)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" ||
					r.FormValue("fb_dtsg") != "form-test" || r.Header.Get("X-FB-LSD") != "lsd-test" {
					t.Error("incorrect deferred read")
				}
				var vars object
				_ = json.Unmarshal([]byte(r.FormValue("variables")), &vars)
				switch r.FormValue("doc_id") {
				case profileDoc:
					if vars["id"] != "34" || vars["__relay_internal__pv__PolarisProfileInformTreatmentBannerEnabledrelayprovider"] != true {
						t.Error("profile query used another account")
					}
					fmt.Fprintf(w, `{"data":{"user":{"username":"owner",%s},"viewer":{"user":{"id":"12"}}}}`, tc.account)
				case postsFirstDoc:
					if vars["username"] != "owner" || vars["after"] != nil || vars["__relay_internal__pv__PolarisSEOIsCrawlerrelayprovider"] != false {
						t.Error("incorrect first timeline query")
					}
					fmt.Fprint(w, `{"data":{"`+postsConnection+`":{"edges":[{"node":{"pk":"90","media_type":1,"user":{"pk":"34"},"display_url":"https://example.cdninstagram.com/photo"}}],"page_info":{"has_next_page":false}}}}`)
				default:
					t.Error("unexpected deferred operation")
					w.WriteHeader(http.StatusBadRequest)
				}
			})
			profile, err := p.FetchProfile(ctx, "owner")
			if tc.kind != "" {
				if !fault.Is(err, tc.kind) || profile.UserID != "" || count.Load() != 2 {
					t.Fatal("unverified deferred profile", err)
				}
				return
			}
			if err != nil || profile.UserID != "34" || p.OwnProfile() {
				t.Fatal("incorrect deferred profile", err)
			}
			items, _, _, err := p.FetchPostsPage(ctx, profile.Username, "")
			if err != nil || len(items) != 1 || BestImageURL(items[0]) == "" || count.Load() != 3 {
				t.Fatal("missing first page", err)
			}
		})
	}
}
