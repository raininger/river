package monitor

import (
	"strconv"
	"strings"
)

func fmtGrouped(v float64, dec int) string {
	neg := v < 0
	s := strconv.FormatFloat(abs(v), 'f', dec, 64)
	return sign(neg) + groupInt(s)
}

func fmtGroupedTrim(v float64) string {
	neg := v < 0
	s := strconv.FormatFloat(abs(v), 'f', -1, 64)
	return sign(neg) + groupInt(s)
}

func moneyTrim(v float64) string {
	if v >= 1000 {
		return fmtGrouped(v, 0)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func signedMoney(v float64) string {
	return fmtGrouped(v, 2)
}

func numTrim(v float64) string {
	return fmtGroupedTrim(v)
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
