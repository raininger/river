package sentinel

import (
	"math"
	"sort"
	"strconv"
	"time"

	"bybit-position-monitor/internal/bybit"
)

// volMinBars 是估计波动率所需的最少收益率样本数。样本太少时标准差本身就不稳定,
// 宁可退回兜底阈值, 也不要拿一个噪声很大的估计值去决定真实下单。
const volMinBars = 30

// bar 是一根已经走完的 K 线。
type bar struct {
	t     time.Time
	close float64
}

// barSeries 把接口返回的 K 线转成按时间正序、且只含已走完的那些根的序列。
//
// 接口返回的是倒序, 这里显式排序, 不依赖返回顺序。
// 时间取「K 线结束时刻」(openTime + step) 而不是 openTime: 该根的 close 反映的
// 是收盘那一刻的价格, 记在 openTime 上会让时间戳整体偏早一个周期。
func barSeries(candles []bybit.Candle, step time.Duration, now time.Time) []bar {
	bars := make([]bar, 0, len(candles))
	for _, c := range candles {
		openMillis, err := strconv.ParseInt(c.OpenTime, 10, 64)
		if err != nil {
			continue
		}
		closeAt := time.UnixMilli(openMillis).UTC().Add(step)
		if closeAt.After(now) {
			continue // 还没走完的 K 线, 其收盘价尚未成立
		}
		close := floatOf(c.Close)
		if close <= 0 {
			continue // 脏数据会让对数收益率变成 NaN
		}
		bars = append(bars, bar{t: closeAt, close: close})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].t.Before(bars[j].t) })
	return bars
}

// HourlyVol 返回相邻两根 K 线之间对数收益率的标准差, 即每 step 跨度一个单位的波动率。
// 用对数收益率而不是简单收益率, 是为了让上涨和下跌的幅度对称。
//
// 第二个返回值是实际参与计算的收益率样本数。少于 volMinBars 时波动率返回 0,
// 调用方应当退回兜底阈值。
func HourlyVol(bars []bar, step time.Duration) (float64, int) {
	// 相邻两根之间缺了 K 线时, 这个收益率跨的是两倍甚至更长的时间, 与其他样本
	// 不可比, 会把波动率整体抬上去。宁可少一个样本也不要混入不同尺度的数据。
	maxGap := step * 3 / 2

	rets := make([]float64, 0, len(bars))
	for i := 1; i < len(bars); i++ {
		prev, cur := bars[i-1], bars[i]
		if cur.t.Sub(prev.t) > maxGap {
			continue
		}
		if prev.close <= 0 || cur.close <= 0 {
			continue
		}
		rets = append(rets, math.Log(cur.close/prev.close))
	}
	if len(rets) < volMinBars {
		return 0, len(rets)
	}
	return stdev(rets), len(rets)
}

// stdev 是样本标准差(分母 n-1)。
func stdev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))

	var sq float64
	for _, x := range xs {
		d := x - mean
		sq += d * d
	}
	return math.Sqrt(sq / float64(len(xs)-1))
}
