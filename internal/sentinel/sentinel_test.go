package sentinel

import (
	"testing"
	"time"

	"bybit-position-monitor/internal/bybit"
	"bybit-position-monitor/internal/cfg"
)

// 回补跨度必须明显长于窗口。回放时窗口自己要先积累满 DropWindow 才给出第一个
// 判定, 真正能重放的历史只有「回补跨度 − DropWindow」这一段; 只回补一个窗口的话
// 这段就是零, 重启落在暴跌中途时 low 会从重启价起算, replay 等于白做。
func TestBackfillSpansWellBeyondWindow(t *testing.T) {
	for _, win := range []time.Duration{time.Hour, 4 * time.Hour, 8 * time.Hour} {
		s := &Sentinel{cfg: cfg.Sentinel{DropWindow: win}}

		span := s.backfillSpan()
		if span <= win {
			t.Fatalf("窗口 %s: 回补跨度 %s 没有超过窗口, replay 无历史可放", win, span)
		}
		// 至少要能重放出可观的时长, 否则等于只是把窗口填满
		if replayable := span - win; replayable < 2*time.Hour {
			t.Fatalf("窗口 %s: 可重放的历史只有 %s, 太短", win, replayable)
		}
	}
}

// 无论窗口配多大, 回补都不能超过接口一次能给的根数, 否则窗口永远填不满。
func TestBackfillLimitWithinAPICap(t *testing.T) {
	for _, win := range []time.Duration{time.Minute, time.Hour, 16 * time.Hour, 100 * time.Hour} {
		s := &Sentinel{cfg: cfg.Sentinel{DropWindow: win}}

		n := s.backfillLimit()
		if n < 1 || n > bybit.MaxKlineLimit {
			t.Fatalf("窗口 %s: 回补根数 %d 越界 (1..%d)", win, n, bybit.MaxKlineLimit)
		}
	}
}
