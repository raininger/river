// Package numfmt 提供数字/金额的格式化助手, 供日报与哨兵共用。
package numfmt

import (
	"fmt"
	"strconv"
	"strings"
)

// Pct 格式化为带符号的百分比, 如 +3.20% / -1.05%
func Pct(v float64) string {
	return fmt.Sprintf("%+.2f%%", v)
}

// SignedMoney 格式化为带千分位的两位小数金额
func SignedMoney(v float64) string {
	return Grouped(v, 2)
}

// Grouped 保留 dec 位小数并加千分位分隔符
func Grouped(v float64, dec int) string {
	neg := v < 0
	s := strconv.FormatFloat(abs(v), 'f', dec, 64)
	return sign(neg) + groupInt(s)
}

func groupInt(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return groupInt(s[:i]) + s[i:]
	}
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	n := len(s)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func sign(neg bool) string {
	if neg {
		return "-"
	}
	return ""
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
