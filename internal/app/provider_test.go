package app

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/models"
)

type fakeProvider struct {
	Provider
	profileErr            error
	sectionErr            map[string]error
	calls                 []string
	unsupported           bool
	posts, reels, stories []models.Media
	highlights            []models.Highlight
	highlightMedia        []models.HighlightMedia
}

func (f *fakeProvider) Capabilities() map[string]models.Capability {
	caps := map[string]models.Capability{}
	for _, name := range []string{"posts", "reels", "stories", "highlights"} {
		caps[name] = models.Capability{Supported: !(name == "highlights" && f.unsupported), Reason: "synthetic provider"}
	}
	return caps
}
func (f *fakeProvider) FetchProfile(context.Context, string) (models.Profile, error) {
	f.calls = append(f.calls, "profile")
	return models.Profile{UserID: "123", Username: "owner"}, f.profileErr
}
func (f *fakeProvider) FetchPostsPage(context.Context, string, string) ([]models.Media, models.PageInfo, string, error) {
	f.calls = append(f.calls, "posts")
	return f.posts, models.PageInfo{}, "123", f.sectionErr["posts"]
}
func (f *fakeProvider) FetchReelsPage(context.Context, string, string, string) ([]models.Media, models.PageInfo, error) {
	f.calls = append(f.calls, "reels")
	return f.reels, models.PageInfo{}, f.sectionErr["reels"]
}
func (f *fakeProvider) FetchStories(context.Context, string, string) ([]models.Media, error) {
	f.calls = append(f.calls, "stories")
	return f.stories, f.sectionErr["stories"]
}
func (f *fakeProvider) FetchHighlightsTray(context.Context, string, string) ([]models.Highlight, error) {
	f.calls = append(f.calls, "highlights")
	return f.highlights, f.sectionErr["highlights"]
}

func (f *fakeProvider) FetchHighlightsPage(context.Context, string, []string, string, int) ([]models.HighlightMedia, models.PageInfo, error) {
	f.calls = append(f.calls, "highlight page")
	return f.highlightMedia, models.PageInfo{}, nil
}

func TestAllCategoriesDownloadSyntheticMedia(t *testing.T) {
	jpeg := testJPEGBytes(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.Contains(r.URL.Path, "video") {
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(testMP4())
		} else {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpeg)
		}
	}))
	defer srv.Close()
	photo := func(id string) models.Media {
		return models.Media{PK: id, MediaType: 1, TakenAt: 1700000000, ImageVersions2: models.ImageVersions2{Candidates: []models.Candidate{{URL: srv.URL + "/image.jpg"}}}}
	}
	video := models.Media{PK: "3", MediaType: 2, TakenAt: 1700000000, VideoVersions: []models.Candidate{{URL: srv.URL + "/video.mp4"}}}
	f := &fakeProvider{
		posts: []models.Media{photo("1"), {PK: "album", MediaType: 8, CarouselMedia: []models.Media{photo("2"), video}}},
		reels: []models.Media{{PK: "6", MediaType: 2, VideoVersions: video.VideoVersions}, video}, stories: []models.Media{photo("4")},
		highlights:     []models.Highlight{{ID: "h1", Title: "Same"}, {ID: "h2", Title: "Same"}},
		highlightMedia: []models.HighlightMedia{{ID: "h1", Items: []models.Media{photo("5")}}, {ID: "h2", Items: []models.Media{{PK: "7", MediaType: 2, VideoVersions: video.VideoVersions}}}},
	}
	dir := t.TempDir()
	var err error
	out := captureOutput(t, func() {
		err = RunWithProvider(context.Background(), config.Config{Username: "owner", OutputRoot: dir}, f)
	})
	if err != nil || !strings.Contains(out, "Download completed. 7 files saved. 1 repeated.") || requests != 7 {
		t.Fatalf("requests=%d err=%v output=%s", requests, err, out)
	}
	for _, line := range []string{
		"Posts       [####################] 3/3",
		"Reels       [####################] 2/2 processed  1 new  1 repeated",
		"Stories     [####################] 1/1",
		"Highlights  [####################] 2/2",
	} {
		if strings.Count(out, line) != 1 {
			t.Fatalf("missing or duplicated progress row %q in %q", line, out)
		}
	}
	for category, count := range map[string]int{"posts": 3, "reels": 1, "stories": 1, "highlights/Same_h1": 1, "highlights/Same_h2": 1} {
		files, err := os.ReadDir(filepath.Join(dir, "owner", filepath.FromSlash(category)))
		if err != nil || len(files) != count {
			t.Fatalf("%s files=%v err=%v", category, files, err)
		}
	}
	// Reuse the seven validated files and keep a new highlight after a bad one.
	f.highlightMedia[1].Items = append(f.highlightMedia[1].Items, models.Media{PK: "8", MediaType: 1}, photo("9"))
	out = captureOutput(t, func() {
		err = RunWithProvider(context.Background(), config.Config{Username: "owner", OutputRoot: dir}, f)
	})
	if !fault.Is(err, fault.Unavailable) || requests != 8 || !strings.Contains(out, "Partial completion. 1 file saved. 7 existing. 1 repeated. 1 failed.") {
		t.Fatalf("requests=%d err=%v output=%s", requests, err, out)
	}
}

func TestValidPartialMediaIsSavedAndReported(t *testing.T) {
	jpeg := testJPEGBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpeg)
	}))
	defer srv.Close()
	f := &fakeProvider{posts: []models.Media{{PK: "1", MediaType: 1, ImageVersions2: models.ImageVersions2{Candidates: []models.Candidate{{URL: srv.URL}}}}}, sectionErr: map[string]error{"posts": fault.New(fault.Partial, "posts", "verified synthetic result")}}
	dir := t.TempDir()
	var err error
	out := captureOutput(t, func() {
		err = RunWithProvider(context.Background(), config.Config{Username: "owner", OutputRoot: dir}, f)
	})
	files, readErr := os.ReadDir(filepath.Join(dir, "owner", "posts"))
	if !fault.OnlyPartial(err) || readErr != nil || len(files) != 1 || !strings.Contains(out, "Partial completion. 1 file saved.") || len(f.calls) != 5 {
		t.Fatalf("files=%v calls=%v error=%v read=%v output=%s", files, f.calls, err, readErr, out)
	}
}

func TestDownloadDenialStopsAlternateURLsAndOtherSections(t *testing.T) {
	for status, kind := range map[int]fault.Kind{401: fault.Authentication, 403: fault.Permission, 429: fault.RateLimited} {
		t.Run(string(kind), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(status) }))
			defer srv.Close()
			f := &fakeProvider{posts: []models.Media{{PK: "1", MediaType: 1, ImageVersions2: models.ImageVersions2{Candidates: []models.Candidate{{URL: srv.URL + "/1", Width: 100, Height: 100}, {URL: srv.URL + "/2", Width: 50, Height: 50}}}}}}
			var err error
			out := captureOutput(t, func() {
				err = RunWithProvider(context.Background(), config.Config{Username: "owner", OutputRoot: t.TempDir()}, f)
			})
			reason := strings.TrimSuffix(userMessage(fault.New(kind, "download", "")), ".")
			if !fault.Is(err, kind) || requests != 1 || strings.Join(f.calls, ",") != "profile,posts" ||
				!strings.Contains(out, reason) || !strings.Contains(out, "1 failed.") {
				t.Fatalf("requests=%d calls=%v err=%v output=%s", requests, f.calls, err, out)
			}
		})
	}
}

func captureOutput(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old; _ = w.Close(); _ = r.Close() }()
	f()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProviderSectionOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name           string
		profile, posts error
		unsupported    bool
		calls, status  string
		failed         bool
	}{
		{"empty", nil, nil, false, "profile,posts,reels,stories,highlights", "Download completed. 0 files saved.", false},
		{"independent", nil, fault.New(fault.Schema, "posts", "bad contract"), false, "profile,posts,reels,stories,highlights", "Media compatibility error.", true},
		{"partial", nil, fault.New(fault.Partial, "posts", "some fields unavailable"), false, "profile,posts,reels,stories,highlights", "Partial response.", true},
		{"auth", nil, fault.New(fault.Authentication, "posts", "expired"), false, "profile,posts", "Authentication unavailable.", true},
		{"denied", nil, fault.New(fault.Permission, "posts", "denied"), false, "profile,posts", "Access restricted.", true},
		{"verification", nil, fault.New(fault.Verification, "posts", "checkpoint"), false, "profile,posts", "Access restricted.", true},
		{"rate", nil, fault.New(fault.RateLimited, "posts", "stop"), false, "profile,posts", "Rate limit reached. Download stopped.", true},
		{"profile gate", fault.New(fault.Authentication, "profile", "missing"), nil, false, "profile", "Download not started.", true},
		{"authenticated but missing target", instagram.ErrTargetProfileMissing, nil, false, "profile", "[OK] Authentication\n[!!] Profile", true},
		{"unsupported", nil, nil, true, "profile,posts,reels,stories", "Highlights: unavailable", true},
		{"joined partial denial", nil, errors.Join(fault.New(fault.Partial, "posts", "data"), fault.New(fault.Permission, "posts", "denied")), false, "profile,posts", "Access restricted.", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeProvider{profileErr: tt.profile, sectionErr: map[string]error{"posts": tt.posts}, unsupported: tt.unsupported}
			var err error
			out := captureOutput(t, func() {
				err = RunWithProvider(context.Background(), config.Config{Username: "owner", OutputRoot: t.TempDir()}, f)
			})
			if (err != nil) != tt.failed || strings.Join(f.calls, ",") != tt.calls || !strings.Contains(out, tt.status) {
				t.Fatalf("calls=%v err=%v output=%s", f.calls, err, out)
			}
		})
	}
}

// A small ISO BMFF container with a video handler and media payload.
func testMP4() []byte {
	box := func(kind string, payload []byte) []byte {
		b := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], kind)
		copy(b[8:], payload)
		return b
	}
	handler := append(make([]byte, 8), []byte("vide")...)
	data := box("ftyp", []byte("isom0000"))
	data = append(data, box("moov", box("trak", box("mdia", box("hdlr", handler))))...)
	return append(data, box("mdat", []byte{1})...)
}
