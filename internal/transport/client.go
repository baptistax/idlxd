// Package transport sends bounded metadata requests to one provider origin.
package transport

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

const MaxBody = 16 << 20

type Client struct {
	origin *url.URL
	http   *http.Client
}

// New preserves an injected transport and its timeout. Only the explicitly
// supplied jar belongs to this credential scope.
func New(origin string, base *http.Client, jar http.CookieJar, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fault.New(fault.InvalidInput, "transport", "invalid HTTPS origin")
	}
	var hc http.Client
	if base != nil {
		hc = *base
	}
	if hc.Timeout <= 0 {
		hc.Timeout = timeout
		if hc.Timeout <= 0 {
			hc.Timeout = 30 * time.Second
		}
	}
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{origin: u, http: &hc}, nil
}

// Exchange keeps the response open for the callback and closes it on return.
// An operation can reject a status immediately or inspect a bounded body.
func (c *Client) Exchange(req *http.Request, handle func(Response) error) error {
	if err := c.checkRequest(req); err != nil {
		if req != nil && req.Body != nil {
			req.Body.Close()
		}
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return networkError(req.Context(), "request", err)
	}
	defer resp.Body.Close()
	if err := handle(Response{http: resp, ctx: req.Context()}); err != nil {
		return err
	}
	return req.Context().Err()
}

func (c *Client) checkRequest(req *http.Request) error {
	if req == nil || req.URL == nil || req.URL.User != nil || req.URL.Opaque != "" ||
		req.URL.Scheme != c.origin.Scheme || !strings.EqualFold(req.URL.Host, c.origin.Host) ||
		req.Host != "" && !strings.EqualFold(req.Host, c.origin.Host) {
		return fault.New(fault.Permission, "request", "outside provider origin")
	}
	return req.Context().Err()
}

type Response struct {
	http *http.Response
	ctx  context.Context
}

func (r Response) StatusCode() int { return r.http.StatusCode }

func (r Response) ReadBody() ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if r.http.ContentLength > MaxBody {
		return nil, fault.New(fault.Schema, "response", "body exceeds supported size")
	}
	body, err := io.ReadAll(io.LimitReader(r.http.Body, MaxBody+1))
	if err != nil {
		return nil, networkError(r.ctx, "response", err)
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) > MaxBody {
		return nil, fault.New(fault.Schema, "response", "body exceeds supported size")
	}
	return body, nil
}

func networkError(ctx context.Context, op string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &fault.Error{Kind: fault.Network, Op: op, Detail: "transport failed", Cause: err}
}

func (r Response) StatusError() error {
	return StatusError(r.http, "request")
}

// StatusError reports only the status code. A redirect to a login or security
// checkpoint must stop both metadata and CDN requests without following it.
func StatusError(resp *http.Response, op string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	kind := fault.Upstream
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
		if dest, err := resp.Location(); err == nil {
			switch {
			case strings.HasPrefix(dest.Path, "/accounts/login"):
				kind = fault.Authentication
			case strings.HasPrefix(dest.Path, "/challenge"), strings.HasPrefix(dest.Path, "/checkpoint"):
				kind = fault.Verification
			}
		}
	case http.StatusUnauthorized:
		kind = fault.Authentication
	case http.StatusForbidden:
		kind = fault.Permission
	case http.StatusTooManyRequests:
		kind = fault.RateLimited
	case http.StatusNotFound, http.StatusGone:
		kind = fault.Unsupported
	}
	e := &fault.Error{Kind: kind, Op: op, Detail: "HTTP " + strconv.Itoa(resp.StatusCode), StatusCode: resp.StatusCode}
	if kind == fault.RateLimited {
		e.RetryAfter = RetryAfter(resp.Header.Get("Retry-After"))
	}
	return e
}

func (r Response) RetryAfter() time.Duration {
	return RetryAfter(r.http.Header.Get("Retry-After"))
}

// RetryAfter parses either delay seconds or an HTTP date.
func RetryAfter(raw string) time.Duration {
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}
