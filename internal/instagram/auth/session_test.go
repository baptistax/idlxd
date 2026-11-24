package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/fault"
)

func TestLocalSessionFormatsAndScopes(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		valid      bool
	}{
		{"Netscape", "# Netscape HTTP Cookie File\n#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t0\tsessionid\tsynthetic-session\n", true},
		{"Cookie Editor", `[{"name":"sessionid","value":"synthetic-session","domain":".instagram.com","secure":true,"httpOnly":true,"path":"/","session":true}]`, true},
		{"Cookie Editor container", `{"cookies":[{"name":"sessionid","value":"synthetic-session","domain":".instagram.com","path":"/","session":true}]}`, true},
		{"expired", `.instagram.com TRUE / TRUE 1 sessionid synthetic-session`, false},
		{"malformed expiry", `.instagram.com TRUE / TRUE invalid sessionid synthetic-session`, false},
		{"negative expiry", `.instagram.com TRUE / TRUE -1 sessionid synthetic-session`, false},
		{"host-only parent", `instagram.com FALSE / TRUE 0 sessionid synthetic-session`, false},
		{"host-only target", `www.instagram.com FALSE / TRUE 0 sessionid synthetic-session`, true},
		{"JSON host-only parent", `{"domain":".instagram.com","hostOnly":true,"name":"sessionid","value":"fixture"}`, false},
		{"JSON domain cookie", `{"domain":"instagram.com","hostOnly":false,"name":"sessionid","value":"fixture"}`, true},
		{"JSON malformed expiry", `{"domain":".instagram.com","name":"sessionid","value":"fixture","expirationDate":"invalid"}`, false},
		{"foreign domain", `evilinstagram.com TRUE / TRUE 0 sessionid synthetic-session`, false},
		{"foreign suffix", `instagram.com.evil.invalid TRUE / TRUE 0 sessionid synthetic-session`, false},
		{"wrong subdomain", `i.instagram.com FALSE / TRUE 0 sessionid synthetic-session`, false},
		{"missing session", `.instagram.com TRUE / TRUE 0 csrftoken synthetic-session`, false},
		{"invalid bytes", `.instagram.com TRUE / TRUE 0 sessionid synthetic"session`, false},
		{"malformed JSON", `[{"value":"synthetic-session"`, false},
		{"wrong fields", `[{"name":"sessionid","value":123,"domain":".instagram.com"}]`, false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cookies.txt")
			if err := os.WriteFile(path, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			jar, err := LoadSessionJar(path)
			if tt.valid {
				if err != nil || jar == nil {
					t.Fatal("synthetic session rejected", err)
				}
			} else if !fault.Is(err, fault.Authentication) || jar != nil {
				t.Fatal("invalid session accepted")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-session") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestMissingCookieFileIsSanitized(t *testing.T) {
	_, err := LoadSessionJar(filepath.Join(t.TempDir(), "private-session-name.txt"))
	if !fault.Is(err, fault.Authentication) || strings.Contains(err.Error(), "private-session-name") {
		t.Fatal(err)
	}
}
