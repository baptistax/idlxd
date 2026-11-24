package instagram

import (
	"context"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

func (c *Client) FetchPostsPage(ctx context.Context, username, after string) ([]Media, PageInfo, string, error) {
	items, info, err := c.mediaPage(ctx, username, "media", after, "posts")
	return items, info, c.profile.UserID, err
}

func (c *Client) FetchReelsPage(ctx context.Context, username, userID, after string) ([]Media, PageInfo, error) {
	if err := c.authorized(username, userID); err != nil {
		return nil, PageInfo{}, err
	}
	return c.mediaPage(ctx, username, "media", after, "reels")
}

func (c *Client) FetchStories(ctx context.Context, username, userID string) ([]Media, error) {
	if err := c.authorized(username, userID); err != nil {
		return nil, err
	}
	after := ""
	pages := NewPagination()
	var all []Media
	for {
		items, info, err := c.mediaPage(ctx, username, "stories", after, "stories")
		if err != nil {
			return nil, err
		}
		if err := pages.Accept(ctx, MediaIDs(items), info); err != nil {
			return nil, err
		}
		all = append(all, items...)
		if !info.HasNextPage {
			return all, nil
		}
		after = info.EndCursor
	}
}

func (c *Client) FetchHighlightsTray(ctx context.Context, username, userID string) ([]Highlight, error) {
	if err := c.authorized(username, userID); err != nil {
		return nil, err
	}
	return nil, fault.New(fault.Unsupported, "highlights", "no permitted live highlights contract implemented")
}

func (c *Client) FetchHighlightsPage(ctx context.Context, username string, ids []string, after string, first int) ([]HighlightMedia, PageInfo, error) {
	return nil, PageInfo{}, fault.New(fault.Unsupported, "highlights", "offline contract only")
}

func (c *Client) FetchMediaInfo(ctx context.Context, username, mediaPK, mediaCode string) (Media, error) {
	if err := c.authorized(username, ""); err != nil {
		return Media{}, err
	}
	// Missing Graph media_url can indicate copyright or availability restrictions.
	// Do not probe another endpoint or substitute a video thumbnail.
	return Media{}, fault.New(fault.Unsupported, "media", "permitted source did not supply a downloadable media URL; unavailable or restricted media cannot be recovered")
}

// FetchSinglePhoto is a bounded operation for developer validation: one selected
// feed photo, no listing, pagination, album expansion or URL fallback.
func (c *Client) FetchSinglePhoto(ctx context.Context, username, mediaID string) (Media, error) {
	if err := c.authorized(username, ""); err != nil {
		return Media{}, err
	}
	if !models.IsNumericID(mediaID) {
		return Media{}, fault.New(fault.InvalidInput, "selected photo", "expected an exact numeric media ID")
	}
	var g graphMedia
	if err := c.get(ctx, mediaID, mediaFields, "", &g); err != nil {
		return Media{}, err
	}
	if exactID(g.ID) == "" {
		return Media{}, fault.New(fault.Schema, "selected photo", "missing media identity in response")
	}
	if exactID(g.ID) != mediaID {
		return Media{}, fault.New(fault.Permission, "selected photo", "returned media does not match selected ID")
	}
	if g.Type != "IMAGE" || g.Product != "FEED" {
		return Media{}, fault.New(fault.Unsupported, "selected photo", "initial smoke scope is one feed photo; no other category or album requested")
	}
	return c.normalizeGraphMedia(ctx, g, c.profile.UserID, c.profile.Username, false)
}
