package auth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The jar needs both the export's source host and its host-only flag. Setting
// http.Cookie.Domain for a host-only record would also authorize subdomains.
type importedCookie struct {
	*http.Cookie
	hostOnly bool
}

func parseCookies(data []byte) ([]importedCookie, error) {
	trimmed := bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n")
	if len(trimmed) == 0 {
		return nil, errors.New("cookies.txt is empty")
	}

	if trimmed[0] == '[' || trimmed[0] == '{' {
		cookies, err := parseJSONCookies(trimmed)
		if err != nil {
			return nil, errors.New("invalid Cookie-Editor JSON")
		}
		return cookies, nil
	}

	return parseNetscapeCookies(trimmed)
}

func parseNetscapeCookies(data []byte) ([]importedCookie, error) {
	var cookies []importedCookie
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			continue
		}

		httpOnly := false
		if strings.HasPrefix(raw, "#HttpOnly_") {
			httpOnly = true
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "#HttpOnly_"))
		} else if strings.HasPrefix(raw, "#") {
			continue
		}

		parts := strings.Fields(raw)
		if len(parts) < 7 {
			continue
		}

		domain := strings.TrimSpace(parts[0])
		pathPart := strings.TrimSpace(parts[2])
		secureStr := strings.TrimSpace(parts[3])
		expiryStr := strings.TrimSpace(parts[4])
		name := strings.TrimSpace(parts[5])
		value := strings.TrimSpace(strings.Join(parts[6:], " "))

		if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = strings.TrimSuffix(strings.TrimPrefix(value, "\""), "\"")
		}

		secure := secureStr == "TRUE" || secureStr == "true" || secureStr == "1"

		var expires time.Time
		unix, err := strconv.ParseInt(expiryStr, 10, 64)
		if err != nil {
			continue
		}
		if unix != 0 {
			expires = time.Unix(unix, 0)
		}

		host := strings.TrimPrefix(domain, ".")
		if host == "" || name == "" {
			continue
		}

		if pathPart == "" {
			pathPart = "/"
		}

		cookies = append(cookies, importedCookie{Cookie: &http.Cookie{
			Name:     name,
			Value:    value,
			Path:     pathPart,
			Domain:   host,
			Secure:   secure,
			Expires:  expires,
			HttpOnly: httpOnly,
		}, hostOnly: !strings.EqualFold(parts[1], "TRUE") && parts[1] != "1"})
	}

	if err := s.Err(); err != nil {
		return nil, err
	}

	return cookies, nil
}

type jsonCookie struct {
	Name           string           `json:"name"`
	Value          string           `json:"value"`
	Domain         string           `json:"domain"`
	Path           string           `json:"path"`
	Secure         bool             `json:"secure"`
	HTTPOnly       bool             `json:"httpOnly"`
	HostOnly       *bool            `json:"hostOnly"`
	Session        bool             `json:"session"`
	ExpirationDate *json.RawMessage `json:"expirationDate"`
}

type jsonCookieContainer struct {
	Cookies []jsonCookie `json:"cookies"`
}

func parseJSONCookies(data []byte) ([]importedCookie, error) {
	var records []jsonCookie
	if data[0] == '[' {
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, err
		}
	} else {
		var container jsonCookieContainer
		if err := json.Unmarshal(data, &container); err == nil && len(container.Cookies) > 0 {
			records = container.Cookies
		} else {
			var single jsonCookie
			if err := json.Unmarshal(data, &single); err != nil {
				return nil, err
			}
			records = []jsonCookie{single}
		}
	}

	cookies := make([]importedCookie, 0, len(records))
	for _, rc := range records {
		host := strings.TrimPrefix(strings.TrimSpace(rc.Domain), ".")
		if host == "" || strings.TrimSpace(rc.Name) == "" {
			continue
		}
		pathPart := strings.TrimSpace(rc.Path)
		if pathPart == "" {
			pathPart = "/"
		}

		cookie := &http.Cookie{
			Name:     rc.Name,
			Value:    rc.Value,
			Path:     pathPart,
			Domain:   host,
			Secure:   rc.Secure,
			HttpOnly: rc.HTTPOnly,
		}

		if !rc.Session && rc.ExpirationDate != nil {
			exp, ok := parseExpirationSeconds(rc.ExpirationDate)
			if !ok {
				continue
			}
			cookie.Expires = time.Unix(exp, 0)
		}

		hostOnly := !strings.HasPrefix(strings.TrimSpace(rc.Domain), ".")
		if rc.HostOnly != nil {
			hostOnly = *rc.HostOnly
		}
		cookies = append(cookies, importedCookie{Cookie: cookie, hostOnly: hostOnly})
	}
	return cookies, nil
}

func parseExpirationSeconds(raw *json.RawMessage) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	trimmed := strings.TrimSpace(string(*raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false
	}

	var asInt int64
	if err := json.Unmarshal(*raw, &asInt); err == nil {
		if asInt > 0 {
			return asInt, true
		}
		return 0, false
	}

	var asFloat float64
	if err := json.Unmarshal(*raw, &asFloat); err == nil {
		if asFloat > 0 {
			if asFloat > math.MaxInt64 {
				return 0, false
			}
			return int64(asFloat), true
		}
	}

	return 0, false
}

func setCookiesInJar(jar http.CookieJar, cookies []importedCookie) {
	for _, cookie := range cookies {
		host := strings.TrimPrefix(cookie.Domain, ".")
		if host == "" {
			continue
		}
		// Copy and sanitize before storing in the cookie jar. This avoids net/http logging
		// about invalid bytes (e.g. '"') when building the Cookie header, while keeping the
		// effective request behavior identical (net/http would drop those bytes anyway).
		c := *cookie.Cookie
		c.Value = sanitizeCookieValueForRequest(c.Value)
		if cookie.hostOnly {
			c.Domain = ""
		}
		jar.SetCookies(&url.URL{Scheme: "https", Host: host, Path: "/"}, []*http.Cookie{&c})
	}
}

// sanitizeCookieValueForRequest removes bytes that are not valid cookie-octets (RFC 6265).
// Go's net/http will drop these bytes when serializing cookies and may emit a log line.
// Performing the sanitization at load time prevents noisy logs without changing what gets sent.
func sanitizeCookieValueForRequest(v string) string {
	if v == "" {
		return v
	}
	// Fast path: if the string is already valid ASCII cookie-octets, return as-is.
	out := make([]byte, 0, len(v))
	changed := false
	for i := 0; i < len(v); i++ {
		b := v[i]
		if isCookieOctet(b) {
			out = append(out, b)
			continue
		}
		changed = true
	}
	if !changed {
		return v
	}
	return string(out)
}

func isCookieOctet(b byte) bool {
	// Matches net/http's cookie-octet checks.
	return b == 0x21 || (b >= 0x23 && b <= 0x2B) || (b >= 0x2D && b <= 0x3A) || (b >= 0x3C && b <= 0x5B) || (b >= 0x5D && b <= 0x7E)
}
