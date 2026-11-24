package models

import (
	"strings"

	"github.com/baptistax/idl/internal/fault"
)

func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	if len(s) == 0 || len(s) > 30 || s == "." || s == ".." {
		return "", fault.New(fault.InvalidInput, "username", "expected 1 to 30 letters, digits, underscores or periods")
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return "", fault.New(fault.InvalidInput, "username", "invalid character")
		}
	}
	return s, nil
}

type Profile struct {
	Username        string
	UserID          string
	IsPrivate       bool
	PrivateKnown    bool
	PostsCount      int
	PostsCountKnown bool
	Provenance      string
}

type Capability struct {
	Supported bool
	Reason    string
}

type PageInfo struct {
	EndCursor   string `json:"end_cursor"`
	HasNextPage bool   `json:"has_next_page"`
}

type HighlightMedia struct {
	ID    string
	Items []Media
}

// IsNumericID accepts nonzero decimal IDs without rounding or integer overflow.
func IsNumericID(s string) bool {
	if s == "" {
		return false
	}
	nonzero := false
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}
