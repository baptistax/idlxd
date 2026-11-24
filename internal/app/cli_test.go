package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/instagram/auth"
)

func TestLocalCommands(t *testing.T) {
	t.Setenv("IDL_PROVIDER", "cookies")
	t.Chdir(t.TempDir())
	for _, tt := range []struct{ name, file, message string }{
		{"missing", "", "cookies.txt not found."},
		{"empty export", "[]", "Invalid cookie file."},
		{"wrong scope", ".instagram.com TRUE /elsewhere TRUE 1 sessionid fixture", "Invalid cookie file."},
		{"expired", ".instagram.com TRUE / TRUE 1 sessionid fixture", "Session expired. Update cookies.txt."},
		{"usable", ".instagram.com TRUE / TRUE 0 sessionid fixture", "Authentication unavailable."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.ParseArgs([]string{"example_user"})
			if err != nil {
				t.Fatal(err)
			}
			cfg.CookiesPath = filepath.Join(t.TempDir(), "cookies.txt")
			if tt.file != "" {
				if err := os.WriteFile(cfg.CookiesPath, []byte(tt.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			base := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 401, Body: http.NoBody}, nil
			})}
			out := captureOutput(t, func() {
				err = RunWithProvider(context.Background(), cfg, instagram.NewCookieProviderWithClient(cfg.CookiesPath, base))
			})
			if err == nil || strings.Count(out, tt.message) != 1 || !strings.Contains(out, "Download not started.\nFinished in ") ||
				strings.ContainsAny(out, "\r\x1b") || strings.Contains(out, "[OK] Authentication") || strings.Contains(out, "fixture") {
				t.Fatalf("download output %q", out)
			}
			if _, err := os.Stat(filepath.Join(cfg.OutputRoot, "example_user")); !os.IsNotExist(err) {
				t.Fatal("target directory created")
			}
			cfg.Status = true
			out = captureOutput(t, func() { err = Run(context.Background(), cfg) })
			if tt.name == "usable" {
				if err != nil || !strings.Contains(out, "[OK] Session file\n[OK] Local storage\n[--] Authentication: not verified\n[--] Instagram connectivity: not checked\n[OK] Download engine\n\nStatus: Limited") {
					t.Fatal(out, err)
				}
			} else if err == nil || !strings.Contains(out, "Status: Unavailable") || !strings.Contains(out, tt.message) {
				t.Fatal(out)
			}
			entries, err := os.ReadDir(cfg.OutputRoot)
			if err != nil || len(entries) != 0 {
				t.Fatal("unexpected status artifacts", entries, err)
			}
		})
	}
}

func TestStatusStorageFailure(t *testing.T) {
	cfg := config.Config{Status: true, Provider: config.GraphProvider, AccessToken: "fixture", AccountID: "123", OutputRoot: filepath.Join(t.TempDir(), "file")}
	if err := os.WriteFile(cfg.OutputRoot, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureOutput(t, func() { err = Run(context.Background(), cfg) })
	if err == nil || !strings.Contains(out, "[!!] Local storage") || !strings.Contains(out, "[OK] Graph configuration") || strings.Contains(out, "[OK] Authentication") {
		t.Fatal(out)
	}
}

func TestSafeMessages(t *testing.T) {
	for _, tt := range []struct {
		err     error
		message string
	}{
		{auth.ErrUnreadable, "Cookie access unavailable."},
		{instagram.ErrProfileNotFound, "Profile not found."},
		{fault.New(fault.Network, "private-host", "signed-url"), "Connection failed."},
		{errors.Join(fault.New(fault.Schema, "secret", ""), fault.New(fault.Permission, "secret", "")), "Access restricted."},
		{errors.New("raw upstream body"), "Completed with errors."},
	} {
		if got := userMessage(tt.err); got != tt.message {
			t.Fatal(got)
		}
	}
}

func TestSpinnerReplacedOnCancellation(t *testing.T) {
	var out bytes.Buffer
	ui := terminal{out: &out, animate: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := ui.wait(ctx, "Profile", func() error {
		time.Sleep(125 * time.Millisecond)
		cancel()
		return ctx.Err()
	})
	ui.check("Profile", err)
	if !errors.Is(err, context.Canceled) || !strings.Contains(out.String(), "[|] Profile") || !strings.HasSuffix(out.String(), "\r[!!] Profile\n") {
		t.Fatalf("spinner output %q", out.String())
	}
}
