package auth

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

const SessionOrigin = "https://www.instagram.com"

var (
	ErrMissing    = errors.New("cookies.txt not found")
	ErrInvalid    = errors.New("invalid cookie file")
	ErrExpired    = errors.New("session cookie expired")
	ErrUnreadable = errors.New("cookie file unreadable")
)

func sessionError(reason error) error {
	return &fault.Error{Kind: fault.Authentication, Op: "cookies", Detail: reason.Error(), Cause: reason}
}

// LoadSessionJar returns the same scoped jar that passed local validation.
func LoadSessionJar(path string) (http.CookieJar, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sessionError(ErrMissing)
		}
		return nil, sessionError(ErrUnreadable)
	}
	cookies, err := parseCookies(data)
	if err != nil || len(cookies) == 0 {
		return nil, sessionError(ErrInvalid)
	}
	jar, _ := cookiejar.New(nil)
	expired := false
	for _, cookie := range cookies {
		host := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		if host != "instagram.com" && !strings.HasSuffix(host, ".instagram.com") {
			continue
		}
		if cookie.Name == "sessionid" && sanitizeCookieValueForRequest(cookie.Value) != cookie.Value {
			return nil, sessionError(ErrInvalid)
		}
		if cookie.Name == "sessionid" && cookie.Value != "" && cookie.Path == "/" &&
			(host == "instagram.com" && !cookie.hostOnly || host == "www.instagram.com") &&
			!cookie.Expires.IsZero() && !cookie.Expires.After(time.Now()) {
			expired = true
		}
		setCookiesInJar(jar, []importedCookie{cookie})
	}
	u, _ := url.Parse(SessionOrigin + "/")
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "sessionid" && cookie.Value != "" {
			return jar, nil
		}
	}
	if expired {
		return nil, sessionError(ErrExpired)
	}
	return nil, sessionError(ErrInvalid)
}
