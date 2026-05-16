package instagram

import (
	"context"
	"errors"
	"fmt"
)

type highlightsTrayResponse struct {
	Data struct {
		Highlights struct {
			Edges []struct {
				Node struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"highlights"`
	} `json:"data"`
	Status string `json:"status"`
}

func (c *Client) FetchHighlightsTray(ctx context.Context, username, userID string) ([]Highlight, error) {
	username = normalizeUsername(username)
	if userID == "" {
		return nil, errors.New("profile id is empty")
	}
	referer := fmt.Sprintf("%s/%s/", baseWWW, username)

	vars := map[string]any{
		"user_id": userID,
	}

	var out highlightsTrayResponse
	if err := c.GraphQL(ctx, referer, "PolarisProfileStoryHighlightsTrayContentQuery", docHighlightsTray, vars, &out); err != nil {
		return nil, err
	}

	hs := make([]Highlight, 0, len(out.Data.Highlights.Edges))
	for _, e := range out.Data.Highlights.Edges {
		if e.Node.ID == "" {
			continue
		}
		hs = append(hs, Highlight{
			ID:    e.Node.ID,
			Title: e.Node.Title,
		})
	}
	return hs, nil
}

type highlightsPageResponse struct {
	Data struct {
		Connection struct {
			Edges []struct {
				Node struct {
					ID    string  `json:"id"`
					Items []Media `json:"items"`
				} `json:"node"`
			} `json:"edges"`
			PageInfo PageInfo `json:"page_info"`
		} `json:"xdt_api__v1__feed__reels_media__connection"`
	} `json:"data"`
	Status string `json:"status"`
}

func (c *Client) FetchHighlightsPage(ctx context.Context, username string, reelIDs []string, after string, first int) ([]struct {
	ID    string
	Items []Media
}, PageInfo, error) {
	username = normalizeUsername(username)
	if len(reelIDs) == 0 {
		return nil, PageInfo{}, errors.New("no highlights")
	}
	referer := fmt.Sprintf("%s/%s/", baseWWW, username)

	friendlyName := "PolarisStoriesV3HighlightsPageQuery"
	docID := docHighlightsPage
	vars := map[string]any{
		"initial_reel_id": reelIDs[0],
		"reel_ids":        reelIDs,
		"first":           3,
		"last":            2,
	}
	if after != "" {
		if first <= 0 {
			first = 2
		}
		friendlyName = "PolarisStoriesV3HighlightsPagePaginationQuery"
		docID = docHighlightsPageConn
		vars = map[string]any{
			"after":           after,
			"before":          nil,
			"first":           first,
			"initial_reel_id": reelIDs[0],
			"is_highlight":    true,
			"last":            nil,
			"reel_ids":        reelIDs,
		}
	}

	var out highlightsPageResponse
	if err := c.GraphQL(ctx, referer, friendlyName, docID, vars, &out); err != nil {
		return nil, PageInfo{}, err
	}

	res := make([]struct {
		ID    string
		Items []Media
	}, 0, len(out.Data.Connection.Edges))

	for _, e := range out.Data.Connection.Edges {
		res = append(res, struct {
			ID    string
			Items []Media
		}{
			ID:    e.Node.ID,
			Items: e.Node.Items,
		})
	}

	return res, out.Data.Connection.PageInfo, nil
}
