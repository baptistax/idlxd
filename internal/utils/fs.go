package utils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func EnsureDir(path string) error {
	if path == "" {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

var invalidPathChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func SanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	s = invalidPathChars.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "unknown"
	}
	return s
}

func JoinClean(elem ...string) string {
	return filepath.Clean(filepath.Join(elem...))
}
