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
		if len(raw) > 0 && raw[0] == '{' {
			var cdl Candle
			if err := json.Unmarshal(raw, &cdl); err != nil {
				continue
			}
			candles = append(candles, cdl)
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			continue
		}
		if len(arr) < 6 {
			continue
		}
		strs := make([]string, len(arr))
		for i, a := range arr {
			json.Unmarshal(a, &strs[i])
		}
		candles = append(candles, Candle{
			OpenTime: strs[0],
			Open:     strs[1],
			High:     strs[2],
			Low:      strs[3],
			Close:    strs[4],
			Volume:   strs[5],
			Turnover: " ",
		})
		if len(strs) > 6 {
			candles[len(candles)-1].Turnover = strs[6]
		}
	}
	return candles, nil
}
