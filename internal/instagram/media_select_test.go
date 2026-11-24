package instagram

import "testing"

func TestOriginalRepresentationsRanked(t *testing.T) {
	full := "https://media.invalid/full.webp?b=2&a=1"
	rows := []Candidate{{URL: "thumb", Width: 1, Height: 1}, {URL: "invalid", Width: -1, Height: -1}, {URL: full, Width: 640, Height: 480}, {URL: full, Width: 320, Height: 240}}
	m := Media{ImageVersions2: ImageVersions2{Candidates: rows}, VideoVersions: rows}
	urls := BestImageURLs(m)
	if len(urls) != 2 || urls[0] != full || BestVideoURL(m) != full {
		t.Fatal("lost ranking, deduplication or original signature")
	}
}
func TestGraphImageURLIsNotRewritten(t *testing.T) {
	url := "https://media.invalid/photo.webp?format=webp&stp=dst-webp&sig=synthetic"
	m := Media{Provenance: "graph-facebook/v27.0", ImageVersions2: ImageVersions2{Candidates: []Candidate{{URL: url}}}}
	urls := BestImageURLs(m)
	if len(urls) != 1 || urls[0] != url {
		t.Fatalf("Graph URL modified: %v", urls)
	}
}
