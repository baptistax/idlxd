package config

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	DefaultCookiesPath = "cookies.txt"
	DefaultOutputRoot  = "out"
	DefaultUserAgent   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

type Config struct {
	Username    string
	CookiesPath string
	OutputRoot  string
	UserAgent   string
}

func ParseArgs(args []string) (Config, error) {
	if len(args) != 1 {
		return Config{}, errors.New("usage: idl <username>")
	}
	username := strings.TrimSpace(args[0])
	if username == "" {
		return Config{}, errors.New("usage: idl <username>")
	}

	return Config{
		Username:    username,
		CookiesPath: filepath.Clean(DefaultCookiesPath),
		OutputRoot:  filepath.Clean(DefaultOutputRoot),
		UserAgent:   DefaultUserAgent,
	}, nil
}
