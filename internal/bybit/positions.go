package bybit

import (
	"context"
	"net/url"
)

func (c *Client) Positions(ctx context.Context, category string) ([]Position, error) {
	var all []Position
	cursor := ""
	for page := 0; page < 200; page++ {
		q := url.Values{}
		q.Set("category", category)
		q.Set("limit", "200")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var res struct {
			List           []Position `json:"list"`
			NextPageCursor string     `json:"nextPageCursor"`
			HasMore        bool       `json:"hasMore"`
		}
		if err := c.call(ctx, "/v5/position/list", q, &res); err != nil {
			return nil, err
		}
		all = append(all, res.List...)
		if !res.HasMore || res.NextPageCursor == "" {
			break
		}
		cursor = res.NextPageCursor
	}
	return all, nil
}
