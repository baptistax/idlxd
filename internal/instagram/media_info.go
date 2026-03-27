package instagram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type mediaInfoResponse struct {
	Items  []Media `json:"items"`
	Status string  `json:"status"`
}

func (c *Client) FetchMediaInfo(ctx context.Context, username string, mediaPK string, mediaCode string) (Media, error) {
	username = normalizeUsername(username)
	mediaPK = strings.TrimSpace(mediaPK)
	mediaCode = strings.TrimSpace(mediaCode)
	if mediaPK == "" {
		return Media{}, errors.New("media pk is empty")
	}

	if err := c.EnsureTokens(ctx); err != nil {
		return Media{}, err
	}

	referer := fmt.Sprintf("%s/%s/", baseWWW, username)
	if mediaCode != "" {
		referer = fmt.Sprintf("%s/reel/%s/", baseWWW, mediaCode)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/media/%s/info/", baseWWW, mediaPK), nil)
	if err != nil {
		return Media{}, err
	}
	c.applyCommonHeaders(req, referer)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", asbdID)
	if csrf := c.cookieValue("csrftoken"); csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Media{}, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Media{}, fmt.Errorf("Instagram returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Media{}, err
	}

	var out mediaInfoResponse
	if err := decodeGraphQLResponse(body, &out); err != nil {
		return Media{}, err
	}
	if len(out.Items) == 0 {
		return Media{}, errors.New("Instagram media info response had no items")
	}
	return out.Items[0], nil
}
