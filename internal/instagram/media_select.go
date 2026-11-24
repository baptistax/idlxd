package instagram

import (
	"slices"
	"sort"
	"strings"
)

// Preserve the representation as served, including signed query parameters.
// Source order breaks equal-resolution ties. Some complete video renditions
// have no dimensions, so rank them after candidates with measured dimensions.
func rankedURLs(candidates []Candidate) []string {
	rows := slices.Clone(candidates)
	area := func(c Candidate) int64 { return int64(c.Width) * int64(c.Height) }
	rows = slices.DeleteFunc(rows, func(c Candidate) bool {
		return strings.TrimSpace(c.URL) == "" || c.Width < 0 || c.Height < 0 || c.Width > 65535 || c.Height > 65535 || (c.Width == 0) != (c.Height == 0)
	})
	sort.SliceStable(rows, func(i, j int) bool { return area(rows[i]) > area(rows[j]) })
	var urls []string
	seen := map[string]bool{}
	for _, c := range rows {
		if !seen[c.URL] {
			urls = append(urls, c.URL)
			seen[c.URL] = true
		}
	}
	return urls
}

func BestImageURLs(m Media) []string { return rankedURLs(m.ImageVersions2.Candidates) }
func BestImageURL(m Media) string {
	if urls := BestImageURLs(m); len(urls) > 0 {
		return urls[0]
	}
	return ""
}
func BestVideoURL(m Media) string {
	if urls := rankedURLs(m.VideoVersions); len(urls) > 0 {
		return urls[0]
	}
	return ""
}
