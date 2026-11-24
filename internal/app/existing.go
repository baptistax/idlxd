package app

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/fault"
)

// Key legacy output by the media PK. Collection, carousel position, and
// capture time can differ between listings of the same item.
var outputName = regexp.MustCompile(`^(?:[0-9]{8}_[0-9]{6}|unknown)_([0-9]+)(?:_[0-9]+)?\.(?:jpg|png|webp|mp4)$`)

type existingMedia map[string][]string

func indexExisting(ctx context.Context, root string) (existingMedia, error) {
	index := existingMedia{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && filepath.Dir(path) == root {
			switch entry.Name() {
			case "posts", "reels", "stories", "highlights":
			default:
				return filepath.SkipDir
			}
		}
		if entry.Type().IsRegular() {
			if match := outputName.FindStringSubmatch(entry.Name()); match != nil && filepath.Dir(path) != root {
				index[match[1]] = append(index[match[1]], path)
			}
		}
		return nil
	})
	return index, err
}

func (index existingMedia) valid(ctx context.Context, id string, video bool) (bool, error) {
	for _, path := range index[id] {
		if strings.HasSuffix(path, ".mp4") != video {
			continue
		}
		ext, err := downloader.ValidateMediaFile(ctx, path, video)
		if err == nil && ext == filepath.Ext(path) {
			return true, nil
		}
		if err != nil && !fault.Is(err, fault.Schema) && !errors.Is(err, fs.ErrNotExist) {
			return false, &fault.Error{Kind: fault.Storage, Op: "existing file", Cause: err}
		}
	}
	return false, nil
}
