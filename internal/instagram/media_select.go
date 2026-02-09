package instagram

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"
)

// BestImageURLs returns a list of URLs to try for the best-quality image.
// The list is ordered by resolution (width*height, descending). For each candidate, a "JPEG-normalized"
// URL is tried first (when applicable), followed by the original URL.
func BestImageURLs(m Media) []string {
	type scored struct {
		url   string
		score int
		jpeg  bool
	}

	cands := make([]scored, 0, len(m.ImageVersions2.Candidates))
	for _, c := range m.ImageVersions2.Candidates {
		u := strings.TrimSpace(c.URL)
		if u == "" {
			continue
		}
		s := c.Width * c.Height
		cands = append(cands, scored{url: u, score: s, jpeg: looksLikeJPEGURL(u)})
	}
	if len(cands) == 0 {
		best := strings.TrimSpace(BestImageURL(m))
		if best == "" {
			return nil
		}
		cands = append(cands, scored{url: best, score: 0, jpeg: looksLikeJPEGURL(best)})
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].jpeg != cands[j].jpeg {
			return cands[i].jpeg
		}
		return cands[i].url < cands[j].url
	})

	out := make([]string, 0, len(cands)*2)
	maxAttempts := 10
	attempts := 0
	for _, c := range cands {
		if attempts >= maxAttempts {
			break
		}
		if u := NormalizeImageURLToJPEG(c.url); strings.TrimSpace(u) != "" {
			out = append(out, u)
			attempts++
			if attempts >= maxAttempts {
				break
			}
		}
		out = append(out, c.url)
		attempts++
	}

	// De-duplicate while preserving order.
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(out))
	for _, u := range out {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		uniq = append(uniq, u)
	}
	return uniq
}

// NormalizeImageURLToJPEG attempts to rewrite an Instagram CDN image URL to a JPEG variant.
// It preserves query parameters and tries common Instagram/Facebook CDN patterns:
// - Replace any "dst-webp" token inside the "stp" query param with "dst-jpg".
// - Replace format=webp with format=jpg (when present).
// - If the path ends with .webp, replace the path extension with .jpg.
func NormalizeImageURLToJPEG(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" {
		return raw
	}
	lowerPath := strings.ToLower(u.Path)
	if strings.HasSuffix(lowerPath, ".jpg") || strings.HasSuffix(lowerPath, ".jpeg") {
		return rewriteQueryForJPEG(u)
	}
	if strings.HasSuffix(lowerPath, ".webp") {
		ext := path.Ext(u.Path)
		if ext != "" {
			u.Path = strings.TrimSuffix(u.Path, ext) + ".jpg"
		}
	}
	return rewriteQueryForJPEG(u)
}

func rewriteQueryForJPEG(u *url.URL) string {
	q := u.Query()
	if stp := q.Get("stp"); stp != "" {
		// Common patterns: dst-webp_e35, dst-webp, ...
		q.Set("stp", strings.ReplaceAll(stp, "dst-webp", "dst-jpg"))
	}
	if f := q.Get("format"); strings.EqualFold(f, "webp") {
		q.Set("format", "jpg")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func looksLikeJPEGURL(raw string) bool {
	u, err := url.Parse(raw)
	if err == nil {
		p := strings.ToLower(strings.TrimSpace(u.Path))
		if strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".jpeg") {
			return true
		}
		if stp := strings.ToLower(u.Query().Get("stp")); strings.Contains(stp, "dst-jpg") {
			return true
		}
		if strings.EqualFold(u.Query().Get("format"), "jpg") || strings.EqualFold(u.Query().Get("format"), "jpeg") {
			return true
		}
	}
	l := strings.ToLower(raw)
	return strings.Contains(l, "dst-jpg") || strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".jpeg")
}

func BestImageURL(m Media) string {
	best := ""
	bestScore := 0
	for _, c := range m.ImageVersions2.Candidates {
		if c.URL == "" {
			continue
		}
		score := c.Width * c.Height
		if score > bestScore {
			bestScore = score
			best = c.URL
		}
	}
	if best != "" {
		return best
	}
	if len(m.ImageVersions2.Candidates) > 0 {
		return m.ImageVersions2.Candidates[0].URL
	}
	return ""
}

func BestVideoURL(m Media) string {
	if dash := strings.TrimSpace(m.VideoDashManifest); dash != "" {
		if u := bestFromDash(dash); u != "" {
			return u
		}
	}

	best := ""
	bestScore := 0
	for _, c := range m.VideoVersions {
		if c.URL == "" {
			continue
		}
		score := c.Width * c.Height
		if score > bestScore {
			bestScore = score
			best = c.URL
		}
	}
	if best != "" {
		return best
	}
	if len(m.VideoVersions) > 0 {
		return m.VideoVersions[0].URL
	}
	return ""
}

type mpd struct {
	Periods []period `xml:"Period"`
}

type period struct {
	AdaptationSets []adaptationSet `xml:"AdaptationSet"`
}

type adaptationSet struct {
	MimeType        string           `xml:"mimeType,attr"`
	ContentType     string           `xml:"contentType,attr"`
	Representations []representation `xml:"Representation"`
}

type representation struct {
	Width     int    `xml:"width,attr"`
	Height    int    `xml:"height,attr"`
	Bandwidth int    `xml:"bandwidth,attr"`
	BaseURL   string `xml:"BaseURL"`
}

func bestFromDash(manifest string) string {
	b := bytes.NewBufferString(manifest)
	dec := xml.NewDecoder(b)
	var doc mpd
	if err := dec.Decode(&doc); err != nil {
		return ""
	}

	var reps []representation
	for _, p := range doc.Periods {
		for _, a := range p.AdaptationSets {
			ct := strings.ToLower(strings.TrimSpace(a.ContentType))
			mt := strings.ToLower(strings.TrimSpace(a.MimeType))
			if ct != "" && ct != "video" {
				continue
			}
			if mt != "" && !strings.Contains(mt, "video") {
				continue
			}
			reps = append(reps, a.Representations...)
		}
	}

	if len(reps) == 0 {
		return ""
	}

	reps = filterMP4(reps)
	if len(reps) == 0 {
		return ""
	}

	sort.SliceStable(reps, func(i, j int) bool {
		if reps[i].Height != reps[j].Height {
			return reps[i].Height > reps[j].Height
		}
		if reps[i].Width != reps[j].Width {
			return reps[i].Width > reps[j].Width
		}
		return reps[i].Bandwidth > reps[j].Bandwidth
	})

	u := strings.TrimSpace(reps[0].BaseURL)
	if u == "" {
		return ""
	}
	return u
}

func filterMP4(in []representation) []representation {
	out := make([]representation, 0, len(in))
	for _, r := range in {
		u := strings.TrimSpace(r.BaseURL)
		if u == "" {
			continue
		}
		lu := strings.ToLower(u)
		if strings.Contains(lu, ".mp4") || strings.Contains(lu, "mime=video") {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var _ = errors.New
