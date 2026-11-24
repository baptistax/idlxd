package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baptistax/idl/internal/fault"
)

func TestProviderSelectionIsExplicit(t *testing.T) {
	t.Setenv("IDL_ACCESS_TOKEN", "synthetic-token")
	t.Setenv("IDL_ACCOUNT_ID", "123")
	for _, tt := range []struct {
		value, want string
		invalid     bool
	}{
		{"", CookiesProvider, false}, {"cookies", CookiesProvider, false}, {" GRAPH ", GraphProvider, false}, {"automatic", "", true},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("IDL_PROVIDER", tt.value)
			cfg, err := ParseArgs([]string{" @OWNER "})
			if tt.invalid {
				if !fault.Is(err, fault.InvalidInput) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || cfg.Provider != tt.want || cfg.Username != "owner" || cfg.CookiesPath != "cookies.txt" {
				t.Fatalf("configuration mismatch: %v", err)
			}
			if tt.want == CookiesProvider && (cfg.AccessToken != "" || cfg.AccountID != "") {
				t.Fatal("Graph environment silently selected or retained for cookies")
			}
			if tt.want == GraphProvider && (cfg.AccessToken != "synthetic-token" || cfg.AccountID != "123") {
				t.Fatal("explicit Graph credentials not loaded")
			}
		})
	}
}

func TestStatusWithoutUsername(t *testing.T) {
	t.Setenv("IDL_PROVIDER", "")
	cfg, err := ParseArgs([]string{"status"})
	if err != nil || !cfg.Status || cfg.Username != "" {
		t.Fatal("status requires no target", err)
	}
	cfg, err = ParseArgs([]string{"@status"})
	if err != nil || cfg.Status || cfg.Username != "status" {
		t.Fatal("status username unreachable", err)
	}
}

func TestCookieDiscoveryPreservesOriginalOrder(t *testing.T) {
	exeDir := t.TempDir()
	exe := func() (string, error) { return filepath.Join(exeDir, "idl.exe"), nil }
	candidate := filepath.Join(exeDir, "cookies.txt")
	if err := os.WriteFile(candidate, []byte("synthetic cookie file"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := resolveCookiesPath("cookies.txt", exe); got != candidate {
		t.Fatalf("executable location not preferred: %s", got)
	}
	absentExe := func() (string, error) { return filepath.Join(t.TempDir(), "idl.exe"), nil }
	if got := resolveCookiesPath("cookies.txt", absentExe); got != "cookies.txt" {
		t.Fatal("working-directory fallback changed")
	}
	if got := resolveCookiesPath("", absentExe); got != "cookies.txt" {
		t.Fatal("default filename changed")
	}
	if got := resolveCookiesPath(candidate, func() (string, error) { t.Fatal("absolute path queried executable"); return "", nil }); got != candidate {
		t.Fatal("absolute path changed")
	}
	if got := resolveCookiesPath("cookies.txt", func() (string, error) { return "", errors.New("synthetic executable lookup error") }); got != "cookies.txt" {
		t.Fatal("executable lookup failure removed fallback")
	}
}

func TestBundledSessionIsLastResort(t *testing.T) {
	t.Chdir(t.TempDir())
	noExe := func() (string, error) { return "", errors.New("no executable") }
	if err := os.Mkdir("files", 0700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join("files", DefaultCookiesPath)
	for _, path := range []string{bundle, DefaultCookiesPath} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if got := resolveCookiesPath(DefaultCookiesPath, noExe); got != path {
			t.Fatalf("expected %s, got %s", path, got)
		}
	}
}
