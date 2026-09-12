package bybit

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// MaxKlineLimit 是 kline 接口 limit 参数的上限。回补窗口与波动率回看的长度
// 都要受它约束: 拉不满的区间等于永远填不满, 判定会一直卡在「未就绪」。
const MaxKlineLimit = 1000

// Candles 拉取 K 线。interval 用 Bybit 的写法: 1/3/5/15/30/60/120/240/360/720/D/W/M,
// 注意不是 "1m" 这种形式。start/end 为零值时不下发对应参数, limit 上限 1000。
func (c *Client) Candles(ctx context.Context, category, symbol, interval string, start, end time.Time, limit int) ([]Candle, error) {
	q := url.Values{}
	q.Set("category", category)
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	if !start.IsZero() {
		q.Set("start", strconv.FormatInt(start.UnixMilli(), 10))
	}
	if !end.IsZero() {
		q.Set("end", strconv.FormatInt(end.UnixMilli(), 10))
	}
	q.Set("limit", strconv.Itoa(limit))

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
		cdl := Candle{
			OpenTime: strs[0],
			Open:     strs[1],
			High:     strs[2],
			Low:      strs[3],
			Close:    strs[4],
			Volume:   strs[5],
		}
		if len(strs) > 6 {
			cdl.Turnover = strs[6]
		}
		candles = append(candles, cdl)
	}
	return candles, nil
}
