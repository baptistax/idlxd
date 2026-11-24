package instagram

import (
	"github.com/baptistax/idl/internal/fault"
	"testing"
)

func TestHighlightsStableCollectionIDsAndEmptyItems(t *testing.T) {
	body := []byte(`{"data":{"xdt_api__v1__feed__reels_media__connection":{"edges":[{"node":{"id":"highlight:1","title":"Same","user":{"pk":"123"},"items":[]}},{"node":{"id":"highlight:2","title":"Same","user":{"pk":"123"},"items":[]}}],"page_info":{"has_next_page":false,"end_cursor":null}}}}`)
	reels, page, err := ParseHighlightsPage(body, "123")
	if err != nil || page.HasNextPage || len(reels) != 2 || reels[0].ID != "highlight:1" || reels[1].ID != "highlight:2" {
		t.Fatalf("%v %v", reels, err)
	}
}

func TestHighlightsRejectMissingContainers(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":{}}`, `{"data":{"xdt_api__v1__feed__reels_media__connection":{"edges":[]}}}`} {
		if _, _, err := ParseHighlightsPage([]byte(body), "123"); !fault.Is(err, fault.Schema) {
			t.Fatal(err)
		}
	}
}
