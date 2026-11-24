package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

var ErrProfileNotFound = errors.New("profile not found")

func (c *Client) FetchProfile(ctx context.Context, username string) (Profile, error) {
	username, err := models.NormalizeUsername(username)
	if err != nil {
		return Profile{}, err
	}
	var raw json.RawMessage
	if err := c.get(ctx, c.accountID, profileFields, "", &raw); err != nil {
		return Profile{}, err
	}
	p, err := ParseProfile(raw, username)
	if err != nil {
		return Profile{}, err
	}
	if p.UserID != c.accountID {
		return Profile{}, fault.New(fault.Permission, "profile", "configured account ID does not match returned account")
	}
	p.Provenance = "graph-facebook/" + GraphVersion
	c.profile = p
	return p, nil
}

// ParseProfile accepts explicit account-object locations only. HTML markers
// alone are insufficient identity evidence and never authorize live access.
func ParseProfile(body []byte, requested string) (Profile, error) {
	username, err := models.NormalizeUsername(requested)
	if err != nil {
		return Profile{}, err
	}
	chunks := [][]byte{body}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		chunks = nil
		for _, m := range scriptContentPattern.FindAllSubmatch(body, -1) {
			chunks = append(chunks, m[1])
		}
	}
	var result *Profile
	for _, chunk := range chunks {
		var root map[string]json.RawMessage
		if err := decodeGraphQLResponse(chunk, &root); err != nil {
			return Profile{}, err
		}
		obj := root
		if data, ok := root["data"]; ok {
			var d map[string]json.RawMessage
			if json.Unmarshal(data, &d) != nil {
				return Profile{}, fault.New(fault.Schema, "profile", "invalid data container")
			}
			if json.Unmarshal(d["user"], &obj) != nil {
				continue
			}
		}
		var got string
		if json.Unmarshal(obj["username"], &got) != nil {
			continue
		}
		normalized, err := models.NormalizeUsername(got)
		if err != nil || normalized != username {
			return Profile{}, fault.New(fault.Permission, "profile", "returned account does not match requested username")
		}
		id, pk := exactID(obj["id"]), exactID(obj["pk"])
		if id == "" {
			id = pk
		} else if pk != "" && pk != id {
			return Profile{}, fault.New(fault.Schema, "profile", "conflicting account identifiers")
		}
		if id == "" {
			return Profile{}, fault.New(fault.Schema, "profile", "account object has no valid ID")
		}
		p := Profile{Username: username, UserID: id, Provenance: webVersion}
		if raw, ok := obj["is_private"]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &p.IsPrivate) != nil {
				return Profile{}, fault.New(fault.Schema, "profile", "invalid privacy flag")
			}
			p.PrivateKnown = true
		}
		if raw, ok := obj["media_count"]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &p.PostsCount) != nil || p.PostsCount < 0 {
				return Profile{}, fault.New(fault.Schema, "profile", "invalid media count")
			}
			p.PostsCountKnown = true
		} else if raw, ok := obj["edge_owner_to_timeline_media"]; ok {
			var t struct {
				Count *int `json:"count"`
			}
			if json.Unmarshal(raw, &t) != nil {
				return Profile{}, fault.New(fault.Schema, "profile", "invalid timeline")
			}
			if t.Count != nil && *t.Count >= 0 {
				p.PostsCount = *t.Count
				p.PostsCountKnown = true
			}
		}
		if result != nil && result.UserID != p.UserID {
			return Profile{}, fault.New(fault.Schema, "profile", "ambiguous account objects")
		}
		result = &p
	}
	if result == nil {
		return Profile{}, fault.New(fault.Schema, "profile", "missing matching account object; markers alone are insufficient")
	}
	return *result, nil
}

var scriptContentPattern = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func exactID(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
	}
	if !models.IsNumericID(s) {
		return ""
	}
	return s
}
