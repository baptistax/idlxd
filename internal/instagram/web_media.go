package instagram

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

func numberText(v any) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return str(v)
}

func accountID(o object) (string, error) {
	id := ""
	for _, key := range []string{"pk", "id"} {
		if v := o[key]; v != nil {
			got := numberText(v)
			if !models.IsNumericID(got) || id != "" && id != got {
				return "", schema("invalid or conflicting account identifiers")
			}
			id = got
		}
	}
	if id == "" {
		return "", schema("missing account identifier")
	}
	return id, nil
}

func checkOwner(o object, p Profile) error {
	id, err := accountID(o)
	if err != nil {
		return err
	}
	if id != p.UserID {
		return mismatch("owner does not match target")
	}
	if u := o["username"]; u != nil {
		name, err := models.NormalizeUsername(str(u))
		if err != nil || p.Username != "" && name != p.Username {
			return mismatch("owner username does not match target")
		}
	}
	return nil
}

func mediaIDs(o object, owner string) (pk, id string, err error) {
	if o["pk"] != nil {
		pk = numberText(o["pk"])
		if !models.IsNumericID(pk) {
			return "", "", schema("invalid media pk")
		}
	}
	if o["id"] != nil {
		id = numberText(o["id"])
		base, suffix, composite := strings.Cut(id, "_")
		if !models.IsNumericID(base) || pk != "" && pk != base {
			return "", "", schema("invalid or conflicting media identifiers")
		}
		if composite && suffix != owner {
			return "", "", mismatch("composite media ID has another owner")
		}
		pk = base
	}
	if pk == "" {
		return "", "", schema("missing media identity")
	}
	if id == "" {
		id = pk
	}
	return pk, id, nil
}

func webMediaType(o object) (int, error) {
	kind := 0
	switch str(o["__typename"]) {
	case "GraphImage":
		kind = 1
	case "GraphVideo":
		kind = 2
	case "GraphSidecar":
		kind = 8
	}
	if o["media_type"] != nil {
		t, err := strconv.Atoi(numberText(o["media_type"]))
		if err != nil || t != 1 && t != 2 && t != 8 {
			return 0, fault.New(fault.Unsupported, "media", "unknown media type")
		}
		if kind != 0 && kind != t {
			return 0, schema("contradictory media type")
		}
		kind = t
	}
	if kind == 0 {
		return 0, schema("missing media type")
	}
	if v := o["is_video"]; v != nil && v != (kind == 2) || isReel(str(o["product_type"])) && kind != 2 {
		return 0, schema("contradictory video classification")
	}
	return kind, nil
}
func isReel(product string) bool { return product == "clips" || product == "reels" }

func mediaURL(v any) string {
	s := str(v)
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return ""
	}
	return s
}

func variants(v any, legacy bool) []Candidate {
	rows, _ := v.([]any)
	var out []Candidate
	for _, row := range rows {
		c, _ := row.(object)
		raw, w, h := c["url"], c["width"], c["height"]
		if legacy {
			raw, w, h = c["src"], c["config_width"], c["config_height"]
		}
		candidate := Candidate{URL: mediaURL(raw)}
		if candidate.URL == "" {
			continue
		}
		if w != nil || h != nil {
			width, e1 := strconv.Atoi(numberText(w))
			height, e2 := strconv.Atoi(numberText(h))
			if e1 != nil || e2 != nil || width <= 0 || height <= 0 || width > 65535 || height > 65535 {
				continue
			}
			candidate.Width, candidate.Height = width, height
		}
		out = append(out, candidate)
	}
	return out
}

func normalizeWebMedia(o object, p Profile, parent *Media) (Media, error) {
	if o["can_viewer_download"] == false || o["is_download_disabled"] == true || o["is_media_unavailable"] == true {
		return Media{}, fault.New(fault.Permission, "media", "provider restricted this item")
	}
	if promoted(o) {
		return Media{}, fault.New(fault.Unsupported, "media", "promoted item")
	}
	owned := false
	for _, key := range []string{"user", "owner"} {
		if v := o[key]; v != nil {
			obj, _ := v.(object)
			if err := checkOwner(obj, p); err != nil {
				return Media{}, err
			}
			owned = true
		}
	}
	if !owned && parent == nil {
		return Media{}, schema("missing media owner")
	}
	pk, id, err := mediaIDs(o, p.UserID)
	if err != nil {
		return Media{}, err
	}
	kind, err := webMediaType(o)
	if err != nil {
		return Media{}, err
	}
	m := Media{PK: pk, ID: id, MediaType: kind, ProductType: str(o["product_type"]), Code: str(o["code"]), User: IGUser{PK: p.UserID, Username: p.Username}, Provenance: webVersion}
	if m.Code == "" {
		m.Code = str(o["shortcode"])
	}
	if parent != nil {
		if kind == 8 || pk == parent.PK {
			return Media{}, schema("invalid nested album")
		}
		if v := o["carousel_parent_id"]; v != nil && numberText(v) != parent.PK && numberText(v) != parent.ID {
			return Media{}, schema("carousel parent mismatch")
		}
		m.TakenAt = parent.TakenAt
	}
	taken := o["taken_at"]
	if taken == nil {
		taken = o["taken_at_timestamp"]
	}
	if taken != nil {
		m.TakenAt, err = strconv.ParseInt(numberText(taken), 10, 64)
		if err != nil || m.TakenAt < 0 {
			return Media{}, schema("invalid timestamp")
		}
	}
	if kind != 8 && (o["carousel_media"] != nil || o["edge_sidecar_to_children"] != nil) {
		if a, ok := o["carousel_media"].([]any); !ok || len(a) != 0 || o["edge_sidecar_to_children"] != nil {
			return Media{}, schema("children on non-album")
		}
	}
	switch kind {
	case 1:
		img, _ := o["image_versions2"].(object)
		m.ImageVersions2.Candidates = append(variants(img["candidates"], false), variants(o["display_resources"], true)...)
		if u := mediaURL(o["display_url"]); u != "" {
			m.ImageVersions2.Candidates = append(m.ImageVersions2.Candidates, Candidate{URL: u})
		}
		if videos, ok := o["video_versions"].([]any); ok && len(videos) > 0 || o["video_url"] != nil {
			return Media{}, schema("video representation on image")
		}
	case 2:
		// A poster and a DASH video-only track are not complete video files.
		m.VideoVersions = variants(o["video_versions"], false)
		if u := mediaURL(o["video_url"]); u != "" {
			m.VideoVersions = append(m.VideoVersions, Candidate{URL: u})
		}
		if len(m.VideoVersions) == 0 {
			rows, ok := o["video_versions"].([]any)
			if o["video_url"] != nil && o["video_url"] != "" || o["video_versions"] != nil && (!ok || len(rows) > 0) {
				return Media{}, schema("invalid video representation")
			}
		}
	case 8:
		children, ok := o["carousel_media"].([]any)
		if sidecar := o["edge_sidecar_to_children"]; sidecar != nil {
			if ok {
				return Media{}, schema("ambiguous carousel children")
			}
			c, _ := sidecar.(object)
			edges, _ := c["edges"].([]any)
			for _, e := range edges {
				edge, _ := e.(object)
				children = append(children, edge["node"])
			}
		}
		count := len(children)
		if count == 0 || o["carousel_media_count"] != nil && numberText(o["carousel_media_count"]) != strconv.Itoa(count) {
			return Media{}, schema("incomplete album")
		}
		m.CarouselMediaCount = &count
		seen := map[string]bool{}
		for _, child := range children {
			obj, _ := child.(object)
			c, err := normalizeWebMedia(obj, p, &m)
			if err != nil {
				return Media{}, err
			}
			if seen[c.PK] {
				return Media{}, schema("duplicate carousel child")
			}
			seen[c.PK] = true
			m.CarouselMedia = append(m.CarouselMedia, c)
		}
	}
	return m, nil
}
