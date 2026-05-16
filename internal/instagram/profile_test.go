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

func TestProfileIDResolverSucceedsWhenPostsAreEmpty(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"data":{
			"user":{
				"id":"123456789",
				"username":"empty_posts",
				"is_private":false,
				"edge_owner_to_timeline_media":{"count":0,"edges":[]}
			}
		}
	}`)
	if got := parseProfileUserID(body); got != "123456789" {
		t.Fatalf("unexpected user id: %q", got)
	}
}

func TestProfileIDResolverSucceedsWithoutProfilePhoto(t *testing.T) {
	t.Parallel()

	body := []byte(`<html><script type="application/json">{
		"require":[["Profile",[],[],{"props":{"page_logging":{"params":{"profile_id":"555444333"}},"profile_pic_url":null}}]]
	}</script></html>`)
	if got := parseProfileUserID(body); got != "555444333" {
		t.Fatalf("unexpected user id: %q", got)
	}
}

func TestProfileIDResolverFindsPageLoggingPageID(t *testing.T) {
	t.Parallel()

	body := []byte(`{"page_logging":{"params":{"page_id":"8159641612"}}}`)
	if got := parseProfileUserID(body); got != "8159641612" {
		t.Fatalf("unexpected user id: %q", got)
	}
}

func TestProfileIDResolverIgnoresZeroID(t *testing.T) {
	t.Parallel()

	body := []byte(`{"data":{"viewer":{"id":"0"},"user":{"id":"8159641612","username":"example_user"}}}`)
	if got := parseProfileUserID(body); got != "8159641612" {
		t.Fatalf("unexpected user id: %q", got)
	}
}
