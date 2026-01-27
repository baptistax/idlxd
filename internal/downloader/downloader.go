package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/baptistax/idl/internal/utils"
)

type Options struct {
	OutputDir string
	Timeout   time.Duration
	UserAgent string
	Referer   string
}

type Downloader struct {
	outputDir  string
	httpClient *http.Client
	userAgent  string
	referer    string
}

func New(opts Options) *Downloader {
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "./out"
	}
	if opts.Referer == "" {
		opts.Referer = "https://www.instagram.com/"
	}
	return &Downloader{
		outputDir: opts.OutputDir,
		httpClient: &http.Client{
			Timeout: opts.Timeout,
		},
		userAgent: opts.UserAgent,
		referer:   opts.Referer,
	}
}

func (d *Downloader) DownloadToFile(ctx context.Context, url, relPath string) (string, error) {
	if err := utils.EnsureDir(filepath.Dir(filepath.Join(d.outputDir, relPath))); err != nil {
		return "", err
	}

	outPath := filepath.Join(d.outputDir, relPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if d.referer != "" {
		req.Header.Set("Referer", d.referer)
	}
	req.Header.Set("Accept", "*/*")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("status inesperado: %s", resp.Status)
	}

	tmpPath := outPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", err
	}

	return outPath, nil
}
