package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baptistax/idl/internal/downloader"
	"github.com/baptistax/idl/internal/instagram"
)

func TestTimelineMediaJobsUsesCarouselItemsOnly(t *testing.T) {
	t.Parallel()

	parent := instagram.Media{
		PK: "parent",
		CarouselMedia: []instagram.Media{
			{PK: "first"},
			{PK: "second"},
		},
	}

	jobs := timelineMediaJobs(parent)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].media.PK != "first" || jobs[0].idx != 1 {
		t.Fatalf("unexpected first job: %+v", jobs[0])
	}
	if jobs[1].media.PK != "second" || jobs[1].idx != 2 {
		t.Fatalf("unexpected second job: %+v", jobs[1])
	}
}

func TestTimelineMediaJobsKeepsSingleMedia(t *testing.T) {
	t.Parallel()

	item := instagram.Media{PK: "solo"}

	jobs := timelineMediaJobs(item)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].media.PK != "solo" || jobs[0].idx != 0 {
		t.Fatalf("unexpected job: %+v", jobs[0])
	}
}

func TestHighlightDirNamesDisambiguatesDuplicateTitles(t *testing.T) {
	t.Parallel()

	dirs := highlightDirNames([]instagram.Highlight{
		{ID: "123", Title: "Trip"},
		{ID: "456", Title: "Trip"},
		{ID: "789", Title: "Friends"},
	})

	if dirs["123"] != "Trip_123" {
		t.Fatalf("unexpected dir for first duplicate: %q", dirs["123"])
	}
	if dirs["456"] != "Trip_456" {
		t.Fatalf("unexpected dir for second duplicate: %q", dirs["456"])
	}
	if dirs["789"] != "Friends" {
		t.Fatalf("unexpected dir for unique title: %q", dirs["789"])
	}
}

func TestHighlightDirNamesHandlesBlankAndInvalidTitles(t *testing.T) {
	t.Parallel()

	dirs := highlightDirNames([]instagram.Highlight{
		{ID: "101", Title: ""},
		{ID: "202", Title: "!!!"},
		{ID: "303", Title: "\u200b"},
	})

	if dirs["101"] != "highlight_101" {
		t.Fatalf("unexpected dir for blank title: %q", dirs["101"])
	}
	if dirs["202"] != "highlight_202" {
		t.Fatalf("unexpected dir for invalid title: %q", dirs["202"])
	}
	if dirs["303"] != "highlight_303" {
		t.Fatalf("unexpected dir for invisible title: %q", dirs["303"])
	}
}

func TestSectionFailureDoesNotStopLaterSections(t *testing.T) {
	t.Parallel()

	var calls []string
	errs := runIndependentSections([]sectionRunner{
		func() error {
			calls = append(calls, "posts")
			return errors.New("posts failed")
		},
		func() error {
			calls = append(calls, "stories")
			return nil
		},
		func() error {
			calls = append(calls, "highlights")
			return nil
		},
	})

	if errs != 1 {
		t.Fatalf("unexpected error count: %d", errs)
	}
	if got := len(calls); got != 3 {
		t.Fatalf("expected all sections to run, got %d", got)
	}
}

func TestFatalProfileIDFailureStopsBeforeSections(t *testing.T) {
	t.Parallel()

	fatalErr := errors.New("failed to resolve profile id")
	sectionsRan := false
	if fatalErr != nil {
		if sectionsRan {
			t.Fatal("sections should not run after fatal profile error")
		}
		return
	}
	_ = runIndependentSections([]sectionRunner{func() error {
		sectionsRan = true
		return nil
	}})
}

func TestDownloadMediaErrorsWhenMediaHasNoURL(t *testing.T) {
	t.Parallel()

	err := downloadMedia(context.Background(), nil, nil, nil, "user", "user", "posts", instagram.Media{PK: "missing", MediaType: 2, ProductType: "clips"}, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "media missing is a video but has no downloadable video URL" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestResolveDownloadMediaHydratesReelWhenVideoURLMissing(t *testing.T) {
	t.Parallel()

	called := false
	initial := instagram.Media{PK: "9001", Code: "ABC123", MediaType: 2, ProductType: "clips"}
	hydrated, isVideo, err := resolveDownloadMedia(context.Background(), "kemillynicolle_", initial, func(ctx context.Context, username, mediaPK, mediaCode string) (instagram.Media, error) {
		called = true
		if username != "kemillynicolle_" || mediaPK != "9001" || mediaCode != "ABC123" {
			t.Fatalf("unexpected hydrate args: %q %q %q", username, mediaPK, mediaCode)
		}
		return instagram.Media{
			PK:            mediaPK,
			Code:          mediaCode,
			MediaType:     2,
			ProductType:   "clips",
			VideoVersions: []instagram.Candidate{{URL: "https://example.test/video.mp4", Width: 720, Height: 1280}},
		}, nil
	})
	if err != nil {
		t.Fatalf("resolveDownloadMedia: %v", err)
	}
	if !called {
		t.Fatal("expected hydrator to be called")
	}
	if !isVideo {
		t.Fatal("expected video media")
	}
	if got := instagram.BestVideoURL(hydrated); got != "https://example.test/video.mp4" {
		t.Fatalf("unexpected best video url: %q", got)
	}
}

func TestDownloadMediaUsesImageFlowForImages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(testJPEGBytes(t))
	}))
	defer srv.Close()

	dl := downloader.New(downloader.Options{
		OutputDir: dir,
		Timeout:   5 * time.Second,
	})

	m := instagram.Media{
		PK:             "img1",
		TakenAt:        1710000000,
		ImageVersions2: instagram.ImageVersions2{Candidates: []instagram.Candidate{{URL: srv.URL, Width: 800, Height: 800}}},
		MediaType:      1,
		ProductType:    "feed",
	}
	if err := downloadMedia(context.Background(), nil, dl, nil, "user", "user", "posts", m, 0); err != nil {
		t.Fatalf("downloadMedia: %v", err)
	}

	want := filepath.Join(dir, "user", "posts", time.Unix(1710000000, 0).UTC().Format("20060102_150405")+"_img1.jpg")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected output file: %v", err)
	}
}

func testJPEGBytes(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}
