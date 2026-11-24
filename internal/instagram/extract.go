package instagram

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

func webResponse(body []byte) (object, error) {
	root, err := readObject(body)
	if err != nil {
		return nil, err
	}
	err = envelopeError(root)
	if data, ok := root["data"].(object); ok {
		err = errors.Join(err, envelopeError(data))
	}
	if err != nil && !fault.OnlyPartial(err) {
		return nil, err
	}
	return root, err
}

func relationship(root any, p Profile, names ...string) (any, error) {
	var result any
	found := false
	err := findFields(root, names, func(_ string, v any, parent object) error {
		if found {
			return schema("ambiguous connections")
		}
		if parent["pk"] != nil || parent["id"] != nil {
			if err := checkOwner(parent, p); err != nil {
				return err
			}
		}
		found = true
		result = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found || result == nil {
		return nil, schema("missing supported connection")
	}
	return result, nil
}

func connection(v any) ([]any, PageInfo, error) {
	o, _ := v.(object)
	if o["edges"] == nil && o["page_info"] == nil {
		var inner object
		err := findFields(v, []string{"page_info"}, func(_ string, _ any, parent object) error {
			if inner != nil {
				return schema("ambiguous nested connection")
			}
			inner = parent
			return nil
		})
		if err != nil {
			return nil, PageInfo{}, err
		}
		o = inner
	}
	edges, ok := o["edges"].([]any)
	meta, _ := o["page_info"].(object)
	next, hasFlag := meta["has_next_page"].(bool)
	cursor := str(meta["end_cursor"])
	if !ok || !hasFlag || next && strings.TrimSpace(cursor) == "" {
		return nil, PageInfo{}, schema("incomplete edges or pagination")
	}
	return edges, PageInfo{HasNextPage: next, EndCursor: cursor}, nil
}

// Search only inside one node of a verified relationship. Stop at each media
// boundary; children and variants have their own parsers. A wrapper containing
// two candidate media objects has no unambiguous interpretation.
func edgeMedia(v any) (object, bool, error) {
	var result object
	skipped := false
	var visit func(any) error
	visit = func(v any) error {
		switch n := v.(type) {
		case []any:
			for _, el := range n {
				if err := visit(el); err != nil {
					return err
				}
			}
		case object:
			if promoted(n) {
				skipped = true
				return nil
			}
			t := str(n["__typename"])
			if n["media_type"] != nil || t == "GraphImage" || t == "GraphSidecar" || t == "GraphVideo" {
				if result != nil {
					return schema("ambiguous media node")
				}
				result = n
				return nil
			}
			for _, k := range slices.Sorted(maps.Keys(n)) {
				if excludedBranch(k) {
					skipped = skipped || excludedFeed(k)
					continue
				}
				switch k {
				case "edges", "user", "owner", "items", "carousel_media", "image_versions2", "video_versions":
					continue
				}
				if err := visit(n[k]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(v); err != nil {
		return nil, false, err
	}
	if result == nil && !skipped {
		return nil, false, schema("missing media node")
	}
	return result, result == nil, nil
}

func parseItems(rows []any, edges bool, p Profile, partial bool) ([]Media, error) {
	if !models.IsNumericID(p.UserID) {
		return nil, schema("expected verified owner ID")
	}
	var items []Media
	seen := map[string]bool{}
	for _, v := range rows {
		if edges {
			edge, _ := v.(object)
			if promoted(edge) {
				continue
			}
			v = edge["node"]
		}
		if v == nil && partial {
			continue
		}
		node, skip, err := edgeMedia(v)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if edges && foreignCollaboration(node, p) {
			continue
		}
		m, err := normalizeWebMedia(node, p, nil)
		if err != nil {
			return nil, err
		}
		for _, id := range append(MediaIDs(m.CarouselMedia), m.PK) {
			if seen[id] {
				return nil, schema("duplicate media identity")
			}
			seen[id] = true
		}
		items = append(items, m)
	}
	return items, nil
}

// Instagram includes a coauthored post on each collaborator's timeline.
// The target's explicit membership explains the foreign row; only the
// primary owner's copy may enter the download list.
func foreignCollaboration(o object, p Profile) bool {
	author, _ := o["user"].(object)
	id, err := accountID(author)
	if err != nil || id == p.UserID {
		return false
	}
	if v := o["owner"]; v != nil {
		owner, _ := v.(object)
		if err := checkOwner(owner, Profile{UserID: id}); err != nil {
			return false
		}
	}
	list, _ := o["coauthor_producers"].([]any)
	for _, v := range list {
		c, _ := v.(object)
		if checkOwner(c, p) == nil {
			return true
		}
	}
	return false
}

func ParseMediaPage(body []byte, category, ownerID string) ([]Media, PageInfo, error) {
	root, err := webResponse(body)
	if root == nil {
		return nil, PageInfo{}, err
	}
	return mediaPage(root, category, Profile{UserID: ownerID}, err)
}

func mediaPage(root object, category string, p Profile, partial error) ([]Media, PageInfo, error) {
	var names []string
	switch category {
	case "posts":
		names = []string{postsConnection, "edge_owner_to_timeline_media"}
	case "reels":
		names = []string{reelsConnection, "clips_connection"}
	default:
		return nil, PageInfo{}, fault.New(fault.Unsupported, "media", "unknown category")
	}
	var rows []any
	var info PageInfo
	var err error
	edges := true
	if list, ok := root["items"].([]any); ok {
		next, ok := root["more_available"].(bool)
		info = PageInfo{HasNextPage: next, EndCursor: str(root["next_max_id"])}
		if !ok || next && info.EndCursor == "" {
			return nil, PageInfo{}, schema("missing feed pagination")
		}
		edges = false
		rows = list
	} else {
		var v any
		// Some category-specific responses return the connection directly.
		data, _ := root["data"].(object)
		if data["edges"] != nil && data["page_info"] != nil {
			v = data
		} else {
			v, err = relationship(root, p, names...)
			if err != nil {
				return nil, PageInfo{}, errors.Join(partial, err)
			}
		}
		rows, info, err = connection(v)
		if err != nil {
			return nil, PageInfo{}, errors.Join(partial, err)
		}
	}
	items, err := parseItems(rows, edges, p, partial != nil)
	if err != nil {
		return nil, PageInfo{}, errors.Join(partial, err)
	}
	for i := range items {
		if category == "reels" {
			if items[i].MediaType != 2 || items[i].ProductType != "" && !isReel(items[i].ProductType) {
				return nil, PageInfo{}, schema("non-reel in clips connection")
			}
			items[i].ProductType = "clips"
		} else {
			switch items[i].ProductType {
			case "", "feed", "igtv", "clips", "reels", "carousel_container":
			default:
				return nil, PageInfo{}, schema("unexpected timeline product type")
			}
		}
	}
	return items, info, partial
}

func ParseStories(body []byte, ownerID string) ([]Media, error) {
	root, err := webResponse(body)
	if root == nil {
		return nil, err
	}
	return stories(root, Profile{UserID: ownerID}, err)
}

func stories(root object, p Profile, partial error) ([]Media, error) {
	v, err := relationship(root, p, storiesContainer, "reels_media")
	if err != nil {
		return nil, err
	}
	if container, ok := v.(object); ok {
		v, err = relationship(container, p, "reels_media")
		if err != nil {
			return nil, err
		}
	}
	groups, ok := v.([]any)
	if !ok || !models.IsNumericID(p.UserID) {
		return nil, schema("invalid story collection")
	}
	if len(groups) == 0 {
		return nil, partial
	}
	if len(groups) != 1 {
		return nil, schema("unexpected number of story collections")
	}
	group, _ := groups[0].(object)
	if numberText(group["id"]) != p.UserID {
		return nil, mismatch("story collection owner mismatch")
	}
	if t := group["reel_type"]; t != nil && t != "user_reel" {
		return nil, schema("unexpected reel type")
	}
	if u := group["user"]; u != nil {
		o, _ := u.(object)
		if err := checkOwner(o, p); err != nil {
			return nil, err
		}
	}
	items, ok := group["items"].([]any)
	if !ok {
		return nil, schema("missing story items")
	}
	media, err := storyItems(items, p, partial != nil)
	return media, errors.Join(partial, err)
}

func storyItems(rows []any, p Profile, partial bool) ([]Media, error) {
	items, err := parseItems(rows, false, p, partial)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		if m.MediaType == 8 || m.ProductType != "" && m.ProductType != "story" {
			return nil, schema("unexpected story media type")
		}
	}
	return items, nil
}

func highlightID(group object, p Profile) (string, error) {
	id := str(group["id"])
	value, ok := strings.CutPrefix(id, "highlight:")
	if !ok || !models.IsNumericID(value) {
		return "", schema("invalid highlight identifier")
	}
	if t := group["reel_type"]; t != nil && t != "highlight_reel" {
		return "", schema("unexpected highlight type")
	}
	u, _ := group["user"].(object)
	return id, checkOwner(u, p)
}

func highlightGroups(root object, p Profile, tray bool, partial error) ([]Highlight, []HighlightMedia, PageInfo, error) {
	key := highlightsConnection
	if tray {
		key = "highlights"
	}
	v, err := relationship(root, p, key)
	if err != nil {
		return nil, nil, PageInfo{}, err
	}
	rows, info, err := connection(v)
	if err != nil {
		return nil, nil, PageInfo{}, err
	}
	var titles []Highlight
	var groups []HighlightMedia
	seen := map[string]bool{}
	for _, row := range rows {
		edge, _ := row.(object)
		o, _ := edge["node"].(object)
		if o == nil && partial != nil {
			continue
		}
		id, err := highlightID(o, p)
		if err != nil {
			return nil, nil, PageInfo{}, err
		}
		if seen[id] {
			return nil, nil, PageInfo{}, schema("duplicate highlight collection")
		}
		seen[id] = true
		if tray {
			titles = append(titles, Highlight{ID: id, Title: str(o["title"])})
			continue
		}
		items, ok := o["items"].([]any)
		if !ok {
			return nil, nil, PageInfo{}, schema("missing highlight items")
		}
		media, err := storyItems(items, p, partial != nil)
		if err != nil {
			return nil, nil, PageInfo{}, err
		}
		groups = append(groups, HighlightMedia{ID: id, Items: media})
	}
	return titles, groups, info, partial
}

func ParseHighlightsPage(body []byte, ownerID string) ([]HighlightMedia, PageInfo, error) {
	root, err := webResponse(body)
	if root == nil {
		return nil, PageInfo{}, err
	}
	_, groups, info, err := highlightGroups(root, Profile{UserID: ownerID}, false, err)
	return groups, info, err
}
