package downloader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/fault"
)

func TestRejectedDownloadDoesNotWriteMedia(t *testing.T) {
	for _, tt := range []struct {
		status  int
		content string
		kind    fault.Kind
	}{
		{401, "text/html", fault.Authentication}, {403, "text/html", fault.Permission},
		{429, "text/html", fault.RateLimited}, {404, "text/plain", fault.Unsupported},
		{200, "text/html", fault.Schema}, {200, "application/json", fault.Schema},
	} {
		t.Run(string(tt.kind)+"/"+tt.content, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.content)
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("synthetic-secret"))
			}))
			defer srv.Close()
			dir := t.TempDir()
			dl := New(Options{OutputDir: dir})
			for _, image := range []bool{false, true} {
				var err error
				if image {
					_, err = dl.DownloadImageAsJPEG(context.Background(), srv.URL, "file.jpg")
				} else {
					_, err = dl.DownloadToFile(context.Background(), srv.URL, "file.mp4")
				}
				if !fault.Is(err, tt.kind) || strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal(err)
				}
				if tt.kind == fault.RateLimited {
					var e *fault.Error
					if !errors.As(err, &e) || e.RetryAfter != 30*time.Second {
						t.Fatal(err)
					}
				}
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("unexpected files=%v err=%v", files, err)
			}
		})
	}
}

func TestMalformedDownloadURLDoesNotLeakQuery(t *testing.T) {
	dl := New(Options{OutputDir: t.TempDir()})
	_, err := dl.DownloadToFile(context.Background(), "https://media.invalid/%zz?token=synthetic-secret", filepath.Join("owner", "video.mp4"))
	if !fault.Is(err, fault.InvalidInput) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal(err)
	}
}

func TestMediaLoginRedirectStopsAtResponse(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/accounts/login/", http.StatusFound)
	}))
	defer srv.Close()
	dl := New(Options{OutputDir: t.TempDir()})
	_, err := dl.DownloadMedia(t.Context(), srv.URL, "file.jpg", false)
	if !fault.Is(err, fault.Authentication) || requests != 1 {
		t.Fatalf("login redirect: %d requests, %v", requests, err)
	}
}
