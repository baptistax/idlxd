package instagram

import (
	"encoding/json"
	"testing"
)

func TestHighlightsTrayParsesIDsAndTitles(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"data":{
			"highlights":{
				"edges":[
					{"node":{"id":"highlight:1","title":"Trip"}},
					{"node":{"id":"highlight:2","title":"Work"}}
				]
			}
		}
	}`)

	var out highlightsTrayResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal highlights tray payload: %v", err)
	}
	if len(out.Data.Highlights.Edges) != 2 {
		t.Fatalf("unexpected highlights length: %d", len(out.Data.Highlights.Edges))
	}
	if out.Data.Highlights.Edges[0].Node.ID != "highlight:1" || out.Data.Highlights.Edges[0].Node.Title != "Trip" {
		t.Fatalf("unexpected first highlight: %+v", out.Data.Highlights.Edges[0].Node)
	}
}

func TestHighlightsInitialPageParsesItemsAndPageInfo(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"data":{
			"xdt_api__v1__feed__reels_media__connection":{
				"edges":[{"node":{"id":"highlight:1","items":[{"pk":"item_1","media_type":1}]}}],
				"page_info":{"end_cursor":"CURSOR_1","has_next_page":true}
			}
		}
	}`)

	var out highlightsPageResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal highlights page payload: %v", err)
	}
	if len(out.Data.Connection.Edges) != 1 {
		t.Fatalf("unexpected edge count: %d", len(out.Data.Connection.Edges))
	}
	if got := out.Data.Connection.Edges[0].Node.Items[0].PK; got != "item_1" {
		t.Fatalf("unexpected item pk: %q", got)
	}
	if out.Data.Connection.PageInfo.EndCursor != "CURSOR_1" || !out.Data.Connection.PageInfo.HasNextPage {
		t.Fatalf("unexpected page info: %+v", out.Data.Connection.PageInfo)
	}
}

func TestHighlightsPaginationStopsWhenHasNextPageFalse(t *testing.T) {
	t.Parallel()

	pages := []PageInfo{
		{EndCursor: "CURSOR_1", HasNextPage: true},
		{EndCursor: "CURSOR_2", HasNextPage: true},
		{EndCursor: "", HasNextPage: false},
	}
	visited := 0
	after := ""
	for {
		pageInfo := pages[visited]
		visited++
		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			break
		}
		after = pageInfo.EndCursor
	}
	if visited != 3 {
		t.Fatalf("unexpected visited pages: %d", visited)
	}
	if after != "CURSOR_2" {
		t.Fatalf("unexpected last cursor: %q", after)
	}
}
