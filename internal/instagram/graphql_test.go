package instagram

import "testing"

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
