//go:build idl_live

package validation

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func TestAuthorizedCookiePhotoSmoke(t *testing.T) {
	if os.Getenv("IDL_COOKIE_SMOKE") != "one-owned-photo-four-requests" {
		t.Skip("cookie smoke is opt-in")
	}
	runLiveCookieTest(t, false, cookieSmokeBudget)
}

func TestAuthorizedCookieReelSmoke(t *testing.T) {
	if os.Getenv("IDL_COOKIE_SMOKE") != "one-owned-reel-five-requests" {
		t.Skip("cookie reel smoke is opt-in")
	}
	runLiveCookieTest(t, true, reelSmokeBudget)
}

func runLiveCookieTest(t *testing.T, reel bool, budget int) {
	t.Helper()
	path, name := os.Getenv("IDL_SMOKE_COOKIES"), os.Getenv("IDL_SMOKE_USERNAME")
	if path == "" || name == "" {
		t.Fatal("cookie path and selected username required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	// A fresh connection per request prevents net/http's transparent retry
	// of an idempotent request on a stale reused connection.
	base := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 12 * time.Second}
	defer base.CloseIdleConnections()
	d, err := runCookieSelection(ctx, path, name, t.TempDir(), base, reel)
	t.Logf("auth=%s profile=%s metadata=%s download=%s validation=%s HTTP=%d attempts=%d/%d classification=%s", d.Authentication, d.Profile, d.Metadata, d.Download, d.Validation, d.LastHTTPStatus, d.Requests, budget, d.Classification)
	if err != nil {
		// Only local classifications are printable. Transport errors can
		// contain the full request URL, including its signature.
		var dns *net.DNSError
		var cert *tls.CertificateVerificationError
		var op *net.OpError
		var local *fault.Error
		switch {
		case errors.As(err, &dns):
			t.Log("network stage: DNS")
		case errors.As(err, &cert):
			t.Log("network stage: TLS certificate verification")
		case errors.As(err, &op) && op.Op == "dial":
			t.Log("network stage: connection")
		case errors.As(err, &local):
			t.Log("local classification:", local.Detail)
		}
		t.Fatal("cookie smoke stopped")
	}
}

// This entrypoint is excluded from ordinary tests. Enabling its build tag alone
// never authorizes a request. The operator must separately approve the exact run.
func TestAuthorizedGraphPhotoSmoke(t *testing.T) {
	if os.Getenv("IDL_LIVE_SMOKE_AUTHORIZED") != "one-photo-three-requests" {
		t.Skip("no explicit approval for this exact smoke scope")
	}
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Fatal("live smoke is prohibited in CI")
	}
	if os.Getenv("IDL_PROVIDER") != "graph" {
		t.Fatal("explicit graph provider selection required; no authentication switch performed")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	diagnostic, err := RunGraphSmoke(ctx, SmokeOptions{
		Authorized: true, Username: os.Getenv("IDL_SMOKE_USERNAME"), MediaID: os.Getenv("IDL_SMOKE_MEDIA_ID"),
		AccessToken: os.Getenv("IDL_ACCESS_TOKEN"), AccountID: os.Getenv("IDL_ACCOUNT_ID"), OutputDir: t.TempDir(),
	})
	t.Logf("provider=%s auth=%s profile=%s category=%s metadata=%s download=%s validation=%s HTTP=%d requests=%d/%d duration=%s classification=%s stop=%s", diagnostic.Provider, diagnostic.Authentication, diagnostic.Profile, diagnostic.Category, diagnostic.Metadata, diagnostic.Download, diagnostic.Validation, diagnostic.LastHTTPStatus, diagnostic.Requests, SmokeRequestBudget, diagnostic.Duration, diagnostic.Classification, diagnostic.Cancellation)
	if err != nil {
		t.Fatal("live smoke stopped; analyze the sanitized classification offline before authorizing another attempt")
	}
}

func RunGraphSmoke(ctx context.Context, opts SmokeOptions) (Diagnostic, error) {
	// No configurable remote host or runtime configuration source is accepted.
	base := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	defer base.CloseIdleConnections()
	return runGraphSmoke(ctx, opts, base)
}
