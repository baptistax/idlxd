package instagram

import (
	"context"
	"github.com/baptistax/idl/internal/fault"
)

// Pagination is per traversal/category, not cross-category deduplication or a
// resume manifest. Invalid pages are rejected before their media are written.
type Pagination struct {
	cursors map[string]bool
	ids     map[string]bool
	done    bool
}

func NewPagination() *Pagination {
	return &Pagination{cursors: map[string]bool{}, ids: map[string]bool{}}
}
func (p *Pagination) Accept(ctx context.Context, ids []string, info PageInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.done {
		return fault.New(fault.Schema, "pagination", "page after explicit termination")
	}
	if info.HasNextPage && (info.EndCursor == "" || p.cursors[info.EndCursor]) {
		return fault.New(fault.Schema, "pagination", "missing or repeated continuation cursor")
	}
	local := map[string]bool{}
	for _, id := range ids {
		if id == "" || p.ids[id] || local[id] {
			return fault.New(fault.Schema, "pagination", "missing or duplicate item ID")
		}
		local[id] = true
	}
	for id := range local {
		p.ids[id] = true
	}
	if info.HasNextPage {
		p.cursors[info.EndCursor] = true
	} else {
		p.done = true
	}
	return nil
}

func MediaIDs(items []Media) []string {
	ids := make([]string, 0, len(items))
	for _, m := range items {
		id := m.PK
		if id == "" {
			id = m.ID
		}
		ids = append(ids, id)
	}
	return ids
}
