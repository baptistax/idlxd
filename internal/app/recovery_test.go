package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

type reelFixture struct {
	fakeProvider
	detail  map[string]models.Media
	lookups []string
}

func (f *reelFixture) FetchMediaInfo(_ context.Context, _, pk, _ string) (models.Media, error) {
	f.lookups = append(f.lookups, pk)
	return f.detail[pk], nil
}

func TestReelAccountingAcrossListingsAndRuns(t *testing.T) {
	dir := t.TempDir()
	mp4, photo := testMP4(), testJPEGBytes(t)
	media := func(pk string, kind int, product, asset string) models.Media {
		m := models.Media{PK: pk, MediaType: kind, ProductType: product, User: models.IGUser{PK: "123"}}
		if asset != "" {
			c := []models.Candidate{{URL: "https://example.invalid/" + asset}}
			if kind == 2 {
				m.VideoVersions = c
			} else {
				m.ImageVersions2.Candidates = c
			}
		}
		return m
	}
	timeline := media("1", 2, "clips", "video")
	lookup := media("2", 2, "clips", "")
	bad := media("3", 2, "clips", "")
	unavailable := media("6", 2, "clips", "")
	story := media("4", 1, "story", "photo")
	highlight := media("5", 1, "story", "photo")
	f := &reelFixture{fakeProvider: fakeProvider{
		posts: []models.Media{timeline}, reels: []models.Media{timeline, lookup, bad, unavailable},
		stories: []models.Media{story}, highlights: []models.Highlight{{ID: "h", Title: "kept"}},
		highlightMedia: []models.HighlightMedia{{ID: "h", Items: []models.Media{story, highlight, bad}}},
	}, detail: map[string]models.Media{"2": media("2", 2, "clips", "video"), "3": media("3", 1, "feed", "poster"), "6": unavailable}}
	requests := 0
	dl := downloader.New(downloader.Options{OutputDir: dir, HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := photo
		switch r.URL.Path {
		case "/video":
			body = mp4
		case "/photo":
		default:
			t.Fatal("unexpected media request")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}})
	root := filepath.Join(dir, "owner")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(want fileCounts, expected ...string) {
		t.Helper()
		index, err := indexExisting(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		d := profileDownload{ig: f, dl: dl, safeUser: "owner", profile: models.Profile{Username: "owner", UserID: "123"}, existing: index}
		var out bytes.Buffer
		got, err := d.runSections(t.Context(), terminal{out: &out})
		if !fault.Is(err, fault.Schema) || got != want {
			t.Fatalf("counts=%+v err=%v", got, err)
		}
		for _, line := range expected {
			if !strings.Contains(out.String(), line) {
				t.Fatalf("missing %s in %s", line, out.String())
			}
		}
	}
	run(fileCounts{saved: 4, repeated: 3, failed: 2}, "Posts       [....................] 0/0", "Reels       [####################] 5/5 processed  2 new  1 repeated  2 failed  Media compatibility error [3]. Media unavailable [6].", "Stories     [####################] 1/1", "Highlights  [####################] 3/3 processed  1 new  2 repeated")
	if requests != 4 || strings.Join(f.lookups, ",") != "2,3,6" {
		t.Fatal("incorrect initial request counts", requests, f.lookups)
	}
	// A previous download can have another index, timestamp, or directory.
	if err := os.Rename(filepath.Join(root, "reels", "unknown_2.mp4"), filepath.Join(root, "stories", "20240101_010000_2_09.mp4")); err != nil {
		t.Fatal(err)
	}
	run(fileCounts{existing: 4, repeated: 3, failed: 2}, "5/5 processed  0 new  2 existing", "1/1 processed  0 new  1 existing")
	if requests != 4 || strings.Join(f.lookups, ",") != "2,3,6,3,6" {
		t.Fatal("existing media caused a request", requests, f.lookups)
	}
	// A thumbnail renamed to MP4 must fail the existing-file check.
	if err := os.WriteFile(filepath.Join(root, "reels", "unknown_1.mp4"), photo, 0600); err != nil {
		t.Fatal(err)
	}
	run(fileCounts{saved: 1, existing: 3, repeated: 3, failed: 2}, "5/5 processed  1 new  1 existing")
	if requests != 5 {
		t.Fatal("corrupt destination not repaired exactly once")
	}
}

type workloadProvider struct {
	fakeProvider
	pageCount int
}

func TestTimelineReelCountSurvivesGlobalStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write(testMP4())
	}))
	defer srv.Close()
	f := &fakeProvider{posts: []models.Media{
		{PK: "1", MediaType: 2, ProductType: "clips", VideoVersions: []models.Candidate{{URL: srv.URL + "/reel"}}},
		{PK: "2", MediaType: 1, ImageVersions2: models.ImageVersions2{Candidates: []models.Candidate{{URL: srv.URL + "/blocked"}}}},
	}}
	d := profileDownload{ig: f, dl: downloader.New(downloader.Options{OutputDir: t.TempDir()}), safeUser: "owner", profile: models.Profile{Username: "owner", UserID: "123"}}
	var out bytes.Buffer
	files, err := d.runSections(t.Context(), terminal{out: &out})
	if !fault.Is(err, fault.Permission) || files.saved != 1 || strings.Join(f.calls, ",") != "posts" ||
		!strings.Contains(out.String(), "Reels       [####################] 1/1 processed  1 new  collection not checked") {
		t.Fatal("lost prior reel or continued after denial", files, f.calls, out.String())
	}
}

func TestHighlightFailuresPreserveFilesAndContinue(t *testing.T) {
	photo := func(id string) models.Media {
		return models.Media{PK: id, MediaType: 1, ImageVersions2: models.ImageVersions2{
			Candidates: []models.Candidate{{URL: "https://offline.invalid/" + id + "?signature=private"}},
		}}
	}
	f := &fakeProvider{
		highlights: []models.Highlight{{ID: "a", Title: "One"}, {ID: "b", Title: "Two"}},
		highlightMedia: []models.HighlightMedia{
			{ID: "a", Items: []models.Media{photo("1"), photo("2"), photo("3"), photo("4"), photo("5")}},
			{ID: "b", Items: []models.Media{photo("2"), photo("1"), photo("6")}},
		},
	}
	body := testJPEGBytes(t)
	dir := t.TempDir()
	var requests []string
	dl := downloader.New(downloader.Options{OutputDir: dir, HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		status, data := http.StatusOK, body
		switch r.URL.Path {
		case "/2":
			status = 404
		case "/3":
			status = 503
		case "/4":
			data = body[:12]
		case "/5":
			return nil, context.DeadlineExceeded // Client timeout while the run context is live.
		}
		return &http.Response{StatusCode: status, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}})
	d := profileDownload{ig: f, dl: dl, safeUser: "owner", profile: models.Profile{Username: "owner", UserID: "123"}}
	var out bytes.Buffer
	counts, err := d.runSections(t.Context(), terminal{out: &out})
	if err == nil || counts != (fileCounts{saved: 2, repeated: 2, failed: 4}) || strings.Join(requests, ",") != "/1,/2,/3,/4,/5,/6" {
		t.Fatalf("counts=%+v requests=%v err=%v", counts, requests, err)
	}
	for _, want := range []string{"8/8 processed  2 new  2 repeated  4 failed", "HTTP 404 [2]", "HTTP 503 [3]", "Media validation failed", "[4]", "Media request timed out [5]"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "private") || strings.Contains(out.String(), "Instagram") {
		t.Fatal(out.String())
	}
	for folder, name := range map[string]string{"One": "unknown_1_01.jpg", "Two": "unknown_6_03.jpg"} {
		path := filepath.Join(dir, "owner", "highlights", folder)
		files, err := os.ReadDir(path)
		if err != nil || len(files) != 1 || files[0].Name() != name {
			t.Fatalf("unexpected files in %s: %v, %v", folder, files, err)
		}
		got, err := os.ReadFile(filepath.Join(path, name))
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal("saved highlight was changed", err)
		}
	}
}

func (f *workloadProvider) FetchPostsPage(_ context.Context, _ string, after string) ([]models.Media, models.PageInfo, string, error) {
	start := 0
	if after != "" {
		var err error
		start, err = strconv.Atoi(after)
		if err != nil {
			return nil, models.PageInfo{}, "", err
		}
	}
	const count = 3000
	end := min(count, start+30)
	items := make([]models.Media, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, models.Media{PK: strconv.Itoa(i + 1), MediaType: 1, ImageVersions2: models.ImageVersions2{Candidates: []models.Candidate{{URL: "https://offline.invalid/photo"}}}})
	}
	f.pageCount++
	return items, models.PageInfo{HasNextPage: end < count, EndCursor: strconv.Itoa(end)}, "123", nil
}

func TestLargeOfflinePagedDownload(t *testing.T) {
	dir := t.TempDir()
	body := testJPEGBytes(t)
	calls := 0
	dl := downloader.New(downloader.Options{OutputDir: dir, HTTPClient: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}})
	f := &workloadProvider{}
	d := profileDownload{ig: f, dl: dl, safeUser: "owner", profile: models.Profile{Username: "owner", UserID: "123"}}
	got, err := d.runSections(t.Context(), terminal{out: io.Discard})
	if err != nil || got.saved != 3000 || calls != 3000 || f.pageCount != 100 {
		t.Fatalf("saved=%d calls=%d pages=%d err=%v", got.saved, calls, f.pageCount, err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "owner", "posts"))
	if err != nil || len(files) != 3000 {
		t.Fatal("unexpected output file count", len(files), err)
	}
}

// Optionally exercise the ordinary CLI orchestration with an archived MP4
// served by a local server. The input file is read-only.
func TestArchivedReelAccounting(t *testing.T) {
	path := os.Getenv("IDL_TEST_REEL_FILE")
	if path == "" {
		t.Skip("local archived MP4 not selected")
	}
	transfers := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transfers++
		http.ServeFile(w, r, path)
	}))
	defer srv.Close()
	m := models.Media{PK: "12345", MediaType: 2, ProductType: "clips", VideoVersions: []models.Candidate{{URL: srv.URL + "/reel.mp4"}}}
	provider := &fakeProvider{posts: []models.Media{m}, reels: []models.Media{m}}
	cfg := config.Config{Username: "owner", OutputRoot: t.TempDir()}
	for run := range 2 {
		var out bytes.Buffer
		if err := runDownload(t.Context(), cfg, provider, nil, terminal{out: &out}); err != nil {
			t.Fatal("archived video rejected", err)
		}
		msg := "Download completed. 1 file saved."
		if run == 1 {
			msg = "Download completed. 0 files saved. 1 existing."
		}
		if transfers != 1 || !strings.Contains(out.String(), msg) {
			t.Fatal("incorrect archived reel accounting")
		}
	}
}
