package instagram

import (
	"testing"
)

func TestOriginalAndCapturedReelsVariants(t *testing.T) {
	connection := `{"edges":[{"node":{"media":{"pk":"9001","id":"9001_123","user":{"pk":"123"},"media_type":2,"product_type":"clips"}}}],"page_info":{"end_cursor":"next","has_next_page":true}}`
	for _, body := range []string{
		`{"data":{"xdt_api__v1__clips__user__connection_v2":` + connection + `}}`,
		`{"data":{"fetch__XDTUserDict":{"id":123,"clips_connection":` + connection + `}}}`,
	} {
		items, page, err := ParseMediaPage([]byte(body), "reels", "123")
		if err != nil || len(items) != 1 || items[0].PK != "9001" || page.EndCursor != "next" || !page.HasNextPage {
			t.Fatalf("items=%+v page=%+v err=%v", items, page, err)
		}
	}
}
