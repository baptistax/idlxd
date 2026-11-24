// Package validation contains developer-only smoke validation machinery. Normal
// application code does not enable or call live smoke tests.
package validation

import (
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/baptistax/idl/internal/fault"
)

// The guard is shared by API metadata and the one media request. It counts
// attempts, not successful responses, and holds the sequential slot until Close.
type requestGuard struct {
	mu                       sync.Mutex
	base                     http.RoundTripper
	allow                    func(*http.Request) bool
	limit, count, lastStatus int
	active                   bool
	stopped                  error
}

func (g *requestGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	switch {
	case req.Context().Err() != nil:
		g.mu.Unlock()
		return nil, req.Context().Err()
	case g.stopped != nil:
		err := g.stopped
		g.mu.Unlock()
		return nil, err
	case g.active:
		g.stopped = fault.New(fault.Permission, "smoke requests", "concurrent requests refused")
	case g.count >= g.limit:
		g.stopped = fault.New(fault.Budget, "smoke requests", "fixed request budget reached; no request sent")
	case !g.allow(req):
		g.stopped = fault.New(fault.Permission, "smoke requests", "request is outside the approved HTTPS host/method/credential scope")
	}
	if g.stopped != nil {
		err := g.stopped
		g.mu.Unlock()
		return nil, err
	}
	g.count++
	g.active = true
	g.mu.Unlock()
	resp, err := g.base.RoundTrip(req)
	if err == nil && (resp == nil || resp.Body == nil) {
		err = fault.New(fault.Schema, "smoke response", "transport returned no response body")
	}
	if err != nil {
		g.mu.Lock()
		g.active = false
		g.stopped = &fault.Error{Kind: fault.Network, Op: "smoke request", Detail: "transport failed; stopped without retry", Cause: err}
		g.mu.Unlock()
		return nil, err
	}
	g.mu.Lock()
	g.lastStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		kind := fault.Upstream
		switch resp.StatusCode {
		case 401:
			kind = fault.Authentication
		case 403:
			kind = fault.Permission
		case 429:
			kind = fault.RateLimited
		}
		g.stopped = fault.New(kind, "smoke request", "HTTP "+strconv.Itoa(resp.StatusCode)+"; stopped without retry")
		g.active = false
		g.mu.Unlock()
		// An HTTP rejection ends the smoke even if its body never finishes.
		resp.Body.Close()
		resp.Body = http.NoBody
		return resp, nil
	}
	g.mu.Unlock()
	resp.Body = &guardedBody{reader: &io.LimitedReader{R: resp.Body, N: maxPhotoBytes + 1}, body: resp.Body, release: func() { g.mu.Lock(); g.active = false; g.mu.Unlock() }}
	return resp, nil
}

type guardedBody struct {
	reader  *io.LimitedReader
	body    io.ReadCloser
	release func()
	once    sync.Once
}

func (b *guardedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if b.reader.N == 0 {
		return n, fault.New(fault.Schema, "smoke response", "response exceeds size limit")
	}
	return n, err
}

func (b *guardedBody) Close() error {
	err := b.body.Close()
	b.once.Do(b.release)
	return err
}

func (g *requestGuard) stats() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.count, g.lastStatus
}
