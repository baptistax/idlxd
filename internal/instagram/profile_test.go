package instagram

import (
	"github.com/baptistax/idl/internal/fault"
	"strings"
	"testing"
)

func TestVerifiedProfile(t *testing.T) {
	tests := []struct {
		name, body, request, id string
		kind                    fault.Kind
		countKnown              bool
	}{
		{"string ID", `{"data":{"user":{"id":"123","username":"empty_posts","media_count":0}}}`, " @EMPTY_POSTS ", "123", "", true},
		{"numeric precision", `{"id":900719925474099312345,"username":"owner"}`, "owner", "900719925474099312345", "", false},
		{"without photo", `<html><script type="application/json">{"id":"123","username":"owner"}</script></html>`, "owner", "123", "", false},
		{"HTML profile with page markers", `<html><head><script type="application/json">{"username":"owner","id":"123","logging_page_id":"profilePage_123","profile_id":"123"}</script></head></html>`, "owner", "123", "", false},
		{"unrelated viewer", `{"data":{"viewer":{"id":"999"},"user":{"id":"123","username":"owner"}}}`, "owner", "123", "", false},
		{"nested ID", `{"data":{"user":{"username":"owner","friend":{"id":"999"}}}}`, "owner", "", fault.Schema, false},
		{"mismatch", `{"data":{"user":{"id":"123","username":"other"}}}`, "owner", "", fault.Permission, false},
		{"zero", `{"id":"0","username":"owner"}`, "owner", "", fault.Schema, false},
		{"fraction", `{"id":1.2,"username":"owner"}`, "owner", "", fault.Schema, false},
		{"exponent", `{"id":1e20,"username":"owner"}`, "owner", "", fault.Schema, false},
		{"conflicting", `{"id":"123","pk":"456","username":"owner"}`, "owner", "", fault.Schema, false},
		{"marker only", `{"logging_page_id":"profilePage_123","profile_id":"123"}`, "owner", "", fault.Schema, false},
		{"page logging only", `{"page_logging":{"params":{"page_id":"123"}}}`, "owner", "", fault.Schema, false},
		{"no username", `{"data":{"user":{"id":"123"}}}`, "owner", "", fault.Schema, false},
		{"unsafe input", `{}`, "../owner", "", fault.InvalidInput, false},
		{"invalid count", `{"id":"123","username":"owner","media_count":null}`, "owner", "", fault.Schema, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseProfile([]byte(tt.body), tt.request)
			if tt.kind != "" {
				if !fault.Is(err, tt.kind) {
					t.Fatalf("expected %s, got %+v %v", tt.kind, p, err)
				}
				return
			}
			if err != nil || p.UserID != tt.id || p.PostsCountKnown != tt.countKnown {
				t.Fatalf("got %+v, %v", p, err)
			}
		})
	}
}

func TestEnvelopeClassificationAndRedaction(t *testing.T) {
	tests := []struct {
		body string
		kind fault.Kind
	}{
		{`{"__ar":1,"error":1357004,"errorDescription":"synthetic-secret https://example.invalid/?token=synthetic-secret","errorSummary":"Failure","payload":null}`, fault.Upstream},
		{`{"status":"fail","message":"login_required synthetic-secret"}`, fault.Authentication},
		{`{"error":{"code":190,"message":"synthetic-secret"}}`, fault.Authentication},
		{`{"data":{"value":1},"errors":[{"message":"access denied synthetic-secret"}]}`, fault.Permission},
		{`{"data":{"value":1},"errors":[{"message":"challenge_required synthetic-secret"}]}`, fault.Verification},
		{`{"data":{"value":1},"errors":[{"message":"synthetic-secret"}]}`, fault.Partial},
		{`{"error":{"code":4}}`, fault.RateLimited},
		{`<html>synthetic-secret</html>`, fault.Schema},
		{`{broken`, fault.Schema}, {`null`, fault.Schema}, {`[]`, fault.Schema},
	}
	for _, tt := range tests {
		var out map[string]any
		err := decodeGraphQLResponse([]byte(tt.body), &out)
		if !fault.Is(err, tt.kind) {
			t.Fatalf("expected %s, got %v", tt.kind, err)
		}
		if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "example.invalid") {
			t.Fatal("response content leaked")
		}
		if fault.Global(err) && out != nil {
			t.Fatal("denied data exposed to caller")
		}
		if tt.kind == fault.Partial && out == nil {
			t.Fatal("partial data not decoded")
		}
	}
}
