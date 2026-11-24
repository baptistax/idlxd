package downloader

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
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

func TestImageConversionAndFallback(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, header, output string
		body                 []byte
	}{
		{"convert PNG by signature", "application/octet-stream", "photo.jpg", encoded.Bytes()},
		{"preserve undecodable WebP", "image/webp", "photo.webp", []byte("synthetic content")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.header)
				w.Write(tt.body)
			}))
			defer srv.Close()
			dir := t.TempDir()
			dl := New(Options{OutputDir: dir})
			path, err := dl.DownloadImageAsJPEG(context.Background(), srv.URL, "photo.jpg")
			if err != nil || path != filepath.Join(dir, tt.output) {
				t.Fatalf("saved to %q: %v", path, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.output == "photo.jpg" {
				img, err := jpeg.Decode(bytes.NewReader(got))
				if err != nil || img.Bounds().Size() != image.Pt(2, 3) {
					t.Fatal("expected a complete 2x3 JPEG", err)
				}
			} else if !bytes.Equal(got, tt.body) {
				t.Fatal("original image was changed")
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatal("unexpected temporary files", files, err)
			}
		})
	}
}

func TestFailedReplacementPreservesDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "synthetic media")
	}))
	defer srv.Close()
	dir := t.TempDir()
	destination := filepath.Join(dir, "taken")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	dl := New(Options{OutputDir: dir})
	if _, err := dl.DownloadToFile(context.Background(), srv.URL, "taken"); err == nil {
		t.Fatal("unexpected replacement of a directory")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "taken" || !files[0].IsDir() {
		t.Fatal("failed transfer changed the destination or left a temporary file", files, err)
	}
}

func TestTypedMediaTransfer(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		video  bool
		header string
		body   []byte
		ok     bool
	}{
		{"original PNG", false, "application/octet-stream", pngData.Bytes(), true},
		{"poster advertised as video", true, "video/mp4", pngData.Bytes(), false},
		{"truncated PNG", false, "image/png", pngData.Bytes()[:40], false},
		{"false JPEG header", false, "image/jpeg", []byte("not an image"), false},
		{"MP4 header without track", true, "video/mp4", []byte("\x00\x00\x00\x10ftypisom0000"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.header)
				w.Write(tt.body)
			}))
			defer srv.Close()
			dir := t.TempDir()
			got, err := New(Options{OutputDir: dir}).DownloadMedia(context.Background(), srv.URL, "selected.jpg", tt.video)
			if (err == nil) != tt.ok {
				t.Fatal("unexpected validation result", err)
			}
			files, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			if !tt.ok {
				if len(files) != 0 {
					t.Fatal("invalid file published")
				}
				return
			}
			body, e := os.ReadFile(got)
			if e != nil || filepath.Ext(got) != ".png" || !bytes.Equal(body, tt.body) || len(files) != 1 {
				t.Fatal("original bytes or format lost")
			}
		})
	}
}

func TestInterruptedMediaReplacementKeepsOriginal(t *testing.T) {
	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	// Flush a short body, then terminate without sending the declared length.
	started, finish := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "512")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		close(started)
		<-finish
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := New(Options{OutputDir: dir}).DownloadMedia(t.Context(), srv.URL, "photo.png", false)
		done <- err
	}()
	<-started
	during, readErr := os.ReadFile(path)
	close(finish)
	if err := <-done; err == nil {
		t.Fatal("short response succeeded")
	}
	after, err := os.ReadFile(path)
	if readErr != nil || err != nil || !bytes.Equal(during, original.Bytes()) || !bytes.Equal(after, original.Bytes()) {
		t.Fatal("original was changed during or after interrupted transfer", readErr, err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 1 {
		t.Fatal("temporary file left after interruption", files, err)
	}
}
