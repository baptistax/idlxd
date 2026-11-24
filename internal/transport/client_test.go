package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func TestMetadataBodyLimit(t *testing.T) {
	block := make([]byte, 32<<10)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/declared-overflow" {
			w.Header().Set("Content-Length", strconv.Itoa(MaxBody+1))
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		for range MaxBody / len(block) {
			if _, err := w.Write(block); err != nil {
				return
			}
		}
		if r.URL.Path == "/overflow" {
			w.Write([]byte{0})
		}
	}))
	defer server.Close()
	c, err := New(server.URL, server.Client(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, path := range []string{"/limit", "/overflow", "/declared-overflow"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = c.Exchange(req, func(r Response) error {
			body, err := r.ReadBody()
			if err == nil && len(body) != MaxBody || err != nil && body != nil {
				t.Error("unexpected buffered length")
			}
			return err
		})
		if path == "/limit" && err != nil || path != "/limit" && !fault.Is(err, fault.Schema) {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestRedirectStopsOnSessionRejection(t *testing.T) {
	for _, tc := range []struct {
		location string
		kind     fault.Kind
	}{
		{"/accounts/login/?next=private", fault.Authentication},
		{"https://www.instagram.com/challenge/step/", fault.Verification},
		{"/checkpoint/", fault.Verification},
	} {
		req, _ := http.NewRequest(http.MethodGet, "https://www.instagram.com/source", nil)
		resp := &http.Response{Request: req, StatusCode: 302, Header: http.Header{"Location": {tc.location}}}
		for _, op := range []string{"request", "download"} {
			err := StatusError(resp, op)
			if !fault.Is(err, tc.kind) || !fault.Global(err) {
				t.Fatal("redirect failed to stop session", err)
			}
		}
	}
}

func TestClientTimeoutCoversBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c, err := New(server.URL, server.Client(), nil, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Exchange(req, func(r Response) error { _, err := r.ReadBody(); return err })
	if !errors.Is(err, context.DeadlineExceeded) || !fault.Is(err, fault.Network) {
		t.Fatal("expected transport timeout", err)
	}
}
