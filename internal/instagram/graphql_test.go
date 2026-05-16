package instagram

import (
	"encoding/json"
	"testing"
)

func TestDecodeGraphQLResponseReturnsInstagramErrors(t *testing.T) {
	t.Parallel()

	body := []byte(`{"message":"Please wait a few minutes before you try again.","status":"fail"}`)
	var out struct {
		Status string `json:"status"`
	}

	err := decodeGraphQLResponse(body, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "Instagram error: Please wait a few minutes before you try again." {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestDecodeGraphQLResponseDecodesSuccessfulPayload(t *testing.T) {
	t.Parallel()

	body := []byte(`{"status":"ok","data":{"value":42}}`)
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Value int `json:"value"`
		} `json:"data"`
	}

	if err := decodeGraphQLResponse(body, &out); err != nil {
		t.Fatalf("decodeGraphQLResponse: %v", err)
	}
	if out.Status != "ok" || out.Data.Value != 42 {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestDecodeGraphQLResponseAcceptsJavaScriptPrefix(t *testing.T) {
	t.Parallel()

	body := []byte(`for (;;);{"status":"ok","data":{"value":42}}`)
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Value int `json:"value"`
		} `json:"data"`
	}

	if err := decodeGraphQLResponse(body, &out); err != nil {
		t.Fatalf("decodeGraphQLResponse: %v", err)
	}
	if out.Data.Value != 42 {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestReelsResponseParsesMediaAndPageInfo(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"data":{
			"xdt_api__v1__clips__user__connection_v2":{
				"edges":[
					{"node":{"media":{"pk":"9001","id":"9001_123","code":"Cde","media_type":2,"product_type":"clips"}}},
					{"node":{"media":{"pk":"9002","id":"9002_123","code":"Def","media_type":2,"product_type":"clips"}}}
				],
				"page_info":{"end_cursor":"CURSOR","has_next_page":true}
			}
		}
	}`)

	var out reelsResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal reels payload: %v", err)
	}

	if got := len(out.Data.Connection.Edges); got != 2 {
		t.Fatalf("unexpected edges length: %d", got)
	}
	if got := out.Data.Connection.Edges[0].Node.Media.PK; got != "9001" {
		t.Fatalf("unexpected first media pk: %q", got)
	}
	if got := out.Data.Connection.PageInfo.EndCursor; got != "CURSOR" {
		t.Fatalf("unexpected end cursor: %q", got)
	}
	if !out.Data.Connection.PageInfo.HasNextPage {
		t.Fatal("expected has_next_page")
	}
}

func TestReelsResponseHandlesEmptyResponse(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"data":{
			"xdt_api__v1__clips__user__connection_v2":{
				"edges":[],
				"page_info":{"end_cursor":"","has_next_page":false}
			}
		}
	}`)

	var out reelsResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal reels payload: %v", err)
	}
	if len(out.Data.Connection.Edges) != 0 {
		t.Fatalf("unexpected edge count: %d", len(out.Data.Connection.Edges))
	}
	if out.Data.Connection.PageInfo.HasNextPage {
		t.Fatal("expected no next page")
	}
}

func TestMediaInfoResponseParsesHydratedMedia(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"items":[
			{
				"pk":"1453280384",
				"id":"1453280384_999",
				"code":"ABC123",
				"taken_at":1710000000,
				"media_type":2,
				"product_type":"clips",
				"video_versions":[{"url":"https://example.test/video.mp4","width":720,"height":1280}],
				"video_dash_manifest":"<MPD></MPD>",
				"carousel_media":[
					{"pk":"1453280384_1","id":"1453280384_1","media_type":1,"product_type":"feed"}
				]
			}
		]
	}`)

	var out mediaInfoResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal media info payload: %v", err)
	}
	if got := len(out.Items); got != 1 {
		t.Fatalf("unexpected items length: %d", got)
	}

	m := out.Items[0]
	if m.PK != "1453280384" || m.ID != "1453280384_999" || m.Code != "ABC123" {
		t.Fatalf("unexpected media identity: %+v", m)
	}
	if m.TakenAt != 1710000000 || m.MediaType != 2 || m.ProductType != "clips" {
		t.Fatalf("unexpected media metadata: %+v", m)
	}
	if got := BestVideoURL(m); got != "https://example.test/video.mp4" {
		t.Fatalf("unexpected best video url: %q", got)
	}
	if got := len(m.CarouselMedia); got != 1 {
		t.Fatalf("unexpected carousel length: %d", got)
	}
}
