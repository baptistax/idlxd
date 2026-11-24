package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	c, err := newGraphClient(Options{AccessToken: "synthetic-token", AccountID: "123", HTTPClient: srv.Client()}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestAuthorizedGraphWorkflow(t *testing.T) {
	var calls []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+"?"+r.URL.Query().Get("after"))
		if r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != "IDL/2.0" || r.URL.Query().Get("access_token") != "" {
			t.Error("invalid credential transport")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/123":
			fmt.Fprint(w, `{"id":123,"username":"owner","media_count":3}`)
		case "/123/media":
			fmt.Fprint(w, `{"data":[{"id":"1","media_type":"IMAGE","media_product_type":"FEED","media_url":"https://media.invalid/photo.jpg","owner":{"id":"123"},"timestamp":"2024-01-01T00:00:00+0000"},{"id":"2","media_type":"VIDEO","media_product_type":"REELS","media_url":"https://media.invalid/video.mp4","owner":{"id":"123"}},{"id":"3","media_type":"CAROUSEL_ALBUM","media_product_type":"FEED","owner":{"id":"123"},"timestamp":"2024-01-01T00:00:00Z"}]}`)
		case "/3/children":
			if r.URL.Query().Get("after") == "" {
				fmt.Fprint(w, `{"data":[{"id":"4","media_type":"IMAGE","media_url":"https://media.invalid/child.jpg"}],"paging":{"next":"https://third-party.invalid/?access_token=never-follow","cursors":{"after":"child-page-2"}}}`)
			} else if r.URL.Query().Get("after") == "child-page-2" {
				fmt.Fprint(w, `{"data":[{"id":"5","media_type":"VIDEO","media_url":"https://media.invalid/child.mp4"}]}`)
			} else {
				t.Error("wrong child cursor")
			}
		case "/123/stories":
			fmt.Fprint(w, `{"data":[{"id":"6","media_type":"IMAGE","media_product_type":"STORY","media_url":"https://media.invalid/story.jpg","owner":{"id":"123"}}]}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if _, _, _, err := c.FetchPostsPage(context.Background(), "owner", ""); !fault.Is(err, fault.Authentication) || len(calls) != 0 {
		t.Fatal("unverified access attempted")
	}
	p, err := c.FetchProfile(context.Background(), "@OWNER")
	if err != nil || p.UserID != "123" || !p.PostsCountKnown || p.Provenance != "graph-facebook/"+GraphVersion {
		t.Fatalf("%+v %v", p, err)
	}
	posts, page, uid, err := c.FetchPostsPage(context.Background(), "owner", "")
	if err != nil || page.HasNextPage || uid != "123" || len(posts) != 2 {
		t.Fatalf("posts=%+v err=%v", posts, err)
	}
	if BestImageURL(posts[0]) == "" || len(posts[1].CarouselMedia) != 2 || BestVideoURL(posts[1].CarouselMedia[1]) == "" || posts[1].CarouselMedia[0].TakenAt != posts[1].TakenAt {
		t.Fatal("incomplete normalized photo/carousel")
	}
	reels, _, err := c.FetchReelsPage(context.Background(), "owner", "123", "")
	if err != nil || len(reels) != 1 || BestVideoURL(reels[0]) == "" {
		t.Fatal(err)
	}
	stories, err := c.FetchStories(context.Background(), "owner", "123")
	if err != nil || len(stories) != 1 {
		t.Fatal(err)
	}
	before := len(calls)
	if _, err := c.FetchHighlightsTray(context.Background(), "owner", "123"); !fault.Is(err, fault.Unsupported) {
		t.Fatal(err)
	}
	if _, _, err := c.FetchReelsPage(context.Background(), "other", "123", ""); !fault.Is(err, fault.Permission) {
		t.Fatal(err)
	}
	if _, err := c.FetchMediaInfo(context.Background(), "owner", "2", ""); !fault.Is(err, fault.Unsupported) {
		t.Fatal(err)
	}
	if len(calls) != before {
		t.Fatal("unsupported or unauthorized operation made a request")
	}
}

func TestGraphFailureStopsWithoutFallback(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		http       int
		kind       fault.Kind
		blocked    bool
	}{
		{"legacy", `{"__ar":1,"error":1357004,"errorSummary":"synthetic-secret","payload":null}`, 200, fault.Upstream, false},
		{"application denial", `{"error":{"code":200,"message":"synthetic-secret"}}`, 200, fault.Permission, true},
		{"verification with data", `{"data":{},"errors":[{"message":"checkpoint synthetic-secret"}]}`, 200, fault.Verification, true},
		{"http auth", `{}`, 401, fault.Authentication, true},
		{"http denial", `<html>synthetic-secret</html>`, 403, fault.Permission, true},
		{"rate", `{"error":{"code":4}}`, 429, fault.RateLimited, true},
		{"upstream", `synthetic-secret`, 503, fault.Upstream, false},
		{"malformed", `<html>synthetic-secret</html>`, 200, fault.Schema, false},
		{"missing fields", `{}`, 200, fault.Schema, false},
		{"wrong identity", `{"id":"123","username":"other"}`, 200, fault.Permission, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(tt.http)
				fmt.Fprint(w, tt.body)
			})
			_, err := c.FetchProfile(context.Background(), "owner")
			if !fault.Is(err, tt.kind) || strings.Contains(err.Error(), "synthetic-secret") || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if tt.kind == fault.RateLimited {
				var e *fault.Error
				if !errors.As(err, &e) || e.RetryAfter != time.Minute {
					t.Fatal(err)
				}
			}
			if tt.blocked {
				_, _ = c.FetchProfile(context.Background(), "owner")
				if calls != 1 {
					t.Fatal("retry after global denial")
				}
			}
		})
	}
}

func TestGraphMediaContracts(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		kind       fault.Kind
		empty      bool
	}{
		{"empty", `{"data":[]}`, "", true},
		{"missing", `{}`, fault.Schema, false},
		{"null", `{"data":null}`, fault.Schema, false},
		{"no cursor", `{"data":[],"paging":{"next":"https://never.invalid/"}}`, fault.Schema, false},
		{"no owner", `{"data":[{"id":"1","media_type":"IMAGE","media_product_type":"FEED"}]}`, fault.Schema, false},
		{"foreign owner", `{"data":[{"id":"1","owner":{"id":"999"},"media_type":"IMAGE","media_product_type":"FEED"}]}`, fault.Permission, false},
		{"foreign skipped reel", `{"data":[{"id":"1","owner":{"id":"999"},"media_type":"VIDEO","media_product_type":"REELS"}]}`, fault.Permission, false},
		{"missing product", `{"data":[{"id":"1","owner":{"id":"123"},"media_type":"VIDEO"}]}`, fault.Schema, false},
		{"duplicate", `{"data":[{"id":"1","owner":{"id":"123"},"media_type":"IMAGE","media_product_type":"FEED"},{"id":"1","owner":{"id":"123"},"media_type":"IMAGE","media_product_type":"FEED"}]}`, fault.Schema, false},
		{"optional URL", `{"data":[{"id":"1","owner":{"id":"123"},"media_type":"VIDEO","media_product_type":"FEED"}]}`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) })
			c.profile = Profile{Username: "owner", UserID: "123"}
			items, _, _, err := c.FetchPostsPage(context.Background(), "owner", "")
			if tt.kind != "" {
				if !fault.Is(err, tt.kind) {
					t.Fatalf("expected %s, got %v", tt.kind, err)
				}
				return
			}
			if err != nil || (len(items) == 0) != tt.empty {
				t.Fatalf("items=%v err=%v", items, err)
			}
		})
	}
}

func TestStoriesTraversalRepeatedCursorAndCancellation(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"data":[{"id":"%d","owner":{"id":"123"},"media_type":"IMAGE"}],"paging":{"next":"https://never.invalid/","cursors":{"after":"repeated"}}}`, calls)
	})
	c.profile = Profile{Username: "owner", UserID: "123"}
	if _, err := c.FetchStories(context.Background(), "owner", "123"); !fault.Is(err, fault.Schema) || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchStories(ctx, "owner", "123"); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRedirectDoesNotForwardToken(t *testing.T) {
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++ }))
	defer target.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) })
	if _, err := c.FetchProfile(context.Background(), "owner"); err == nil || redirected != 0 {
		t.Fatalf("redirected=%d error=%v", redirected, err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkErrorSanitization(t *testing.T) {
	underlying := errors.New("synthetic-secret https://private.invalid/?access_token=synthetic-secret")
	c, err := NewClient(Options{AccountID: "123", AccessToken: "synthetic", HTTPClient: &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) { return nil, underlying })}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.FetchProfile(context.Background(), "owner")
	if !fault.Is(err, fault.Network) || !errors.Is(err, underlying) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal(err)
	}
}

func TestRateLimitHTTPDate(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(time.Minute).UTC().Format(http.TimeFormat))
		w.WriteHeader(429)
		fmt.Fprint(w, `{}`)
	})
	var out json.RawMessage
	err := c.get(context.Background(), "123", "id", "", &out)
	var e *fault.Error
	if !errors.As(err, &e) || e.Kind != fault.RateLimited || e.RetryAfter <= 0 || e.RetryAfter > time.Minute {
		t.Fatal(err)
	}
}

func TestCancellationDuringRequest(t *testing.T) {
	started := make(chan struct{})
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.FetchProfile(ctx, "owner"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request ignored cancellation")
	}
	if c.profile.UserID != "" {
		t.Fatal("cancelled request verified a profile")
	}
}

func TestIncompleteCarouselIsNotReturnedAsComplete(t *testing.T) {
	children := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/123/media" {
			fmt.Fprint(w, `{"data":[{"id":"1","owner":{"id":"123"},"media_type":"CAROUSEL_ALBUM","media_product_type":"FEED"}]}`)
			return
		}
		children++
		if children == 1 {
			fmt.Fprint(w, `{"data":[{"id":"2","media_type":"IMAGE"}],"paging":{"next":"https://never.invalid/","cursors":{"after":"next"}}}`)
		} else {
			fmt.Fprint(w, `{"data":null}`)
		}
	})
	c.profile = Profile{Username: "owner", UserID: "123"}
	items, _, _, err := c.FetchPostsPage(context.Background(), "owner", "")
	if len(items) != 0 || !fault.Is(err, fault.Schema) || children != 2 {
		t.Fatalf("items=%v requests=%d err=%v", items, children, err)
	}
}
