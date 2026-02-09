package downloader

import (
	"bytes"
	"context"
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

	"github.com/baptistax/idl/internal/utils"
	xwebp "golang.org/x/image/webp"
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

// DownloadImageAsJPEG downloads an image and ensures the output is a JPEG file.
// If the response is a PNG or WebP, it is converted to JPEG (quality 95) and saved at relPath.
// If conversion fails, no output file is created and an error is returned (callers may retry with another URL).
// relPath is expected to end with ".jpg" or ".jpeg".
func (d *Downloader) DownloadImageAsJPEG(ctx context.Context, url, relPath string) (string, error) {
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
	// Prefer JPEG/PNG. Do not advertise WebP to reduce the chance of getting WebP from the CDN.
	req.Header.Set("Accept", "image/jpeg,image/png,*/*;q=0.8")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	tmpDownloadPath := outPath + ".tmp.download"
	f, err := os.Create(tmpDownloadPath)
	if err != nil {
		return "", err
	}

	// Capture the first bytes for content sniffing.
	sniff := make([]byte, 512)
	n, readErr := io.ReadFull(resp.Body, sniff)
	if readErr != nil && !errorsIsEOF(readErr) {
		_ = f.Close()
		_ = os.Remove(tmpDownloadPath)
		return "", readErr
	}
	sniff = sniff[:n]
	reader := io.MultiReader(bytes.NewReader(sniff), resp.Body)
	if _, err := io.Copy(f, reader); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpDownloadPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpDownloadPath)
		return "", err
	}

	headerType := normalizeContentType(resp.Header.Get("Content-Type"))
	sniffType := strings.ToLower(strings.TrimSpace(http.DetectContentType(sniff)))
	contentType := headerType
	// Prefer sniffed type when it is a known image subtype. This avoids conversion failures
	// when servers mislabel Content-Type.
	if sniffType == "image/jpeg" || sniffType == "image/jpg" || sniffType == "image/png" || sniffType == "image/webp" {
		contentType = sniffType
	}
	if contentType == "" {
		contentType = sniffType
	}

	switch contentType {
	case "image/jpeg", "image/jpg":
		if err := renameReplace(tmpDownloadPath, outPath); err != nil {
			return "", err
		}
		return outPath, nil
	case "image/png":
		jpegTmp := outPath + ".tmp.jpg"
		if err := convertImageFileToJPEG(tmpDownloadPath, jpegTmp, contentType); err != nil {
			// Fallback: keep the original PNG if conversion fails.
			_ = os.Remove(jpegTmp)
			fallback := replaceExt(outPath, ".png")
			if rerr := renameReplace(tmpDownloadPath, fallback); rerr != nil {
				_ = os.Remove(tmpDownloadPath)
				return "", fmt.Errorf("failed to convert png to jpeg: %v (and failed to preserve original: %v)", err, rerr)
			}
			return fallback, nil
		}
		_ = os.Remove(tmpDownloadPath)
		if err := renameReplace(jpegTmp, outPath); err != nil {
			_ = os.Remove(jpegTmp)
			return "", err
		}
		return outPath, nil
	case "image/webp":
		jpegTmp := outPath + ".tmp.jpg"
		if err := convertImageFileToJPEG(tmpDownloadPath, jpegTmp, contentType); err != nil {
			// Fallback: keep the original WebP if conversion fails.
			_ = os.Remove(jpegTmp)
			fallback := replaceExt(outPath, ".webp")
			if rerr := renameReplace(tmpDownloadPath, fallback); rerr != nil {
				_ = os.Remove(tmpDownloadPath)
				return "", fmt.Errorf("failed to convert webp to jpeg: %v (and failed to preserve original: %v)", err, rerr)
			}
			return fallback, nil
		}
		_ = os.Remove(tmpDownloadPath)
		if err := renameReplace(jpegTmp, outPath); err != nil {
			_ = os.Remove(jpegTmp)
			return "", err
		}
		return outPath, nil
	default:
		// Preserve unknown payloads.
		fallback := replaceExt(outPath, extFromContentType(contentType))
		if rerr := renameReplace(tmpDownloadPath, fallback); rerr != nil {
			_ = os.Remove(tmpDownloadPath)
			return "", fmt.Errorf("unsupported image content-type %q", contentType)
		}
		return fallback, nil
	}
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

func renameReplace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// On Windows, Rename fails if dst exists.
	_ = os.Remove(dst)
	return os.Rename(src, dst)
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
	defer out.Close()

	if err := jpeg.Encode(out, opaque, &jpeg.Options{Quality: 95}); err != nil {
		return err
	}
	return out.Close()
}

func flattenToOpaque(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

func errorsIsEOF(err error) bool {
	// io.ReadFull returns io.EOF or io.ErrUnexpectedEOF when the stream is smaller than the buffer.
	return err == io.EOF || err == io.ErrUnexpectedEOF
}
