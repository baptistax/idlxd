package instagram

import (
	"encoding/json"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestParseCookies_JSONCookieEditor(t *testing.T) {
	input := []byte("\ufeff \n\t[\n  {\n    \"name\": \"sessionid\",\n    \"value\": \"123%3AabcDEF%3A10%3AAYj4XN8cLpcG9o0QkD8aijCTalWfV\\\\\\\"lPNF\",\n    \"domain\": \".instagram.com\",\n    \"hostOnly\": false,\n    \"path\": \"/\",\n    \"secure\": true,\n    \"httpOnly\": true,\n    \"sameSite\": null,\n    \"session\": false,\n    \"firstPartyDomain\": \"\",\n    \"partitionKey\": null,\n    \"expirationDate\": 1801846439.938,\n    \"storeId\": null\n  },\n  {\n    \"name\": \"rur\",\n    \"value\": \"\\\"RVA\\\\\\\\054617073445\\\\\\\\0541801846466:01fe8774f3e1c5f4\\\"\",\n    \"domain\": \".instagram.com\",\n    \"hostOnly\": false,\n    \"path\": \"/\",\n    \"secure\": true,\n    \"httpOnly\": true,\n    \"sameSite\": \"lax\",\n    \"session\": true,\n    \"firstPartyDomain\": \"\",\n    \"partitionKey\": null,\n    \"storeId\": null\n  }\n]")

	cookies, err := parseCookies(input)
	if err != nil {
		t.Fatalf("parseCookies returned error: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}

	if cookies[0].Name != "sessionid" || cookies[0].Domain != "instagram.com" || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("unexpected first cookie: %+v", cookies[0])
	}
	if got := cookies[0].Expires.Unix(); got != 1801846439 {
		t.Fatalf("expected expiration 1801846439, got %d", got)
	}
	if cookies[1].Name != "rur" {
		t.Fatalf("unexpected second cookie name: %s", cookies[1].Name)
	}
	if !cookies[1].Expires.IsZero() {
		t.Fatalf("expected session cookie to have zero Expires, got %v", cookies[1].Expires)
	}
}

func TestParseCookies_NetscapeAndJarConstruction(t *testing.T) {
	input := []byte("# Netscape HTTP Cookie File\n#HttpOnly_.instagram.com	TRUE	/	TRUE	1801846439	sessionid	abc\"def\n.instagram.com	TRUE	/	TRUE	0	rur	\"RVA\\054617073445\\0541801846466:01fe8774f3e1c5f4\"\n")

	cookies, err := parseCookies(input)
	if err != nil {
		t.Fatalf("parseCookies returned error: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}
	if !cookies[0].HttpOnly {
		t.Fatalf("expected first cookie to be HttpOnly")
	}
	if cookies[1].Value != `RVA\054617073445\0541801846466:01fe8774f3e1c5f4` {
		t.Fatalf("unexpected second cookie value: %q", cookies[1].Value)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New error: %v", err)
	}
	setCookiesInJar(jar, cookies)

	target, _ := url.Parse("https://www.instagram.com/")
	stored := jar.Cookies(target)
	if len(stored) < 2 {
		t.Fatalf("expected cookies in jar, got %d", len(stored))
	}

	foundSession := false
	for _, c := range stored {
		if c.Name == "sessionid" {
			foundSession = true
			// Cookie values are sanitized when loaded into the jar to avoid noisy net/http logs.
			// net/http would drop invalid bytes (like '"') when serializing the Cookie header.
			if c.Value != `abcdef` {
				t.Fatalf("unexpected sessionid value: %q", c.Value)
			}
		}
	}
	if !foundSession {
		t.Fatalf("sessionid cookie was not loaded into jar")
	}
}

func TestParseExpirationSeconds_IntAndFloat(t *testing.T) {
	intRaw := json.RawMessage("1801846439")
	fRaw := json.RawMessage("1801846439.938")

	if got, ok := parseExpirationSeconds(&intRaw); !ok || got != 1801846439 {
		t.Fatalf("expected int expiration to parse, got (%d, %v)", got, ok)
	}
	if got, ok := parseExpirationSeconds(&fRaw); !ok || got != 1801846439 {
		t.Fatalf("expected float expiration to parse, got (%d, %v)", got, ok)
	}

	nullRaw := json.RawMessage("null")
	if got, ok := parseExpirationSeconds(&nullRaw); ok || got != 0 {
		t.Fatalf("expected null expiration to fail, got (%d, %v)", got, ok)
	}
}
