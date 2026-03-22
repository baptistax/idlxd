package instagram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var profileUserIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"logging_page_id":"profilePage_([0-9]+)"`),
	regexp.MustCompile(`"profilePage_([0-9]+)"`),
	regexp.MustCompile(`"profile_id":"([0-9]+)"`),
	regexp.MustCompile(`"target_user_id":"([0-9]+)"`),
	regexp.MustCompile(`"user_id":"([0-9]+)"`),
}

func (c *Client) FetchProfile(ctx context.Context, username string) (Profile, error) {
	username = normalizeUsername(username)
	if username == "" {
		return Profile{}, errors.New("usage: idl <username>")
	}

	profileURL := fmt.Sprintf("%s/%s/", baseWWW, username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return Profile{}, err
	}
	c.applyCommonHeaders(req, profileURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("failed to reach Instagram profile: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Profile{}, fmt.Errorf("Instagram profile not found: %s", username)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Profile{}, fmt.Errorf("Instagram returned %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Profile{}, err
	}

	return Profile{
		Username: username,
		UserID:   parseProfileUserID(body),
	}, nil
}

func parseProfileUserID(body []byte) string {
	s := string(body)
	for _, pattern := range profileUserIDPatterns {
		m := pattern.FindStringSubmatch(s)
		if len(m) == 2 {
			return m[1]
		}
	}
	return ""
}
