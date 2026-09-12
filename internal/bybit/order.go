package bybit

import (
	"context"
	"errors"
	"strings"
)

// 下单相关的错误码。官方提示同一业务错误按校验顺序可能返回不同 retCode,
// 所以判定时除了码还要看 retMsg(见 IsAlreadyClosed)。
const (
	CodeRateLimit     = 10006  // 超过 API 限频
	CodeQtyTruncated  = 110017 // 下单数量被截断为 0, 通常意味着仓位已不存在
	CodeNoNetPosition = 110034 // 没有净持仓
)

// CloseShort 以市价单平掉一个空头仓位。
//
// side 固定为 Buy(平空方向); positionIdx 直接透传仓位的值——单向持仓模式为 0,
// 双向持仓模式下空头为 2——这样无需探测账户当前是哪种持仓模式。
// qty 传仓位的实际数量而不是 "0": 官方文档说明 qty=0 只保证平到 maxMktOrderQty,
// 不保证一次清空。
func (c *Client) CloseShort(ctx context.Context, category, symbol, qty string, positionIdx int) (string, error) {
	body := map[string]any{
		"category":    category,
		"symbol":      symbol,
		"side":        "Buy",
		"orderType":   "Market",
		"qty":         normalizeQty(qty),
		"reduceOnly":  true,
		"positionIdx": positionIdx,
	}

	var res struct {
		OrderID string `json:"orderId"`
	}
	if err := c.post(ctx, "/v5/order/create", body, &res); err != nil {
		return "", err
	}
	return res.OrderID, nil
}

// IsRateLimited 判断错误是否为 API 限频, 调用方应退避后重试。
func IsRateLimited(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.RetCode == CodeRateLimit
}

// IsAlreadyClosed 判断错误是否意味着「这个仓位已经不存在了」。
// 这种失败不是真的失败(可能是上一轮已经平掉, 或人工平了), 应当视为成功并清理状态。
func IsAlreadyClosed(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.RetCode {
	case CodeQtyTruncated, CodeNoNetPosition:
		return true
	}
	msg := strings.ToLower(ae.RetMsg)
	return strings.Contains(msg, "cannot fix reduce-only order qty") ||
		strings.Contains(msg, "no net position")
}

// normalizeQty 去掉数量里可能存在的符号与空白。Bybit 的 position.size 恒为正数,
// 但下单接口对负数会直接报错, 这里容错处理一下。
func normalizeQty(qty string) string {
	return strings.TrimPrefix(strings.TrimSpace(qty), "-")
}
