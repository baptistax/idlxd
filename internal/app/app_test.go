package app

import (
	"context"
	"testing"

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

func TestDownloadMediaErrorsWhenMediaHasNoURL(t *testing.T) {
	t.Parallel()

	err := downloadMedia(context.Background(), nil, nil, "user", "posts", instagram.Media{PK: "missing"}, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "media missing has no downloadable URL" {
		t.Fatalf("unexpected error: %q", got)
	}
}
