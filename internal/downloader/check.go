package downloader

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// CheckEngine exercises the regular transfer and final rename with a file-only transport.
func CheckEngine(ctx context.Context) (err error) {
	dir, err := os.MkdirTemp("", "idl-status-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	const sample = "IDL local transfer check"
	if err := os.WriteFile(filepath.Join(dir, "source"), []byte(sample), 0600); err != nil {
		return err
	}
	engine := New(Options{OutputDir: dir, HTTPClient: &http.Client{Transport: http.NewFileTransport(http.Dir(dir))}})
	path, err := engine.DownloadToFile(ctx, "file:///source", "copy")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err == nil && string(data) != sample {
		return errors.New("local transfer mismatch")
	}
	return err
}
