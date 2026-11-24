package validation

import (
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/models"
)

const (
	SmokeRequestBudget = 3
	maxPhotoBytes      = 10 << 20
	maxPhotoPixels     = 10_000_000
)

type SmokeOptions struct {
	Authorized                                           bool
	Username, AccountID, MediaID, AccessToken, OutputDir string
}

// Diagnostic is safe to print: no usernames, identifiers, paths, cookies, tokens,
// raw error strings, response bodies or signed URLs. It never certifies another
// category or a provider-wide live-verified state.
type Diagnostic struct {
	Provider, Authentication, Profile, Category, Metadata, Download, Validation string
	Classification                                                              fault.Kind
	Cancellation                                                                string
	Requests, LastHTTPStatus                                                    int
	Duration                                                                    time.Duration
}

func approvedRequest(req *http.Request, accountID, mediaID string) bool {
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" && req.URL.Port() != "443" || req.Header.Get("Cookie") != "" {
		return false
	}
	host := strings.ToLower(req.URL.Hostname())
	if host == "graph.facebook.com" {
		prefix := "/" + instagram.GraphVersion + "/"
		return req.Header.Get("Authorization") != "" && (req.URL.Path == prefix+accountID || req.URL.Path == prefix+mediaID)
	}
	if req.Header.Get("Authorization") != "" {
		return false
	}
	return strings.HasSuffix(host, ".cdninstagram.com") || strings.HasSuffix(host, ".fbcdn.net")
}

func runGraphSmoke(ctx context.Context, opts SmokeOptions, base http.RoundTripper) (diagnostic Diagnostic, runErr error) {
	started := time.Now()
	diagnostic = Diagnostic{Provider: "graph", Category: "one selected feed photo", Authentication: "not verified", Profile: "not attempted", Metadata: "not attempted", Download: "not attempted", Validation: "not attempted"}
	guard := &requestGuard{base: base, limit: SmokeRequestBudget, allow: func(r *http.Request) bool {
		return approvedRequest(r, opts.AccountID, opts.MediaID)
	}}
	defer func() {
		diagnostic.Requests, diagnostic.LastHTTPStatus = guard.stats()
		diagnostic.Duration = time.Since(started)
		if runErr != nil {
			diagnostic.Classification = classification(runErr)
			switch {
			case errors.Is(runErr, context.Canceled):
				diagnostic.Cancellation = "context cancelled"
			case errors.Is(runErr, context.DeadlineExceeded):
				diagnostic.Cancellation = "deadline reached"
			default:
				diagnostic.Cancellation = "stopped on first failure"
			}
		}
	}()
	if !opts.Authorized {
		return diagnostic, fault.New(fault.Permission, "live smoke", "explicit authorization required; no request sent")
	}
	username, err := models.NormalizeUsername(opts.Username)
	if err != nil {
		return diagnostic, err
	}
	if !models.IsNumericID(opts.MediaID) || opts.OutputDir == "" {
		return diagnostic, fault.New(fault.InvalidInput, "live smoke", "selected numeric media ID and private output directory required")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return diagnostic, err
	}
	hc := &http.Client{Transport: guard, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider, err := instagram.NewClient(instagram.Options{AccessToken: opts.AccessToken, AccountID: opts.AccountID, HTTPClient: hc})
	if err != nil {
		return diagnostic, err
	}
	diagnostic.Profile = "attempted"
	if _, err := provider.FetchProfile(ctx, username); err != nil {
		return diagnostic, err
	}
	diagnostic.Authentication = "API request accepted for matching account"
	diagnostic.Profile = "matched"
	diagnostic.Metadata = "attempted"
	media, err := provider.FetchSinglePhoto(ctx, username, opts.MediaID)
	if err != nil {
		return diagnostic, err
	}
	diagnostic.Metadata = "matched selected owned photo"
	url := instagram.BestImageURL(media)
	if url == "" {
		return diagnostic, fault.New(fault.Unsupported, "live smoke", "permitted source did not provide a photo URL")
	}
	if err := os.MkdirAll(opts.OutputDir, 0700); err != nil {
		return diagnostic, &fault.Error{Kind: fault.InvalidInput, Op: "live smoke", Detail: "private output directory unavailable", Cause: err}
	}
	dir, err := os.MkdirTemp(opts.OutputDir, "smoke-")
	if err != nil {
		return diagnostic, &fault.Error{Kind: fault.InvalidInput, Op: "live smoke", Detail: "private output directory unavailable", Cause: err}
	}
	dl := downloader.New(downloader.Options{OutputDir: dir, UserAgent: "IDL/2.0", HTTPClient: hc})
	diagnostic.Download = "attempted"
	path, err := dl.DownloadToFile(ctx, url, "selected.media")
	if err != nil {
		return diagnostic, err
	}
	diagnostic.Download = "one file saved"
	if err := validatePhoto(ctx, path); err != nil {
		_ = os.Remove(path)
		diagnostic.Validation = "failed; invalid file removed"
		return diagnostic, err
	}
	diagnostic.Validation = "one decodable photo verified"
	return diagnostic, nil
}

func validatePhoto(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return &fault.Error{Kind: fault.Schema, Op: "photo validation", Detail: "downloaded file unavailable", Cause: err}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 || info.Size() > maxPhotoBytes {
		return fault.New(fault.Schema, "photo validation", "empty or oversized file")
	}
	config, _, err := image.DecodeConfig(f)
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxPhotoPixels/config.Height {
		return fault.New(fault.Schema, "photo validation", "invalid or oversized image dimensions")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fault.New(fault.Schema, "photo validation", "unable to read image")
	}
	if _, _, err := image.Decode(f); err != nil {
		return fault.New(fault.Schema, "photo validation", "image failed full decoding")
	}
	return ctx.Err()
}

func classification(err error) fault.Kind {
	for _, kind := range []fault.Kind{fault.Budget, fault.Authentication, fault.Permission, fault.Verification, fault.RateLimited, fault.Unavailable, fault.Download, fault.Unsupported, fault.InvalidInput, fault.Schema, fault.Partial, fault.Network, fault.Upstream} {
		if fault.Is(err, kind) {
			return kind
		}
	}
	return fault.Network
}
