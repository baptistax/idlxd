package instagram

import (
	"encoding/json"
	"testing"
)

func TestStoriesResponseParsesActiveStoryItems(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"data":{
			"xdt_api__v1__feed__reels_media":{
				"reels_media":[{
					"items":[
						{"pk":"story_1","media_type":1,"image_versions2":{"candidates":[{"url":"https://example.test/story.jpg","width":720,"height":1280}]}},
						{"pk":"story_2","media_type":2,"video_versions":[{"url":"https://example.test/story.mp4","width":720,"height":1280}]}
					]
				}]
			}
		}
	}`)

	var out storiesResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal stories payload: %v", err)
	}
	items := out.Data.ReelsMedia.Reels[0].Items
	if len(items) != 2 {
		t.Fatalf("unexpected stories length: %d", len(items))
	}
	if items[0].PK != "story_1" || items[1].PK != "story_2" {
		t.Fatalf("unexpected stories: %+v", items)
	}
}
