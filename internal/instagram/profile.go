package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

	profile := Profile{
		Username: username,
		UserID:   parseProfileUserID(body),
	}

	webInfoErr := error(nil)
	if profile.UserID == "" {
		if webProfile, err := c.fetchWebProfileInfo(ctx, username); err == nil {
			profile = mergeProfile(profile, webProfile)
		} else {
			webInfoErr = err
		}
	}

	profileContentErr := error(nil)
	if profile.UserID == "" {
		if gqlProfile, err := c.fetchProfileContent(ctx, username); err == nil {
			profile = mergeProfile(profile, gqlProfile)
		} else {
			profileContentErr = err
		}
	}

	if profile.UserID == "" {
		if profileContentErr != nil {
			return Profile{}, fmt.Errorf("failed to resolve profile id: %v", profileContentErr)
		}
		if webInfoErr != nil {
			return Profile{}, fmt.Errorf("failed to resolve profile id: %v", webInfoErr)
		}
		return Profile{}, errors.New("failed to resolve profile id")
	}
	return profile, nil
}

func mergeProfile(dst, src Profile) Profile {
	if src.Username != "" {
		dst.Username = src.Username
	}
	if src.UserID != "" {
		dst.UserID = src.UserID
	}
	dst.IsPrivate = src.IsPrivate
	if src.PostsCountKnown {
		dst.PostsCount = src.PostsCount
		dst.PostsCountKnown = true
	}
	return dst
}

type webProfileInfoResponse struct {
	Data struct {
		User struct {
			ID         string `json:"id"`
			PK         string `json:"pk"`
			Username   string `json:"username"`
			IsPrivate  bool   `json:"is_private"`
			MediaCount int    `json:"media_count"`
			Timeline   struct {
				Count int `json:"count"`
			} `json:"edge_owner_to_timeline_media"`
		} `json:"user"`
	} `json:"data"`
	Status string `json:"status"`
}

func (c *Client) fetchWebProfileInfo(ctx context.Context, username string) (Profile, error) {
	profileURL := fmt.Sprintf("%s/api/v1/users/web_profile_info/?username=%s", baseWWW, url.QueryEscape(username))
	referer := fmt.Sprintf("%s/%s/", baseWWW, username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return Profile{}, err
	}
	c.applyCommonHeaders(req, referer)
	req.Header.Set("X-IG-App-ID", igAppID)
	if csrf := c.cookieValue("csrftoken"); csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("failed to reach Instagram profile metadata: %v", err)
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

	var out webProfileInfoResponse
	if err := decodeGraphQLResponse(body, &out); err != nil {
		return Profile{}, err
	}
	p := Profile{
		Username:        out.Data.User.Username,
		UserID:          out.Data.User.PK,
		IsPrivate:       out.Data.User.IsPrivate,
		PostsCount:      out.Data.User.MediaCount,
		PostsCountKnown: true,
	}
	if p.UserID == "" {
		p.UserID = out.Data.User.ID
	}
	if p.PostsCount == 0 && out.Data.User.Timeline.Count > 0 {
		p.PostsCount = out.Data.User.Timeline.Count
	}
	if p.Username == "" {
		p.Username = username
	}
	if !isNumericID(p.UserID) {
		return Profile{}, errors.New("web profile metadata did not include profile id")
	}
	return p, nil
}

func parseProfileUserID(body []byte) string {
	if id := profileIDFromStructuredPayload(body); id != "" {
		return id
	}

	s := string(body)
	for _, pattern := range profileUserIDPatterns {
		m := pattern.FindStringSubmatch(s)
		if len(m) == 2 && isNumericID(m[1]) {
			return m[1]
		}
	}
	return ""
}

type profileContentResponse = map[string]any

func (c *Client) fetchProfileContent(ctx context.Context, username string) (Profile, error) {
	referer := fmt.Sprintf("%s/%s/", baseWWW, username)
	var out profileContentResponse
	vars := map[string]any{
		"__relay_internal__pv__PolarisCASB976ProfileEnabledrelayprovider":           false,
		"__relay_internal__pv__PolarisCannesGuardianExperienceEnabledrelayprovider": true,
		"__relay_internal__pv__PolarisRepostsConsumptionEnabledrelayprovider":       true,
		"__relay_internal__pv__PolarisWebSchoolsEnabledrelayprovider":               false,
		"enable_integrity_filters": true,
		"id":                       username,
	}
	if err := c.GraphQLAPI(ctx, referer, "PolarisProfilePageContentQuery", docProfilePageContent, vars, &out); err != nil {
		return Profile{}, err
	}

	p := Profile{Username: username}
	p.UserID = findProfileID(out)
	if p.UserID == "" {
		return Profile{}, fmt.Errorf("profile content response has no profile id (%s)", shallowKeySummary(out))
	}
	p.Username = findStringField(out, "username", username)
	if v, ok := findBoolField(out, "is_private"); ok {
		p.IsPrivate = v
	}
	if n, ok := findNumericField(out, "media_count", "posts_count", "count"); ok {
		p.PostsCount = n
		p.PostsCountKnown = true
	}
	return p, nil
}

func shallowKeySummary(v map[string]any) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if data, ok := v["data"].(map[string]any); ok {
		dataKeys := make([]string, 0, len(data))
		for k := range data {
			dataKeys = append(dataKeys, "data."+k)
		}
		sort.Strings(dataKeys)
		keys = append(keys, dataKeys...)
	}
	if len(keys) == 0 {
		return "empty"
	}
	return strings.Join(keys, ",")
}

func profileIDFromStructuredPayload(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) == nil {
		if id := findProfileID(v); id != "" {
			return id
		}
	}

	for _, chunk := range scriptJSONChunks(string(body)) {
		var v any
		if json.Unmarshal([]byte(chunk), &v) != nil {
			continue
		}
		if id := findProfileID(v); id != "" {
			return id
		}
	}
	return ""
}

var scriptContentPattern = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)

func scriptJSONChunks(s string) []string {
	matches := scriptContentPattern.FindAllStringSubmatch(s, -1)
	chunks := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		content := strings.TrimSpace(html.UnescapeString(match[1]))
		if content == "" {
			continue
		}
		if json.Valid([]byte(content)) {
			chunks = append(chunks, content)
			continue
		}
		if idx := strings.Index(content, "{"); idx >= 0 {
			if obj := firstJSONObject(content[idx:]); obj != "" {
				chunks = append(chunks, obj)
			}
		}
	}
	return chunks
}

func firstJSONObject(s string) string {
	depth := 0
	inString := false
	escaped := false
	started := false
	for i, r := range s {
		if !started {
			if r != '{' {
				continue
			}
			started = true
			depth = 1
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

type profileIDCandidate struct {
	value string
	score int
	order int
}

func findProfileID(v any) string {
	var candidates []profileIDCandidate
	collectProfileIDCandidates(v, nil, 0, &candidates)
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	return candidates[0].value
}

func collectProfileIDCandidates(v any, path []string, score int, out *[]profileIDCandidate) {
	switch x := v.(type) {
	case map[string]any:
		localScore := score
		if hasAnyKey(x, "username", "full_name", "is_private", "profile_pic_url", "biography") {
			localScore += 10
		}
		for k, val := range x {
			lk := strings.ToLower(k)
			nextPath := append(path, lk)
			fieldScore := localScore + scoreProfileIDKey(lk, nextPath)
			if fieldScore > localScore {
				if id := numericIDFromAny(val); id != "" {
					*out = append(*out, profileIDCandidate{value: id, score: fieldScore, order: len(*out)})
				}
			}
			if lk == "logging_page_id" {
				if id := profilePageID(fmt.Sprint(val)); id != "" {
					*out = append(*out, profileIDCandidate{value: id, score: localScore + 100, order: len(*out)})
				}
			}
			collectProfileIDCandidates(val, nextPath, localScore, out)
		}
	case []any:
		for _, item := range x {
			collectProfileIDCandidates(item, path, score, out)
		}
	case string:
		if id := profilePageID(x); id != "" {
			*out = append(*out, profileIDCandidate{value: id, score: score + 90, order: len(*out)})
		}
	}
}

func scoreProfileIDKey(key string, path []string) int {
	switch key {
	case "profile_id", "target_user_id":
		return 95
	case "user_id", "pk":
		return 80
	case "id":
		if pathHas(path, "user") || pathHas(path, "owner") || pathHas(path, "profile") {
			return 70
		}
	case "page_id":
		if pathHas(path, "page_logging") || pathHas(path, "logging") || pathHas(path, "params") {
			return 90
		}
	}
	return 0
}

func numericIDFromAny(v any) string {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if isNumericID(x) {
			return x
		}
		if id := profilePageID(x); id != "" {
			return id
		}
	case float64:
		if x > 0 && x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil && n > 0 {
			return strconv.FormatInt(n, 10)
		}
	}
	return ""
}

func profilePageID(s string) string {
	m := regexp.MustCompile(`profilePage_([0-9]+)`).FindStringSubmatch(s)
	if len(m) == 2 && isNumericID(m[1]) {
		return m[1]
	}
	return ""
}

func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	hasNonZero := false
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		if r != '0' {
			hasNonZero = true
		}
	}
	return hasNonZero
}

func hasAnyKey(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func pathHas(path []string, part string) bool {
	for _, p := range path {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}

func findStringField(v any, key, fallback string) string {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key].(string); ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
		for _, val := range x {
			if got := findStringField(val, key, ""); got != "" {
				return got
			}
		}
	case []any:
		for _, item := range x {
			if got := findStringField(item, key, ""); got != "" {
				return got
			}
		}
	}
	return fallback
}

func findBoolField(v any, key string) (bool, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key].(bool); ok {
			return val, true
		}
		for _, val := range x {
			if got, ok := findBoolField(val, key); ok {
				return got, true
			}
		}
	case []any:
		for _, item := range x {
			if got, ok := findBoolField(item, key); ok {
				return got, true
			}
		}
	}
	return false, false
}

func findNumericField(v any, keys ...string) (int, bool) {
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}
	return findNumericFieldIn(v, keySet)
}

func findNumericFieldIn(v any, keys map[string]struct{}) (int, bool) {
	switch x := v.(type) {
	case map[string]any:
		for key, val := range x {
			if _, ok := keys[key]; ok {
				if n, ok := nonNegativeIntFromAny(val); ok {
					return n, true
				}
			}
		}
		for _, val := range x {
			if got, ok := findNumericFieldIn(val, keys); ok {
				return got, true
			}
		}
	case []any:
		for _, item := range x {
			if got, ok := findNumericFieldIn(item, keys); ok {
				return got, true
			}
		}
	}
	return 0, false
}

func nonNegativeIntFromAny(v any) (int, bool) {
	switch x := v.(type) {
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil && n >= 0
	case float64:
		if x >= 0 && x == float64(int64(x)) {
			return int(x), true
		}
	case json.Number:
		if n, err := x.Int64(); err == nil && n >= 0 {
			return int(n), true
		}
	}
	return 0, false
}
