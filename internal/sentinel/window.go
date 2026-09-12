package sentinel

import "time"

// minSamples 是判定「窗口已就绪」所需的最少采样点数。
// 轮询连续失败会在窗口里留下大空洞, 只有两三个点不足以保证参考价有意义。
const minSamples = 3

type sample struct {
	t     time.Time
	price float64
}

// PriceWindow 记录单个品种的标记价采样, 用于计算「现价相对最近 window 内最高价的回撤」。
//
// 采样点由每次轮询追加, 过期数据自动丢弃, 因此稳态下不产生任何额外请求。
// 时间必须是递增的: 启动时的 K 线回补按时间正序灌入, 之后是实时采样。
type PriceWindow struct {
	window time.Duration
	// staleness 是数据新鲜度的容忍上限。轮询若长时间停摆, 样本会整体过期,
	// 此时宁可不动作, 也不要拿一个陈旧的价格当基准。
	staleness time.Duration
	// retain 是样本保留时长, 比 window 多留一段以便窗口边界上总有样本可用
	// (markPrice 采样是离散的, 边界上未必刚好有一笔)。
	retain  time.Duration
	samples []sample
}

func NewPriceWindow(window, poll time.Duration) *PriceWindow {
	// 回补用的是 1 分钟 K 线, 因此容忍度不能小于 1 分钟, 否则回补出来的
	// 数据会被自己的新鲜度检查判为过期。
	staleness := 3 * poll
	if staleness < 3*time.Minute {
		staleness = 3 * time.Minute
	}
	return &PriceWindow{
		window:    window,
		staleness: staleness,
		retain:    window + staleness,
		samples:   make([]sample, 0, 64),
	}
}

// Add 追加一次采样并丢弃过期数据。非正价格会被忽略(脏数据会让跌幅算成 -100%)。
func (w *PriceWindow) Add(t time.Time, price float64) {
	if price <= 0 {
		return
	}
	if n := len(w.samples); n > 0 && !t.After(w.samples[n-1].t) {
		// 时间未前进说明是重复/乱序数据, 直接丢弃以维持样本有序
		return
	}
	w.samples = append(w.samples, sample{t: t, price: price})
	w.trim(t)
}

// trim 丢掉超过保留期的样本, 让内存占用有界。
func (w *PriceWindow) trim(now time.Time) {
	cutoff := now.Add(-w.retain)
	i := 0
	for i < len(w.samples) && w.samples[i].t.Before(cutoff) {
		i++
	}
	if i > 0 {
		// 原地前移, 不重新分配
		w.samples = append(w.samples[:0], w.samples[i:]...)
	}
}

// Fall 返回现价相对窗口内最高价的跌幅(百分比), 下跌为正。
// ok 为 false 表示窗口未就绪或数据不可靠, 调用方必须跳过该品种。
//
// 口径是「从窗口内的高点回撤了多少」, 而不是「相对窗口边界那一笔的涨跌」。
// 后者只比较窗口两端, 缓慢阴跌(每个轮询只跌一点点)永远凑不出足够的落差;
// 回撤口径与路径无关, 阶梯式暴跌和阴跌都能被识别为同一场下跌。
func (w *PriceWindow) Fall(now time.Time, price float64) (float64, bool) {
	if price <= 0 {
		return 0, false
	}
	high, ok := w.High(now)
	if !ok {
		return 0, false
	}
	return (high - price) / high * 100, true
}

// High 返回窗口内的最高价。reference 的就绪判断同时用作前置门槛:
// 窗口没覆盖满、数据不新鲜、或基准过老时都不给出结论。
func (w *PriceWindow) High(now time.Time) (float64, bool) {
	if _, ok := w.reference(now); !ok {
		return 0, false
	}
	cutoff := now.Add(-w.window)
	high := 0.0
	for _, s := range w.samples {
		// 样本按时间递增, 但过期样本尚未被 trim 掉(trim 在 retain 之后才动手),
		// 所以要显式排除窗口之外的样本
		if s.t.Before(cutoff) {
			continue
		}
		if s.price > high {
			high = s.price
		}
	}
	if high <= 0 {
		return 0, false
	}
	return high, true
}

// reference 选取参考价, 即窗口边界上(或刚好越过边界)的最新一笔采样。
func (w *PriceWindow) reference(now time.Time) (float64, bool) {
	n := len(w.samples)
	if n < minSamples {
		return 0, false
	}
	// 最新一笔都已经过期, 说明轮询停摆了很久, 不能据此判定
	if now.Sub(w.samples[n-1].t) > w.staleness {
		return 0, false
	}
	// 从最新往回找第一笔 age >= window 的样本。样本按时间递增,
	// 所以从末尾倒着扫, 第一个满足条件的就是「边界上最新的一笔」。
	for i := n - 1; i >= 0; i-- {
		age := now.Sub(w.samples[i].t)
		if age < w.window {
			continue
		}
		if age > w.window+w.staleness {
			// 参考价过老(中间有过长时间空洞), 宁可不动
			return 0, false
		}
		return w.samples[i].price, true
	}
	return 0, false
}

// Ready 报告窗口是否已就绪。
func (w *PriceWindow) Ready(now time.Time) bool {
	_, ok := w.reference(now)
	return ok
}

// Len 返回当前保留的样本数。
func (w *PriceWindow) Len() int {
	return len(w.samples)
}
