package instagram

import (
	"bytes"
	"encoding/xml"
	"errors"
	"sort"
	"strings"
)

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
