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

// dropMinBars 是估计回撤分位数所需的最少样本数。分位数取的是尾部, 要让尾部站得住,
// 样本数至少得够 1/(1-q) 个才能保证有样本落在分位数之上——默认 q=0.995 时正好是 200。
// 样本不够就退回固定阈值, 不要拿一个被单个极值决定的数去当真。
const dropMinBars = 200

// bar 是一根已经走完的 K 线。
type bar struct {
	t     time.Time
	high  float64
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
		// 最高价缺失时退回收盘价, 至少不会把回撤算成负数
		high := floatOf(c.High)
		if high < close {
			high = close
		}
		bars = append(bars, bar{t: closeAt, high: high, close: close})
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

// DropQuantile 返回该品种自己的「暴跌」尺子: 「从最近 windowBars 根 K 线的最高价
// 回落到该根收盘价」这一幅度的 q 分位数(百分比)。
//
// 为什么不用「k × 标准差」来定义暴跌: σ 是拿最近几天算的, 暴跌当天它自己被暴跌
// 抬上去好几倍, 于是尺子跟着变长, 越暴力的暴跌越量不出来——一次 7% 的下跌配上
// 抬升 5 倍的 σ, 反而达不到阈值, 永远不武装。分位数不受这个污染: 一场暴跌只是
// 几百个样本里的几个, 抬不动分位数。同时它也不假设收益正态, 而加密收益是厚尾的。
//
// 每个窗口用当根的收盘价作被减数, 而不是当根的最低价: 运行时判定也是拿某一次
// 采样价去和窗口最高价比, 用收盘价正是同一个统计量在同一时间尺度上的一个样本。
// 用最低价会系统性高估(那是一根 K 线里最极端的那个点)。
//
// 第二个返回值是实际样本数, 少于 dropMinBars 时分位数返回 0, 调用方退回固定阈值。
func DropQuantile(bars []bar, windowBars int, q float64) (float64, int) {
	if windowBars < 1 {
		windowBars = 1
	}
	if q <= 0 || q >= 1 || len(bars) <= windowBars {
		return 0, 0
	}

	drops := make([]float64, 0, len(bars)-windowBars+1)
	for i := windowBars - 1; i < len(bars); i++ {
		high := 0.0
		for j := i - windowBars + 1; j <= i; j++ {
			h := bars[j].high
			if h < bars[j].close {
				h = bars[j].close
			}
			if h > high {
				high = h
			}
		}
		close := bars[i].close
		if high <= 0 || close <= 0 || close > high {
			continue
		}
		drops = append(drops, (high-close)/high*100)
	}
	if len(drops) < dropMinBars {
		return 0, len(drops)
	}

	sort.Float64s(drops)
	// 最近秩法: 超过该值的样本恰好占 (1-q)。q 很接近 1 时索引会撞到末尾, 夹一下。
	idx := int(math.Ceil(q*float64(len(drops)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(drops) {
		idx = len(drops) - 1
	}
	return drops[idx], len(drops)
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
