package instagram

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

// IDL_TEST_HAR is optional. Stream entries individually and retain only
// normalized grid identities and selected item responses. Never replay requests.
func TestCapturedReelHydration(t *testing.T) {
	path := os.Getenv("IDL_TEST_HAR")
	if path == "" {
		t.Skip("local HAR not selected")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("cannot open capture")
	}
	defer f.Close()
	d := json.NewDecoder(f)
	for {
		token, err := d.Token()
		if err != nil {
			t.Fatal("capture has no entries")
		}
		if token == "entries" {
			break
		}
	}
	if token, _ := d.Token(); token != json.Delim('[') {
		t.Fatal("invalid HAR entries")
	}
	grid := map[string]Media{}
	details := map[string][]byte{}
	var pages, listings, withoutVideo int
	for d.More() {
		var entry struct {
			Request struct {
				URL      string
				PostData struct{ Text string }
			}
			Response struct {
				Status  int
				Content struct{ Text, Encoding string }
			}
		}
		if err := d.Decode(&entry); err != nil {
			t.Fatal("invalid HAR entry")
		}
		u, err := url.Parse(entry.Request.URL)
		if err != nil || u.Hostname() != "www.instagram.com" || entry.Response.Status != 200 {
			continue
		}
		infoPK, info := strings.CutPrefix(u.Path, "/api/v1/media/")
		infoPK, suffix := strings.CutSuffix(infoPK, "/info/")
		info = info && suffix && !strings.Contains(infoPK, "/")
		if !info && u.Path != "/api/graphql" && u.Path != "/graphql/query" {
			continue
		}
		params, _ := url.ParseQuery(entry.Request.PostData.Text)
		doc := params.Get("doc_id")
		if !info && doc != reelsFirstDoc && doc != reelsNextDoc {
			continue
		}
		content := entry.Response.Content
		body := []byte(content.Text)
		if content.Encoding == "base64" {
			body, err = base64.StdEncoding.DecodeString(content.Text)
			if err != nil {
				t.Fatal("invalid captured encoding")
			}
		}
		if info {
			details[infoPK] = body
			continue
		}
		var vars struct {
			Data struct {
				Target string `json:"target_user_id"`
			}
		}
		if json.Unmarshal([]byte(params.Get("variables")), &vars) != nil || vars.Data.Target == "" {
			t.Fatal("missing owner on captured request")
		}
		items, _, err := ParseMediaPage(body, "reels", vars.Data.Target)
		if err != nil {
			t.Fatal("captured reels page failed structural validation", err)
		}
		pages++
		for _, m := range items {
			grid[m.PK] = m
			listings++
			if BestVideoURL(m) == "" {
				withoutVideo++
				if BestImageURL(m) != "" {
					t.Fatal("poster selected for a video")
				}
			}
		}
	}
	matched := 0
	for pk, m := range grid {
		body, ok := details[pk]
		if !ok {
			continue
		}
		full, err := parseMediaInfo(body, Profile{Username: m.User.Username, UserID: m.User.PK}, pk, m.Code)
		if err != nil || !full.IsReel() || full.MediaType != 2 || BestVideoURL(full) == "" {
			t.Fatal("captured item detail did not yield a matching video", err)
		}
		matched++
	}
	if pages == 0 || withoutVideo == 0 || matched == 0 {
		t.Fatal("missing reel listing or matched detail coverage")
	}
	t.Logf("%d grid pages, %d reel rows, %d without video, %d unique reels, %d matched video details", pages, listings, withoutVideo, len(grid), matched)
}
