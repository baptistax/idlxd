package instagram

import (
	"context"
	"errors"
	"fmt"
)

type storiesResponse struct {
	Data struct {
		ReelsMedia struct {
			Reels []struct {
				Items []Media `json:"items"`
			} `json:"reels_media"`
		} `json:"xdt_api__v1__feed__reels_media"`
	} `json:"data"`
	Status string `json:"status"`
}

func (c *Client) FetchStories(ctx context.Context, username, userID string) ([]Media, error) {
	username = normalizeUsername(username)
	if userID == "" {
		return nil, errors.New("profile id is empty")
	}

	referer := fmt.Sprintf("%s/stories/%s/", baseWWW, username)
	vars := map[string]any{
		"reel_ids_arr": []string{userID},
	}

	var out storiesResponse
	if err := c.GraphQL(ctx, referer, "PolarisStoriesV3ReelPageStandaloneQuery", docStoriesStandalone, vars, &out); err != nil {
		return nil, err
	}
	if len(out.Data.ReelsMedia.Reels) == 0 {
		return nil, nil
	}
	return out.Data.ReelsMedia.Reels[0].Items, nil
}
