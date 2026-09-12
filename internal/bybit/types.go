package bybit

type Position struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Size          string `json:"size"`
	AvgPrice      string `json:"avgPrice"`
	PositionValue string `json:"positionValue"`
	UnrealisedPnl string `json:"unrealisedPnl"`
	Leverage      string `json:"leverage"`
	PositionIM    string `json:"positionIM"`
	MarkPrice     string `json:"markPrice"`
	UpdatedTime   string `json:"updatedTime"`
	// PositionIdx 单向持仓模式为 0, 双向持仓模式下多头为 1、空头为 2。
	// 下单时直接透传此值, 即可同时适配两种持仓模式。
	PositionIdx int `json:"positionIdx"`
}

type Candle struct {
	OpenTime string `json:"openTime"`
	Open     string `json:"open"`
	High     string `json:"high"`
	Low      string `json:"low"`
	Close    string `json:"close"`
	Volume   string `json:"volume"`
	Turnover string `json:"turnover"`
}
