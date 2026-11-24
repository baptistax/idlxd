package instagram

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/transport"
)

type object = map[string]any

func schema(detail string) error   { return fault.New(fault.Schema, "web response", detail) }
func mismatch(detail string) error { return fault.New(fault.Permission, "web identity", detail) }

// Bound the tree before allocating large subtrees. Reject duplicate keys so two
// contradictory owners or connections cannot be resolved by field order.
func readObject(body []byte) (object, error) {
	if len(body) > transport.MaxBody {
		return nil, schema("response too large")
	}
	d := json.NewDecoder(bytes.NewReader(normalizeGraphQLJSON(body)))
	d.UseNumber()
	nodes := 0
	var read func(depth int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > 80 || nodes > 500_000 {
			return nil, schema("JSON structure exceeds limits")
		}
		t, err := d.Token()
		if err != nil {
			return nil, schema("invalid JSON")
		}
		switch t {
		case json.Delim('{'):
			m := object{}
			for d.More() {
				k, err := d.Token()
				key, ok := k.(string)
				if err != nil || !ok {
					return nil, schema("invalid object key")
				}
				if _, exists := m[key]; exists {
					return nil, schema("duplicate object key")
				}
				m[key], err = read(depth + 1)
				if err != nil {
					return nil, err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return nil, schema("incomplete object")
			}
			return m, nil
		case json.Delim('['):
			var a []any
			for d.More() {
				v, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return nil, schema("incomplete array")
			}
			return a, nil
		}
		return t, nil
	}
	v, err := read(0)
	if err != nil {
		return nil, err
	}
	m, ok := v.(object)
	if !ok {
		return nil, schema("expected an object")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, schema("trailing response data")
	}
	return m, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// A recognized relationship grants context only to its own subtree. In
// particular, never cross a different feed, another media object or account
// suggestions in search of a plausible URL.
func excludedFeed(key string) bool {
	key = strings.ToLower(key)
	for _, part := range []string{"suggest", "recommend", "discover", "sponsor", "advert", "chaining"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	switch key {
	case "ad", "ads", "ad_media", "ad_items", "related_media":
		return true
	}
	return false
}

func excludedBranch(key string) bool {
	switch key {
	case "cover_media", "cover", "direct", "inbox", "threads", "comments", "caption", "tagged", "usertags":
		return true
	}
	return excludedFeed(key) || strings.Contains(key, "thumbnail") || strings.Contains(key, "profile_pic")
}

func matchesRelationship(key string, names []string) bool {
	if slices.Contains(names, key) {
		return true
	}
	// Allow versioned connection names only within the same kind of feed.
	if !strings.Contains(key, "connection") {
		return false
	}
	if slices.Contains(names, postsConnection) {
		return strings.Contains(key, "user_timeline") || strings.Contains(key, "profile_timeline")
	}
	return slices.Contains(names, reelsConnection) && (strings.Contains(key, "clips_connection") || strings.Contains(key, "clips__user__"))
}

func promoted(m object) bool {
	for _, key := range []string{"is_ad", "is_sponsored", "is_suggested", "is_recommended", "ad_id", "ad_metadata", "sponsored_label", "recommendation_context"} {
		v := m[key]
		if v != nil && v != false && v != "" && v != json.Number("0") {
			return true
		}
	}
	return false
}

func findFields(v any, names []string, visit func(string, any, object) error) error {
	switch n := v.(type) {
	case []any:
		for _, item := range n {
			if err := findFields(item, names, visit); err != nil {
				return err
			}
		}
	case object:
		if promoted(n) || n["media_type"] != nil {
			return nil
		}
		for _, key := range slices.Sorted(maps.Keys(n)) {
			switch {
			case excludedBranch(key):
				continue
			case matchesRelationship(key, names):
				if err := visit(key, n[key], n); err != nil {
					return err
				}
			case strings.HasPrefix(key, "xdt_api__"), key == "edges", key == "items", key == "reels_media", key == "viewer", key == "owner":
				continue
			default:
				if err := findFields(n[key], names, visit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
