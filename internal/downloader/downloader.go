package downloader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/transport"
	"github.com/baptistax/idl/internal/utils"
	xwebp "golang.org/x/image/webp"
)

type Options struct {
	OutputDir  string
	Timeout    time.Duration
	UserAgent  string
	Referer    string
	HTTPClient *http.Client
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
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: opts.Timeout}
	}
	if hc.CheckRedirect == nil {
		client := *hc
		client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 || fault.Global(transport.StatusError(next.Response, "download")) {
				return http.ErrUseLastResponse
			}
			return nil
		}
		hc = &client
	}
	return &Downloader{
		outputDir:  opts.OutputDir,
		httpClient: hc,
		userAgent:  opts.UserAgent,
		referer:    opts.Referer,
	}
}

func (d *Downloader) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &fault.Error{Kind: fault.InvalidInput, Op: "download", Detail: "invalid media URL", Cause: err}
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if d.referer != "" {
		req.Header.Set("Referer", d.referer)
	}
	req.Header.Set("Accept", accept)
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, &fault.Error{Kind: fault.Network, Op: "download", Detail: "transport failed", Cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, transport.StatusError(resp, "download")
	}
	return resp, nil
}

// Write beside the destination so only a completed transfer is renamed into place.
func saveTemp(outPath string, body io.Reader) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(outPath), ".idl-*.tmp")
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(f, body)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (d *Downloader) DownloadToFile(ctx context.Context, url, relPath string) (string, error) {
	return d.transfer(ctx, url, relPath, 0, nil)
}

func (d *Downloader) transfer(ctx context.Context, url, relPath string, limit int64, validate func(string, string) (string, error)) (string, error) {
	outPath := filepath.Join(d.outputDir, relPath)
	if err := utils.EnsureDir(filepath.Dir(outPath)); err != nil {
		return "", err
	}
	resp, err := d.get(ctx, url, "*/*")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if ct := normalizeContentType(resp.Header.Get("Content-Type")); ct == "text/html" || ct == "application/json" {
		return "", fault.New(fault.Schema, "download", "received a document instead of media")
	}
	var body io.Reader = resp.Body
	if limit > 0 {
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Range") != "" || resp.ContentLength > limit {
			return "", invalidFile("partial or oversized media file")
		}
		body = io.LimitReader(resp.Body, limit+1)
	}
	tmp, err := saveTemp(outPath, body)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	if limit > 0 {
		st, err := os.Stat(tmp)
		if err != nil {
			return "", err
		}
		if st.Size() == 0 || st.Size() > limit || resp.ContentLength >= 0 && st.Size() != resp.ContentLength {
			return "", invalidFile("empty, truncated or oversized transfer")
		}
	}
	if validate != nil {
		outPath, err = validate(tmp, outPath)
		if err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

// DownloadImageAsJPEG converts PNG and WebP when possible. If conversion fails,
// it keeps the original format and returns the path actually written.
func (d *Downloader) DownloadImageAsJPEG(ctx context.Context, url, relPath string) (string, error) {
	outPath := filepath.Join(d.outputDir, relPath)
	if err := utils.EnsureDir(filepath.Dir(outPath)); err != nil {
		return "", err
	}
	resp, err := d.get(ctx, url, "image/jpeg,image/png,*/*;q=0.8")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	prefix, err := reader.Peek(512)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	contentType := normalizeContentType(resp.Header.Get("Content-Type"))
	sniffed := normalizeContentType(http.DetectContentType(prefix))
	switch sniffed {
	case "image/jpeg", "image/png", "image/webp":
		contentType = sniffed
	default:
		if contentType == "" {
			contentType = sniffed
		}
	}
	if contentType == "text/html" || contentType == "application/json" {
		return "", fault.New(fault.Schema, "download", "received a document instead of an image")
	}
	tmp, err := saveTemp(outPath, reader)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	switch contentType {
	case "image/jpeg", "image/jpg":
	case "image/png", "image/webp":
		converted := tmp + ".jpg"
		defer os.Remove(converted)
		if err := convertImageFileToJPEG(tmp, converted, contentType); err == nil {
			tmp = converted
		} else {
			outPath = replaceExt(outPath, extFromContentType(contentType))
		}
	default:
		outPath = replaceExt(outPath, extFromContentType(contentType))
	}
	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

func normalizeContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return ""
	}
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

func extFromContentType(ct string) string {
	switch ct {
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	default:
		return ".bin"
	}
}

func replaceExt(path, newExt string) string {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	return base + newExt
}

func convertImageFileToJPEG(inPath, outPath, contentType string) error {
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	var img image.Image
	switch contentType {
	case "image/webp":
		img, err = xwebp.Decode(in)
	case "image/png":
		img, err = png.Decode(in)
	default:
		return fmt.Errorf("unsupported conversion from %q", contentType)
	}
	if err != nil {
		return err
	}

	opaque := flattenToOpaque(img)

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	err = jpeg.Encode(out, opaque, &jpeg.Options{Quality: 95})
	return errors.Join(err, out.Close())
}

func flattenToOpaque(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}
