package instagram

import (
	"github.com/baptistax/idl/internal/fault"
	"testing"
)

func TestStoriesOwnershipAndIntegrity(t *testing.T) {
	for _, tt := range []struct {
		body string
		kind fault.Kind
	}{
		{`{"data":{"xdt_api__v1__feed__reels_media":{"reels_media":[{"id":"999","items":[]}]}}}`, fault.Permission},
		{`{"data":{"xdt_api__v1__feed__reels_media":{"reels_media":[{"id":"123"}]}}}`, fault.Schema},
		{`{"data":{"xdt_api__v1__feed__reels_media":{"reels_media":[{"id":"123","items":[{"pk":"1","media_type":1,"user":{"pk":"999"}}]}]}}}`, fault.Permission},
	} {
		if _, err := ParseStories([]byte(tt.body), "123"); !fault.Is(err, tt.kind) {
			t.Fatalf("expected %s, got %v", tt.kind, err)
		}
	}
}
