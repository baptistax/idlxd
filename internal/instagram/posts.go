package instagram

import (
    "context"
    "errors"
    "fmt"
)

type postsResponse struct {
    Data struct {
        Connection struct {
            Edges []struct {
                Node Media `json:"node"`
            } `json:"edges"`
            PageInfo PageInfo `json:"page_info"`
        } `json:"xdt_api__v1__feed__user_timeline_graphql_connection"`
    } `json:"data"`
    Status string `json:"status"`
}

func (c *Client) FetchPostsPage(ctx context.Context, username string, after string) ([]Media, PageInfo, string, error) {
    username = normalizeUsername(username)
    if username == "" {
        return nil, PageInfo{}, "", errors.New("usuário inválido")
    }

    referer := fmt.Sprintf("%s/%s/", baseWWW, username)

    if after == "" {
        vars := map[string]any{
            "data": map[string]any{
                "count":                          12,
                "include_reel_media_seen_timestamp": true,
                "include_relationship_info":       true,
                "latest_besties_reel_media":       true,
                "latest_reel_media":               true,
            },
            "username": username,
        }
        var out postsResponse
        if err := c.GraphQL(ctx, referer, "PolarisProfilePostsQuery", docPostsFirstPage, vars, &out); err != nil {
            return nil, PageInfo{}, "", err
        }
        items, userID := flattenTimeline(out.Data.Connection.Edges)
        return items, out.Data.Connection.PageInfo, userID, nil
    }

    vars := map[string]any{
        "after":  after,
        "before": nil,
        "data": map[string]any{
            "count":                          12,
            "include_reel_media_seen_timestamp": true,
            "include_relationship_info":       true,
            "latest_besties_reel_media":       true,
            "latest_reel_media":               true,
        },
        "first":    12,
        "last":     nil,
        "username": username,
    }

    var out postsResponse
    if err := c.GraphQL(ctx, referer, "PolarisProfilePostsTabContentQuery_connection", docPostsPagination, vars, &out); err != nil {
        return nil, PageInfo{}, "", err
    }
    items, userID := flattenTimeline(out.Data.Connection.Edges)
    return items, out.Data.Connection.PageInfo, userID, nil
}

func flattenTimeline(edges []struct{ Node Media `json:"node"` }) ([]Media, string) {
    items := make([]Media, 0, len(edges))
    userID := ""
    for _, e := range edges {
        items = append(items, e.Node)
        if userID == "" && e.Node.User.PK != "" {
            userID = e.Node.User.PK
        }
    }
    return items, userID
}

func normalizeUsername(s string) string {
    s = trimSpaces(s)
    if s == "" {
        return ""
    }
    if s[0] == '@' {
        s = s[1:]
    }
    return s
}
