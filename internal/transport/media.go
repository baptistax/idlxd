package transport

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func MediaHost(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".cdninstagram.com") || strings.HasSuffix(host, ".fbcdn.net")
}

type mediaTransport struct{ base http.RoundTripper }

func (t mediaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !MediaHost(r.URL) || r.Method != http.MethodGet || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-CSRFToken") != "" {
		return nil, fault.New(fault.Permission, "download", "outside media credential scope")
	}
	return t.base.RoundTrip(r)
}

func MediaClient(base *http.Client) *http.Client {
	hc := http.Client{Timeout: 60 * time.Second}
	if base != nil {
		hc = *base
	}
	if hc.Transport == nil {
		hc.Transport = http.DefaultTransport
	}
	if hc.Timeout <= 0 {
		hc.Timeout = 60 * time.Second
	}
	hc.Transport = mediaTransport{hc.Transport}
	hc.Jar = nil
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}
