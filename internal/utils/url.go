package utils

import (
    "net/url"
    "path"
    "strings"
)

func ExtFromURL(raw string) string {
    u, err := url.Parse(raw)
    if err != nil {
        return ""
    }
    ext := path.Ext(u.Path)
    if ext == "" {
        return ""
    }
    if len(ext) > 10 {
        return ""
    }
    ext = strings.ToLower(ext)
    return ext
}
