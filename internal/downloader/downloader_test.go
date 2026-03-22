package downloader

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadToFileOverwritesDestinationAndLeavesNoTmp(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "fresh-content")
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := New(Options{
		OutputDir: dir,
		Timeout:   5 * time.Second,
	})

	relPath := filepath.Join("raissaturno", "posts", "clip.mp4")
	outPath := filepath.Join(dir, relPath)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(outPath, []byte("old-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := dl.DownloadToFile(context.Background(), srv.URL, relPath)
	if err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	if got != outPath {
		t.Fatalf("unexpected output path: got %q want %q", got, outPath)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "fresh-content" {
		t.Fatalf("unexpected file contents: got %q", string(data))
	}

	if _, err := os.Stat(outPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file should not remain, got err=%v", err)
	}
}
