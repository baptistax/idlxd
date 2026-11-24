package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

const (
	DefaultOutputRoot  = "out"
	DefaultUserAgent   = "IDL/2.0"
	DefaultCookiesPath = "cookies.txt"
	CookiesProvider    = "cookies"
	GraphProvider      = "graph"
)

type Config struct {
	Status      bool
	Username    string
	OutputRoot  string
	UserAgent   string
	AccessToken string
	AccountID   string
	Provider    string
	CookiesPath string
}

func ParseArgs(args []string) (Config, error) {
	if len(args) != 1 {
		return Config{}, fault.New(fault.InvalidInput, "arguments", "expected a username or status")
	}
	provider, err := ProviderName(os.Getenv("IDL_PROVIDER"))
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Status:     args[0] == "status",
		OutputRoot: filepath.Clean(DefaultOutputRoot), UserAgent: DefaultUserAgent,
		Provider: provider, CookiesPath: filepath.Clean(DefaultCookiesPath),
	}
	if !cfg.Status {
		cfg.Username, err = models.NormalizeUsername(args[0])
		if err != nil {
			return Config{}, err
		}
	}
	if provider == GraphProvider {
		cfg.AccessToken = strings.TrimSpace(os.Getenv("IDL_ACCESS_TOKEN"))
		cfg.AccountID = strings.TrimSpace(os.Getenv("IDL_ACCOUNT_ID"))
	}
	return cfg, nil
}

func ProviderName(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", CookiesProvider:
		return CookiesProvider, nil
	case GraphProvider:
		return GraphProvider, nil
	default:
		return "", fault.New(fault.InvalidInput, "provider selection", "IDL_PROVIDER must be cookies or graph; no fallback performed")
	}
}

// Prefer the executable and working directories over a local files bundle.
func ResolveCookiesPath(path string) string { return resolveCookiesPath(path, os.Executable) }

func resolveCookiesPath(path string, executable func() (string, error)) string {
	if path == "" {
		path = DefaultCookiesPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	if exe, err := executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if path == DefaultCookiesPath {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			bundled := filepath.Join("files", path)
			if _, err := os.Stat(bundled); err == nil {
				return bundled
			}
		}
	}
	return filepath.Clean(path)
}
