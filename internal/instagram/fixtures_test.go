package instagram

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/fault"
)

func TestPopulatedMediaResponses(t *testing.T) {
	const photo = `{"id":"10_123","pk":"10","user":{"id":"123"},"media_type":1}`
	const video = `{"pk":11,"user":{"pk":123},"media_type":2}`

	t.Run("posts", func(t *testing.T) {
		body := `{"data":{"xdt_api__v1__feed__user_timeline_graphql_connection":{
			"edges":[{"node":` + photo + `},{"node":` + video + `}],
			"page_info":{"has_next_page":true,"end_cursor":"next"}}}}`
		items, page, err := ParseMediaPage([]byte(body), "posts", "123")
		if err != nil || !slices.Equal(MediaIDs(items), []string{"10", "11"}) ||
			items[0].ID != "10_123" || items[1].MediaType != 2 || !page.HasNextPage || page.EndCursor != "next" {
			t.Fatalf("items=%+v page=%+v err=%v", items, page, err)
		}
		mixed := strings.Replace(body, `"user":{"pk":123}`, `"user":{"pk":456}`, 1)
		items, _, err = ParseMediaPage([]byte(mixed), "posts", "123")
		if len(items) != 0 || !fault.Is(err, fault.Permission) {
			t.Fatalf("mixed-owner page returned %d items: %v", len(items), err)
		}
	})

	t.Run("stories", func(t *testing.T) {
		body := `{"data":{"xdt_api__v1__feed__reels_media":{"reels_media":[
			{"id":"123","items":[` + video + `,` + photo + `]}]}}}`
		items, err := ParseStories([]byte(body), "123")
		if err != nil || !slices.Equal(MediaIDs(items), []string{"11", "10"}) {
			t.Fatalf("items=%+v err=%v", items, err)
		}
	})

	t.Run("highlight collections", func(t *testing.T) {
		body := `{"data":{"xdt_api__v1__feed__reels_media__connection":{
			"edges":[{"node":{"id":"highlight:1","user":{"pk":"123"},"items":[` + photo + `,` + video + `]}},
			         {"node":{"id":"highlight:2","user":{"id":123},"items":[` + video + `]}}],
			"page_info":{"has_next_page":true,"end_cursor":"next"}}}}`
		groups, page, err := ParseHighlightsPage([]byte(body), "123")
		if err != nil || !page.HasNextPage || page.EndCursor != "next" || len(groups) != 2 ||
			groups[0].ID != "highlight:1" || !slices.Equal(MediaIDs(groups[0].Items), []string{"10", "11"}) ||
			groups[1].ID != "highlight:2" || !slices.Equal(MediaIDs(groups[1].Items), []string{"11"}) {
			t.Fatalf("groups=%+v page=%+v err=%v", groups, page, err)
		}
	})
}

func TestEmptyAndMalformedFixtureContracts(t *testing.T) {
	for _, category := range []string{"posts", "reels"} {
		key := postsConnection
		if category == "reels" {
			key = reelsConnection
		}
		for _, tt := range []struct {
			inner string
			valid bool
		}{
			{`{"edges":[],"page_info":{"end_cursor":null,"has_next_page":false}}`, true},
			{`{}`, false}, {`null`, false}, {`{"edges":[]}`, false},
			{`{"edges":[],"page_info":{}}`, false},
			{`{"edges":[],"page_info":{"has_next_page":true,"end_cursor":null}}`, false},
			{`{"edges":null,"page_info":{"has_next_page":false}}`, false},
			{`{"edges":[{"node":null}],"page_info":{"has_next_page":false}}`, false},
		} {
			body := []byte(`{"data":{"` + key + `":` + tt.inner + `}}`)
			items, _, err := ParseMediaPage(body, category, "123")
			if tt.valid {
				if err != nil || len(items) != 0 {
					t.Fatalf("expected verified empty: %v", err)
				}
			} else if !fault.Is(err, fault.Schema) {
				t.Fatalf("expected schema mismatch for %s: %v", tt.inner, err)
			}
		}
	}
	for _, body := range []string{`{}`, `{"data":{}}`, `{"data":{"xdt_api__v1__feed__reels_media":{}}}`} {
		if _, err := ParseStories([]byte(body), "123"); !fault.Is(err, fault.Schema) {
			t.Fatal(err)
		}
	}
	if items, err := ParseStories([]byte(`{"data":{"xdt_api__v1__feed__reels_media":{"reels_media":[]}}}`), "123"); err != nil || len(items) != 0 {
		t.Fatal(err)
	}
	if reels, _, err := ParseHighlightsPage([]byte(`{"data":{"xdt_api__v1__feed__reels_media__connection":{"edges":[],"page_info":{"has_next_page":false}}}}`), "123"); err != nil || len(reels) != 0 {
		t.Fatal(err)
	}
}

func TestSyntheticMixedCarouselAndPartialIntegrity(t *testing.T) {
	media := `{"id":"9007199254740993_123","pk":9007199254740993,"user":{"pk":123},"media_type":8,"taken_at":1700000000,"carousel_media_count":2,"carousel_media":[{"pk":"2","media_type":1,"user":{"pk":"123"},"image_versions2":{"candidates":[{"url":"https://media.invalid/photo"}]}},{"pk":"3","media_type":2,"user":{"pk":"123"},"video_versions":[{"url":"https://media.invalid/video"}]}]}`
	m, err := parseTestMedia([]byte(media), "123")
	if err != nil || m.PK != "9007199254740993" || len(m.CarouselMedia) != 2 || BestVideoURL(m.CarouselMedia[1]) == "" || BestImageURL(m.CarouselMedia[0]) == "" || m.CarouselMedia[0].TakenAt != m.TakenAt {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{strings.Replace(media, `"carousel_media_count":2`, `"carousel_media_count":3`, 1), strings.Replace(media, `"user":{"pk":"123"}`, `"user":{"pk":"999"}`, 1)} {
		if _, err := parseTestMedia([]byte(bad), "123"); err == nil {
			t.Fatal("accepted invalid album")
		}
	}
	body := []byte(`{"data":{"` + postsConnection + `":{"edges":[{"node":` + media + `}],"page_info":{"has_next_page":false}}},"errors":[{"message":"synthetic partial error"}]}`)
	items, _, err := ParseMediaPage(body, "posts", "123")
	if len(items) != 1 || !fault.OnlyPartial(err) {
		t.Fatalf("valid partial result lost: %v", err)
	}
	denied := strings.Replace(string(body), "synthetic partial error", "access denied", 1)
	items, _, err = ParseMediaPage([]byte(denied), "posts", "123")
	if len(items) != 0 || !fault.Is(err, fault.Permission) {
		t.Fatalf("denial ignored: %v", err)
	}
}

func TestPaginationGuards(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ids    [][]string
		pages  []PageInfo
		failAt int
	}{
		{"complete", [][]string{{"1"}, {"2"}}, []PageInfo{{EndCursor: "a", HasNextPage: true}, {}}, -1},
		{"empty complete", [][]string{{}}, []PageInfo{{}}, -1},
		{"repeated cursor", [][]string{{"1"}, {"2"}}, []PageInfo{{EndCursor: "a", HasNextPage: true}, {EndCursor: "a", HasNextPage: true}}, 1},
		{"missing cursor", [][]string{{"1"}}, []PageInfo{{HasNextPage: true}}, 0},
		{"duplicate", [][]string{{"1"}, {"1"}}, []PageInfo{{EndCursor: "a", HasNextPage: true}, {}}, 1},
		{"within page", [][]string{{"1", "1"}}, []PageInfo{{}}, 0},
		{"after termination", [][]string{{"1"}, {"2"}}, []PageInfo{{}, {}}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPagination()
			for i, page := range tt.pages {
				err := p.Accept(context.Background(), tt.ids[i], page)
				if i == tt.failAt {
					if !fault.Is(err, fault.Schema) {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewPagination().Accept(ctx, nil, PageInfo{}); err != context.Canceled {
		t.Fatal(err)
	}
}

func parseTestMedia(body []byte, owner string) (Media, error) {
	root, err := readObject(body)
	if err != nil {
		return Media{}, err
	}
	return normalizeWebMedia(root, Profile{UserID: owner}, nil)
}
