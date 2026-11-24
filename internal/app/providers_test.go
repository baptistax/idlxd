package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
)

func TestProviderFactoryDoesNotSwitchAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(".instagram.com TRUE / TRUE 0 sessionid synthetic-session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", config.CookiesProvider} {
		p, err := newProvider(config.Config{Provider: name, CookiesPath: path, AccessToken: "synthetic-token", AccountID: "123"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := p.(*instagram.CookieProvider); !ok {
			t.Fatal("cookies silently replaced by another provider")
		}
		if !p.Capabilities()["posts"].Supported {
			t.Fatal("web provider has no timeline reader")
		}
	}
	if _, err := newProvider(config.Config{Provider: config.GraphProvider, CookiesPath: path}); !fault.Is(err, fault.Authentication) {
		t.Fatal("Graph failure fell back to cookies")
	}
	p, err := newProvider(config.Config{Provider: config.GraphProvider, AccessToken: "synthetic-token", AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*instagram.Client); !ok {
		t.Fatal("explicit Graph provider not selected")
	}
	if _, err := newProvider(config.Config{Provider: "unknown"}); !fault.Is(err, fault.InvalidInput) {
		t.Fatal(err)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunWithCookieOperationReachesAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(".instagram.com TRUE / TRUE 0 sessionid synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "sessionid=synthetic" || r.URL.Path != "/owner/" {
			t.Error("wrong credential scope or account")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	localURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		local := r.Clone(r.Context())
		local.URL.Host = localURL.Host
		return server.Client().Transport.RoundTrip(local)
	})}
	p := instagram.NewCookieProviderWithClient(path, base)
	cfg := config.Config{Username: "owner", OutputRoot: t.TempDir()}
	out := captureOutput(t, func() { err = RunWithProvider(context.Background(), cfg, p) })
	if !fault.Is(err, fault.Permission) || calls.Load() != 1 || !strings.Contains(out, "[OK] Session file") ||
		!strings.Contains(out, "Access restricted.") || !strings.Contains(out, "Download not started.") ||
		strings.Contains(out, "[OK] Authentication") || strings.Contains(out, "synthetic") {
		t.Fatal("unexpected authentication outcome", err)
	}
}
