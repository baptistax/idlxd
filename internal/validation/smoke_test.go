package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func photoBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func syntheticOptions(t *testing.T) SmokeOptions {
	t.Helper()
	return SmokeOptions{Authorized: true, Username: "owner", AccountID: "123", MediaID: "456", AccessToken: "synthetic-token", OutputDir: t.TempDir()}
}

// Only this test transport maps fixed API/CDN names to a local server. Production
// code has no remote-host override and never receives this transport.
func localTransport(t *testing.T, h http.HandlerFunc) http.RoundTripper {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	destination, _ := url.Parse(srv.URL)
	return transportFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = destination.Scheme
		u.Host = destination.Host
		clone.URL = &u
		return srv.Client().Transport.RoundTrip(clone)
	})
}

func TestSmokeDownloadsExactlyOneSelectedPhoto(t *testing.T) {
	jpeg := photoBytes(t)
	var calls atomic.Int32
	base := localTransport(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v27.0/123":
			if r.Header.Get("Authorization") != "Bearer synthetic-token" {
				t.Error("missing API authorization")
			}
			fmt.Fprint(w, `{"id":"123","username":"owner"}`)
		case "/v27.0/456":
			fmt.Fprint(w, `{"id":"456","media_type":"IMAGE","media_product_type":"FEED","owner":{"id":"123"},"media_url":"https://synthetic.cdninstagram.com/selected.jpg?sig=synthetic-secret"}`)
		case "/selected.jpg":
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("credentials forwarded to CDN")
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpeg)
		default:
			t.Error("unapproved listing or category request")
			w.WriteHeader(404)
		}
	})
	opts := syntheticOptions(t)
	d, err := runGraphSmoke(context.Background(), opts, base)
	if err != nil || d.Requests != 3 || calls.Load() != 3 || d.Validation != "one decodable photo verified" {
		t.Fatalf("diagnostic=%+v err=%v", d, err)
	}
	files, err := filepath.Glob(filepath.Join(opts.OutputDir, "smoke-*", "selected.media"))
	if err != nil || len(files) != 1 {
		t.Fatal("expected one saved file")
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(data, jpeg) {
		t.Fatal("downloaded content changed")
	}
	if strings.Contains(fmt.Sprint(d), "synthetic-secret") || strings.Contains(fmt.Sprint(d), "owner") {
		t.Fatal("private diagnostic leaked")
	}
}

func TestSmokeStopsAtFirstFailure(t *testing.T) {
	for _, tt := range []struct {
		name, body       string
		status, at, want int
		kind             fault.Kind
	}{
		{"auth", `{}`, 401, 1, 1, fault.Authentication},
		{"permission alongside data", `{"data":{},"errors":[{"message":"access denied synthetic-secret"}]}`, 200, 1, 1, fault.Permission},
		{"challenge", `{"error":{"message":"challenge_required synthetic-secret"}}`, 200, 1, 1, fault.Verification},
		{"rate", `{"error":{"code":4}}`, 429, 1, 1, fault.RateLimited},
		{"metadata missing", `{}`, 200, 2, 2, fault.Schema},
		{"metadata mismatch", `{"id":"999","media_type":"IMAGE","media_product_type":"FEED"}`, 200, 2, 2, fault.Permission},
		{"unselected video", `{"id":"456","media_type":"VIDEO","media_product_type":"REELS"}`, 200, 2, 2, fault.Unsupported},
		{"media rejected", `synthetic-secret`, 403, 3, 3, fault.Permission},
		{"media corrupt", `synthetic-secret`, 200, 3, 3, fault.Schema},
		{"redirect", `synthetic-secret`, 302, 1, 1, fault.Upstream},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			base := localTransport(t, func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				if n == tt.at {
					w.Header().Set("Retry-After", "120")
					w.Header().Set("Location", "https://never.invalid/")
					w.WriteHeader(tt.status)
					fmt.Fprint(w, tt.body)
					return
				}
				if n == 1 {
					fmt.Fprint(w, `{"id":"123","username":"owner"}`)
				} else {
					fmt.Fprint(w, `{"id":"456","media_type":"IMAGE","media_product_type":"FEED","owner":{"id":"123"},"media_url":"https://synthetic.cdninstagram.com/selected.jpg"}`)
				}
			})
			opts := syntheticOptions(t)
			d, err := runGraphSmoke(context.Background(), opts, base)
			if !fault.Is(err, tt.kind) || d.Classification != tt.kind || d.Requests != tt.want || int(calls.Load()) != tt.want || strings.Contains(fmt.Sprint(d), "synthetic-secret") {
				t.Fatalf("diagnostic=%+v err=%v", d, err)
			}
			if tt.at == 3 && tt.status == 200 {
				files, _ := filepath.Glob(filepath.Join(opts.OutputDir, "smoke-*", "selected.media"))
				if len(files) != 0 {
					t.Fatal("corrupt file retained as validated")
				}
			}
		})
	}
}

func TestSmokePreflightAndCancellationMakeNoRequests(t *testing.T) {
	calls := 0
	base := transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })
	opts := syntheticOptions(t)
	opts.Authorized = false
	if d, err := runGraphSmoke(context.Background(), opts, base); !fault.Is(err, fault.Permission) || d.Requests != 0 {
		t.Fatal("unapproved smoke attempted")
	}
	opts.Authorized = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, err := runGraphSmoke(ctx, opts, base); !errors.Is(err, context.Canceled) || d.Requests != 0 {
		t.Fatal("cancelled smoke attempted")
	}
	opts.MediaID = "../unsafe"
	if _, err := runGraphSmoke(context.Background(), opts, base); !fault.Is(err, fault.InvalidInput) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("preflight sent a request")
	}
}

func TestSmokeStopsOnRejectionHeaders(t *testing.T) {
	base := localTransport(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusForbidden)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := runGraphSmoke(ctx, syntheticOptions(t), base)
	if ctx.Err() != nil || !fault.Is(err, fault.Permission) || d.Requests != 1 || d.LastHTTPStatus != 403 {
		t.Fatal("smoke waited for a rejected body", err)
	}
}

func TestRequestBudgetAndSequentialEnforcement(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprint(concurrent), func(t *testing.T) {
			calls := 0
			g := &requestGuard{limit: 1, allow: func(*http.Request) bool { return true }, base: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("synthetic"))}, nil
			})}
			r, _ := http.NewRequest(http.MethodGet, "https://synthetic.invalid/", nil)
			resp, err := g.RoundTrip(r)
			if err != nil {
				t.Fatal(err)
			}
			if !concurrent {
				_ = resp.Body.Close()
			}
			_, err = g.RoundTrip(r)
			want := fault.Budget
			if concurrent {
				want = fault.Permission
				_ = resp.Body.Close()
			}
			if !fault.Is(err, want) || calls != 1 {
				t.Fatal("request budget or sequential guard bypassed")
			}
		})
	}
}

func TestRequestHostAndCredentialScope(t *testing.T) {
	for _, raw := range []string{"http://graph.facebook.com/v27.0/123", "https://graph.facebook.com.evil.invalid/v27.0/123", "https://graph.facebook.com/v27.0/999", "https://media.invalid/photo", "https://graph.facebook.com/unsupported/123", "https://user:pass@synthetic.cdninstagram.com/photo"} {
		r, _ := http.NewRequest(http.MethodGet, raw, nil)
		r.Header.Set("Authorization", "Bearer synthetic")
		if approvedRequest(r, "123", "456") {
			t.Fatal("unapproved request permitted")
		}
	}
	r, _ := http.NewRequest(http.MethodGet, "https://synthetic.cdninstagram.com/photo", nil)
	r.Header.Set("Authorization", "Bearer synthetic")
	if approvedRequest(r, "123", "456") {
		t.Fatal("CDN credential transfer permitted")
	}
	r.Header.Del("Authorization")
	r.Header.Set("Cookie", "sessionid=synthetic")
	if approvedRequest(r, "123", "456") {
		t.Fatal("browser session transfer permitted")
	}
}

func TestSmokeCancellationDuringFirstRequest(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	base := localTransport(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(started); <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		diagnostic Diagnostic
		err        error
	}
	done := make(chan outcome, 1)
	opts := syntheticOptions(t)
	go func() { d, err := runGraphSmoke(ctx, opts, base); done <- outcome{d, err} }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("local request did not start")
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || result.diagnostic.Requests != 1 || calls.Load() != 1 || result.diagnostic.Cancellation != "context cancelled" {
			t.Fatalf("diagnostic=%+v err=%v", result.diagnostic, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("smoke ignored cancellation")
	}
}
