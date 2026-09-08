package bybit

import (
	"context"
	"encoding/json"
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
		List     []json.RawMessage `json:"list"`
		Category string            `json:"category"`
	}
	if err := c.call(ctx, "/v5/market/kline", q, &res); err != nil {
		return nil, err
	}

	candles := make([]Candle, 0, len(res.List))
	for _, raw := range res.List {
		var cdl Candle
		if err := json.Unmarshal(raw, &cdl); err != nil {
			continue
		}
		candles = append(candles, cdl)
	}
	return candles, nil
}
