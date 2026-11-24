package auth

import (
	"encoding/json"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestParseCookiesJSONCookieEditor(t *testing.T) {
	// Entirely synthetic values, including the escaped-cookie regression.
	input := []byte("\ufeff \n" + `[{"name":"sessionid","value":"synthetic\\\"session","domain":".instagram.com","path":"/","secure":true,"httpOnly":true,"expirationDate":1801846439.938},{"name":"rur","value":"synthetic-value","domain":".instagram.com","session":true}]`)
	cookies, err := parseCookies(input)
	if err != nil || len(cookies) != 2 {
		t.Fatalf("cookie parsing failed: %v", err)
	}
	if cookies[0].Name != "sessionid" || cookies[0].Domain != "instagram.com" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Expires.Unix() != 1801846439 {
		t.Fatal("cookie metadata changed")
	}
	if !cookies[1].Expires.IsZero() {
		t.Fatal("session cookie has an expiry")
	}
}

func TestParseCookiesNetscapeAndJarConstruction(t *testing.T) {
	input := []byte("# Netscape HTTP Cookie File\n#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t0\tsessionid\tsynthetic\"session\n.instagram.com\tTRUE\t/\tTRUE\t0\trur\t\"synthetic\\054value\"\n")
	cookies, err := parseCookies(input)
	if err != nil || len(cookies) != 2 || !cookies[0].HttpOnly || cookies[1].Value != `synthetic\054value` {
		t.Fatal("Netscape cookie parsing changed")
	}
	jar, _ := cookiejar.New(nil)
	setCookiesInJar(jar, cookies)
	target, _ := url.Parse("https://www.instagram.com/")
	stored := jar.Cookies(target)
	found := false
	for _, c := range stored {
		if c.Name == "sessionid" {
			found = true
			if c.Value != "syntheticsession" {
				t.Fatal("legacy serialization sanitization changed")
			}
		}
	}
	if !found {
		t.Fatal("synthetic cookie not stored")
	}
}

func TestParseExpirationSecondsIntAndFloat(t *testing.T) {
	for _, s := range []string{"1801846439", "1801846439.938"} {
		raw := json.RawMessage(s)
		if got, ok := parseExpirationSeconds(&raw); !ok || got != 1801846439 {
			t.Fatal("expiry parsing changed")
		}
	}
	raw := json.RawMessage("null")
	if _, ok := parseExpirationSeconds(&raw); ok {
		t.Fatal("null expiry accepted")
	}
}
