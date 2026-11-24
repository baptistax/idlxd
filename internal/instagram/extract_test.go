package instagram

import (
	"strings"
	"testing"

	"github.com/baptistax/idl/internal/fault"
)

func TestStructuralContextAndLegacySidecar(t *testing.T) {
	const photo = `{"id":"9007199254740993123","__typename":"GraphImage","display_resources":[{"src":"https://example.invalid/full.webp?b=2&a=1","config_width":1000,"config_height":800}]}`
	const video = `{"id":"35","__typename":"GraphVideo","is_video":true,"display_url":"https://example.invalid/thumbnail.jpg","video_url":"https://example.invalid/video.mp4"}`
	const album = `{"id":"11_123","owner":{"id":123},"__typename":"GraphSidecar","taken_at_timestamp":123456,"edge_sidecar_to_children":{"edges":[{"node":` + photo + `},{"node":` + video + `}]}}`
	const unrelated = `{"pk":"100","user":{"id":"999"},"media_type":1}`
	const collab = `{"pk":"101","user":{"id":"999"},"coauthor_producers":[{"pk":"123"}],"media_type":1}`
	page := `{"edges":[{"node":{"new_wrapper":[` + album + `],"recommendations":[` + unrelated + `]}},{"node":{"is_ad":true,"media":` + unrelated + `}},{"node":` + collab + `}],"page_info":{"has_next_page":false}}`
	body := `{"data":{"recommendations":{"edge_owner_to_timeline_media":` + page + `},"revision":[{"extra":{"edge_owner_to_timeline_media":` + page + `}}]},"unknown":true}`
	items, _, err := ParseMediaPage([]byte(body), "posts", "123")
	if err != nil || len(items) != 1 || len(items[0].CarouselMedia) != 2 {
		t.Fatal("sidecar or context", err)
	}
	a, b := items[0].CarouselMedia[0], items[0].CarouselMedia[1]
	if a.PK != "9007199254740993123" || b.PK != "35" || a.User.PK != "123" || a.TakenAt != 123456 || BestImageURL(a) != "https://example.invalid/full.webp?b=2&a=1" || BestImageURL(b) != "" || BestVideoURL(b) != "https://example.invalid/video.mp4" {
		t.Fatal("lost child identity, order, type or source")
	}
}

func TestStructuralRejections(t *testing.T) {
	const one = `{"pk":"11","id":"11_123","user":{"pk":"123"},"media_type":1}`
	page := func(rows string) []byte {
		return []byte(`{"data":{"` + postsConnection + `":{"edges":[` + rows + `],"page_info":{"has_next_page":false}}}}`)
	}
	for _, tt := range []struct {
		name, node string
		kind       fault.Kind
	}{
		{"unrelated", strings.Replace(one, `"pk":"123"`, `"pk":"999"`, 1), fault.Permission},
		{"conflicting owner", strings.Replace(one, `"media_type"`, `"owner":{"id":"999"},"media_type"`, 1), fault.Permission},
		{"composite owner", strings.Replace(one, "11_123", "11_321", 1), fault.Permission},
		{"media alias", strings.Replace(one, "11_123", "12_123", 1), fault.Schema},
		{"malformed owner", strings.Replace(one, `"pk":"123"`, `"pk":1.1,"id":"123"`, 1), fault.Schema},
		{"ambiguous wrapper", `{"a":` + one + `,"b":` + one + `}`, fault.Schema},
		{"repeated JSON key", strings.Replace(one, `"pk":"11"`, `"pk":"12","pk":"11"`, 1), fault.Schema},
		{"invalid type", strings.Replace(one, `"media_type":1`, `"media_type":9`, 1), fault.Unsupported},
		{"thumbnail classification", strings.Replace(one, `"media_type":1`, `"media_type":1,"product_type":"clips"`, 1), fault.Schema},
		{"missing type", `{"id":11,"user":{"pk":123},"url":"https://example.invalid/a.jpg"}`, fault.Schema},
	} {
		t.Run(tt.name, func(t *testing.T) {
			items, _, err := ParseMediaPage(page(`{"node":`+tt.node+`}`), "posts", "123")
			if len(items) != 0 || !fault.Is(err, tt.kind) {
				t.Fatal("accepted ambiguous media", err)
			}
		})
	}
	for _, body := range [][]byte{page(`{"node":` + one + `},{"node":` + one + `}`), []byte(strings.Repeat(`{"nested":`, 90) + `{}` + strings.Repeat("}", 90)), make([]byte, (16<<20)+1), []byte(`{}{}`)} {
		if items, _, err := ParseMediaPage(body, "posts", "123"); !fault.Is(err, fault.Schema) || len(items) != 0 {
			t.Fatal("invalid structure accepted")
		}
	}
}

func TestPartialNodesAndAccountWarnings(t *testing.T) {
	body := `{"errors":[{"message":"field missing"}],"data":{"` + postsConnection + `":{"edges":[{"node":null},{"node":{"pk":"1","media_type":2,"user":{"pk":"123"},"image_versions2":{"candidates":[{"url":"https://example.invalid/poster.jpg"}]}}}],"page_info":{"has_next_page":false}}}}`
	items, _, err := ParseMediaPage([]byte(body), "posts", "123")
	if !fault.OnlyPartial(err) || len(items) != 1 || items[0].MediaType != 2 || BestImageURL(items[0]) != "" || BestVideoURL(items[0]) != "" {
		t.Fatal("partial video or poster mishandled", err)
	}
	for _, field := range []string{`"feedback_required":true`, `"account_warning":{}`, `"errors":[{"message":"We suspect automated behavior on your account"}]`} {
		b := strings.Replace(body, `"errors":[{"message":"field missing"}]`, field, 1)
		if items, _, err := ParseMediaPage([]byte(b), "posts", "123"); len(items) != 0 || !fault.Is(err, fault.Verification) {
			t.Fatal("account warning ignored", err)
		}
	}
}

func TestConnectionVariationsStayTargetScoped(t *testing.T) {
	const conn = `{"edges":[{"node":{"pk":"1","media_type":1,"owner":{"id":123}}}],"page_info":{"has_next_page":false}}`
	for _, data := range []string{
		conn,
		`{"profile_timeline_connection_v3":{"wrapper":[` + conn + `]}}`,
		`{"suggested_profile_timeline_connection":` + conn + `,"profile_timeline_connection_v3":` + conn + `}`,
	} {
		items, _, err := ParseMediaPage([]byte(`{"data":`+data+`}`), "posts", "123")
		if err != nil || len(items) != 1 || items[0].PK != "1" {
			t.Fatal("renamed or wrapped timeline", err)
		}
	}
	raw := testPage(`{"username":"owner","pk":"123"}`, `{"pk":"123"}`, conn, "")
	raw = strings.Replace(raw, postsConnection, "profile_timeline_connection_v3", 1)
	p, err := parseProfilePage([]byte(raw), "owner")
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := mediaPage(p.timeline, "posts", p.profile, p.timelineErr)
	if err != nil || len(items) != 1 {
		t.Fatal("embedded compatibility path", err)
	}
}
