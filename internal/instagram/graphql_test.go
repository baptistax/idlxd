package instagram

import (
	"encoding/json"
	"testing"

	"github.com/baptistax/idl/internal/fault"
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
	if !fault.Is(err, fault.RateLimited) {
		t.Fatalf("unexpected error: %v", err)
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

func TestTimelineFieldErrors(t *testing.T) {
	path := []any{postsConnection, "edges", json.Number("0"), "node", "location", "profile_pic_url"}
	for _, tc := range []struct {
		name string
		errs []any
		want fault.Kind
	}{
		{"location avatar", []any{object{"path": path}}, ""},
		{"also missing media", []any{object{"path": path}, object{"message": "another field failed"}}, fault.Partial},
		{"denied", []any{object{"path": path, "message": "access denied"}}, fault.Permission},
		{"expired", []any{object{"path": path, "requires_reauth": true}}, fault.Authentication},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := envelopeError(object{"data": object{}, "errors": tc.errs})
			if tc.want == "" && err != nil || tc.want != "" && !fault.Is(err, tc.want) {
				t.Fatal("incorrect field error classification", err)
			}
		})
	}
}
