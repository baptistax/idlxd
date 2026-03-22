package instagram

import "testing"

func TestParseProfileUserIDFromLoggingPageID(t *testing.T) {
	t.Parallel()

	body := []byte(`{"logging_page_id":"profilePage_987654321"}`)
	if got := parseProfileUserID(body); got != "987654321" {
		t.Fatalf("unexpected user id: %q", got)
	}
}

func TestParseProfileUserIDFallsBackToProfileID(t *testing.T) {
	t.Parallel()

	body := []byte(`<script>window.__data={"profile_id":"1234567890"}</script>`)
	if got := parseProfileUserID(body); got != "1234567890" {
		t.Fatalf("unexpected user id: %q", got)
	}
}

func TestParseProfileUserIDReturnsEmptyWhenMissing(t *testing.T) {
	t.Parallel()

	if got := parseProfileUserID([]byte("<html></html>")); got != "" {
		t.Fatalf("expected empty user id, got %q", got)
	}
}
