package instagram

import (
	"context"
	"errors"
	"fmt"
)

type reelsResponse struct {
	Data struct {
		Connection struct {
			Edges []struct {
				Node struct {
					Media Media `json:"media"`
				} `json:"node"`
			} `json:"edges"`
			PageInfo PageInfo `json:"page_info"`
		} `json:"xdt_api__v1__clips__user__connection_v2"`
	} `json:"data"`
	Status string `json:"status"`
}

func (c *Client) FetchReelsPage(ctx context.Context, username, targetUserID, after string) ([]Media, PageInfo, error) {
	username = normalizeUsername(username)
	if targetUserID == "" {
		return nil, PageInfo{}, errors.New("profile id is empty")
	}

	referer := fmt.Sprintf("%s/%s/reels/", baseWWW, username)
	vars := map[string]any{
		"data": map[string]any{
			"include_feed_video": true,
			"page_size":          12,
			"target_user_id":     targetUserID,
		},
	}
	if after != "" {
		vars["after"] = after
	}

	var out reelsResponse
	if err := c.GraphQL(ctx, referer, "PolarisProfileReelsTabContentQuery", docReelsTabContent, vars, &out); err != nil {
		return nil, PageInfo{}, err
	}

	items := make([]Media, 0, len(out.Data.Connection.Edges))
	for _, e := range out.Data.Connection.Edges {
		if e.Node.Media.PK == "" && e.Node.Media.ID == "" {
			continue
		}
		items = append(items, e.Node.Media)
	}
	return items, out.Data.Connection.PageInfo, nil
}
