package monitor

import "bybit-position-monitor/internal/numfmt"

// 通用的数字/金额格式化已抽到 internal/numfmt, 哨兵共用同一套实现。
// 这里保留包内的短名字, 避免改动调用处。

func signedMoney(v float64) string { return numfmt.SignedMoney(v) }

func pctStr(d float64) string { return numfmt.Pct(d) }
