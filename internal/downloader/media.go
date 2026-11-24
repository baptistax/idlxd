package downloader

import (
	"context"
	"encoding/binary"
	"image"
	"io"
	"os"

	"github.com/baptistax/idl/internal/fault"
)

func invalidFile(detail string) error { return fault.New(fault.Schema, "media file", detail) }

// DownloadMedia uses the same temporary-file transfer as DownloadToFile. Only
// publish the original bytes after validating the requested kind and format.
func (d *Downloader) DownloadMedia(ctx context.Context, rawURL, relPath string, video bool) (string, error) {
	return d.transfer(ctx, rawURL, relPath, mediaLimit(video), func(tmp, path string) (string, error) {
		ext, err := ValidateMediaFile(ctx, tmp, video)
		return replaceExt(path, ext), err
	})
}

func mediaLimit(video bool) int64 {
	if video {
		return 1 << 30
	}
	return 64 << 20
}

// ValidateMediaFile applies the same integrity and size checks to a transfer
// and to an existing IDL output before it is counted as present.
func ValidateMediaFile(ctx context.Context, path string, video bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > mediaLimit(video) {
		return "", invalidFile("invalid media file size or type")
	}
	if video {
		return ".mp4", validateMP4(ctx, f)
	}
	cfg, format, err := image.DecodeConfig(f)
	if err == image.ErrFormat {
		return "", invalidFile("unrecognized image format")
	}
	if err != nil {
		return "", invalidFile("invalid image header")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 50_000_000/cfg.Height {
		return "", invalidFile("invalid or oversized image dimensions")
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "webp": ".webp"}[format]
	if ext == "" {
		return "", invalidFile("unsupported image format")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, _, err := image.Decode(f); err != nil {
		return "", invalidFile("incomplete image")
	}
	return ext, ctx.Err()
}

// Verify the ISO BMFF box extents, file type, a video track and nonempty
// media data. This is container integrity validation, not codec decoding.
func validateMP4(ctx context.Context, f *os.File) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var fileType, video, data bool
	boxes := 0
	var walk func(start, end int64, parent string) error
	walk = func(start, end int64, parent string) error {
		for pos := start; pos < end; {
			if err := ctx.Err(); err != nil {
				return err
			}
			var h [16]byte
			if end-pos < 8 {
				return invalidFile("truncated video box")
			}
			if _, err := f.ReadAt(h[:8], pos); err != nil {
				return invalidFile("truncated video header")
			}
			boxes++
			if boxes > 100000 {
				return invalidFile("too many video boxes")
			}
			size := int64(binary.BigEndian.Uint32(h[:4]))
			head := int64(8)
			switch size {
			case 0:
				size = end - pos
			case 1:
				if _, err := f.ReadAt(h[8:], pos+8); err != nil {
					return invalidFile("truncated video size")
				}
				size = int64(binary.BigEndian.Uint64(h[8:]))
				head = 16
			}
			if size < head || size > end-pos {
				return invalidFile("invalid video box length")
			}
			payload, next := pos+head, pos+size
			switch parent + "/" + string(h[4:8]) {
			case "/ftyp":
				fileType = next-payload >= 8
			case "/mdat":
				data = data || next > payload
			case "/moov", "moov/trak", "trak/mdia":
				if err := walk(payload, next, string(h[4:8])); err != nil {
					return err
				}
			case "mdia/hdlr":
				if next-payload >= 12 {
					var handler [4]byte
					if _, err := f.ReadAt(handler[:], payload+8); err != nil {
						return invalidFile("truncated video track")
					}
					video = video || string(handler[:]) == "vide"
				}
			}
			pos = next
		}
		return nil
	}
	if err := walk(0, st.Size(), ""); err != nil {
		return err
	}
	if !fileType || !video || !data {
		return invalidFile("incomplete or non-video MP4")
	}
	return nil
}
