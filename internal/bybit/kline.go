package bybit

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) DailyCandles(ctx context.Context, category, symbol string, start, end time.Time) ([]Candle, error) {
	q := url.Values{}
	q.Set("category", category)
	q.Set("symbol", symbol)
	q.Set("interval", "D")
	q.Set("start", strconv.FormatInt(start.UnixMilli(), 10))
	q.Set("end", strconv.FormatInt(end.UnixMilli(), 10))
	q.Set("limit", "10")

	var res struct {
		List     []Candle `json:"list"`
		Category string   `json:"category"`
	}
	if err := c.call(ctx, "/v5/market/kline", q, &res); err != nil {
		return nil, err
	}
	return res.List, nil
}
