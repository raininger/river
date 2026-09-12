package sentinel

import (
	"math"
	"strconv"
	"testing"
	"time"

	"bybit-position-monitor/internal/bybit"
)

// buildBars 造 n 根间隔 step 的 K 线, 收盘价由 f 决定。
func buildBars(n int, step time.Duration, f func(i int) float64) []bar {
	bars := make([]bar, n)
	for i := range bars {
		bars[i] = bar{t: t0.Add(time.Duration(i) * step), close: f(i)}
	}
	return bars
}

func candleAt(open time.Time, close string) bybit.Candle {
	return bybit.Candle{
		OpenTime: strconv.FormatInt(open.UnixMilli(), 10),
		Close:    close,
	}
}

// 对数收益率交替 +5% / -5% 时, 样本标准差应为 0.05 × sqrt(n/(n-1))。
// 用对数收益率而不是简单收益率, 才能让这样一组涨跌的均值正好为 0。
func TestHourlyVolAlternatingReturns(t *testing.T) {
	up := math.Exp(0.05)
	bars := buildBars(31, time.Hour, func(i int) float64 {
		if i%2 == 0 {
			return 100
		}
		return 100 * up
	})

	vol, n := HourlyVol(bars, time.Hour)
	if n != 30 {
		t.Fatalf("31 根 K 线应产生 30 个收益率样本, 实际 %d", n)
	}
	approx(t, vol, 0.05*math.Sqrt(30.0/29.0))
}

// 样本不足时返回 0, 调用方据此退回兜底阈值; 同时如实报告样本数用于日志。
func TestHourlyVolTooFewBars(t *testing.T) {
	bars := buildBars(20, time.Hour, func(i int) float64 { return 100 + float64(i) })

	vol, n := HourlyVol(bars, time.Hour)
	if vol != 0 {
		t.Fatalf("样本不足时波动率应为 0, 实际 %v", vol)
	}
	if n != 19 {
		t.Fatalf("应报告 19 个样本, 实际 %d", n)
	}
}

// 相邻两根之间缺了 K 线时, 那个收益率跨的是更长的时间, 与其他样本不可比,
// 必须剔除, 否则一个数据空洞会把波动率整体抬上去。
func TestHourlyVolSkipsGappedBars(t *testing.T) {
	bars := buildBars(31, time.Hour, func(i int) float64 { return 100 })
	// 补一根隔了 3 小时且价格翻倍的 K 线: 不剔除的话它会主导整个标准差
	bars = append(bars, bar{t: bars[len(bars)-1].t.Add(3 * time.Hour), close: 200})

	vol, n := HourlyVol(bars, time.Hour)
	if n != 30 {
		t.Fatalf("断层处的收益率应被剔除, 期望 30 个样本, 实际 %d", n)
	}
	approx(t, vol, 0)
}

// 接口返回的是倒序, 且最后一根往往还没走完; 未走完的那根收盘价尚未成立, 必须丢弃。
func TestBarSeriesSortsAndDropsIncompleteBar(t *testing.T) {
	now := t0.Add(150 * time.Minute)
	cs := []bybit.Candle{
		candleAt(t0.Add(2*time.Hour), "103"), // 尚未走完(要到 t0+3h 才收盘)
		candleAt(t0.Add(time.Hour), "102"),
		candleAt(t0, "100"),
	}

	bars := barSeries(cs, time.Hour, now)
	if len(bars) != 2 {
		t.Fatalf("应剩 2 根已走完的 K 线, 实际 %d", len(bars))
	}
	// 时间取收盘时刻(openTime + 周期), 并按正序排列
	if want := t0.Add(time.Hour); !bars[0].t.Equal(want) || bars[0].close != 100 {
		t.Fatalf("第 0 根应为 %v/100, 实际 %v/%v", want, bars[0].t, bars[0].close)
	}
	if want := t0.Add(2 * time.Hour); !bars[1].t.Equal(want) || bars[1].close != 102 {
		t.Fatalf("第 1 根应为 %v/102, 实际 %v/%v", want, bars[1].t, bars[1].close)
	}
}

// 非正收盘价会让对数收益率变成 NaN 或 -Inf, 必须剔除。
func TestBarSeriesDropsNonPositiveClose(t *testing.T) {
	now := t0.Add(3 * time.Hour)
	cs := []bybit.Candle{
		candleAt(t0, "100"),
		candleAt(t0.Add(time.Hour), "0"),
		candleAt(t0.Add(2*time.Hour), "102"),
	}

	bars := barSeries(cs, time.Hour, now)
	if len(bars) != 2 {
		t.Fatalf("非正收盘价应被剔除, 实际剩 %d 根", len(bars))
	}
	for _, b := range bars {
		if b.close <= 0 {
			t.Fatalf("不应残留非正收盘价: %v", b.close)
		}
	}
}
