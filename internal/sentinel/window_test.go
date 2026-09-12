package sentinel

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

// feed 按固定间隔灌入采样, 时间必须递增, 与启动回补 + 实时轮询的写入顺序一致。
func feed(w *PriceWindow, start time.Time, step time.Duration, prices ...float64) {
	for i, p := range prices {
		w.Add(start.Add(time.Duration(i)*step), p)
	}
}

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("得到 %.6f, 期望 %.6f", got, want)
	}
}

const (
	testWindow = 5 * time.Minute
	testPoll   = 15 * time.Second
)

// 稳定轮询 6 分钟, 窗口内最高价 100, 现价 96.5, 回撤应为 3.5%。
func TestFallRelativeToWindowHigh(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 25)
	for i := range prices {
		prices[i] = 100
	}
	prices[len(prices)-1] = 96.5
	feed(w, t0, testPoll, prices...)

	now := t0.Add(6 * time.Minute)
	fall, ok := w.Fall(now, 96.5)
	if !ok {
		t.Fatal("窗口应已就绪")
	}
	approx(t, fall, 3.5)
}

// 最高价取窗口内任意一笔, 而不是窗口边界那一笔; 且已经滑出窗口的样本不参与。
func TestHighComesFromAnywhereInWindow(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 25)
	for i := range prices {
		prices[i] = 100
	}
	prices[0] = 200 // T0, 在 now=T0+6m 时已滑出 5 分钟窗口, 不能被采用
	prices[4] = 130 // T0+1m, 恰好压在窗口边界上
	prices[24] = 95
	feed(w, t0, testPoll, prices...)

	now := t0.Add(6 * time.Minute)
	high, ok := w.High(now)
	if !ok {
		t.Fatal("窗口应已就绪")
	}
	approx(t, high, 130)

	fall, ok := w.Fall(now, 95)
	if !ok {
		t.Fatal("窗口应已就绪")
	}
	approx(t, fall, (130.0-95.0)/130.0*100)
}

// 先冲高、再暴跌、再反弹的形状: 只比较窗口两端的口径会把这看成「上涨」,
// 回撤口径才能认出窗口内确实发生过一场暴跌。
func TestFallCatchesCrashInsideWindow(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 25)
	for i := range prices {
		prices[i] = 80 // 窗口起点在 80
	}
	for i := 4; i < 24; i++ {
		prices[i] = 100 // 窗口内冲高到 100
	}
	prices[24] = 90 // 从 100 崩到 90 后小幅反弹
	feed(w, t0, testPoll, prices...)

	now := t0.Add(6 * time.Minute)
	fall, ok := w.Fall(now, 90)
	if !ok {
		t.Fatal("窗口应已就绪")
	}
	// 只看两端的话是 90 相对窗口起点的 80, 反而算成上涨;
	// 回撤口径看到的是 100 → 90
	approx(t, fall, 10)
}

// 只积累了不到一个窗口的数据时, 不能判定——否则等于拿一个更短的区间冒充 N 分钟。
func TestNotReadyBeforeWindowCovered(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 19) // 4m30s, 还差 30s 才够 5 分钟
	for i := range prices {
		prices[i] = 100
	}
	feed(w, t0, testPoll, prices...)

	now := t0.Add(4*time.Minute + 30*time.Second)
	if w.Ready(now) {
		t.Fatal("窗口尚未覆盖满, 不应就绪")
	}
	if _, ok := w.Fall(now, 90); ok {
		t.Fatal("窗口尚未覆盖满, Fall 应返回 ok=false")
	}
}

// 轮询整体停摆后, 最新样本已过期, 不能拿陈旧价格当基准。
func TestNotReadyWhenDataStale(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 24)
	for i := range prices {
		prices[i] = 100
	}
	feed(w, t0, testPoll, prices...)

	now := t0.Add(30 * time.Minute)
	if w.Ready(now) {
		t.Fatal("最新样本已远超容忍度, 不应就绪")
	}
}

// 轮询中断后恢复: 空洞之前的样本虽然还在, 但作为基准已经太老, 必须判为未就绪,
// 否则会拿一个十分钟前的高价去算「5 分钟跌幅」, 得出虚假的暴跌。
func TestNotReadyWhenReferenceTooOld(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	feed(w, t0, testPoll, 100, 100, 100, 100, 100) // T0 .. T0+1m
	feed(w, t0.Add(8*time.Minute), testPoll, 96.5, 96.5)

	// 最新样本在 T0+8m15s; 这里模拟停摆刚结束、时间已推进到 T0+11m
	now := t0.Add(11 * time.Minute)
	if w.Ready(now) {
		t.Fatal("参考价已过老, 不应就绪")
	}
}

// 非正价格会让回撤算成 100%, 必须直接丢弃。
func TestIgnoresNonPositivePrice(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	prices := make([]float64, 25)
	for i := range prices {
		prices[i] = 100
	}
	prices[10] = 0
	prices[11] = -5
	feed(w, t0, testPoll, prices...)

	now := t0.Add(6 * time.Minute)
	fall, ok := w.Fall(now, 96.5)
	if !ok {
		t.Fatal("窗口应已就绪")
	}
	approx(t, fall, 3.5)
}

// 时间未前进的重复/乱序数据必须被丢弃, 否则样本会失去有序性, 参考价选取随之出错。
func TestIgnoresOutOfOrder(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	feed(w, t0, testPoll, 100, 100, 100, 100, 100) // T0 .. T0+1m
	before := w.Len()

	w.Add(t0.Add(time.Minute), 999)    // 与最后一笔同一时刻, 丢弃
	w.Add(t0.Add(30*time.Second), 999) // 早于最后一笔, 丢弃
	if w.Len() != before {
		t.Fatalf("乱序数据不应被接受: 期望 %d 个样本, 实际 %d", before, w.Len())
	}

	w.Add(t0.Add(2*time.Minute), 50) // 正常向前推进, 接受
	if w.Len() != before+1 {
		t.Fatalf("期望 %d 个样本, 实际 %d", before+1, w.Len())
	}
	if got := w.samples[w.Len()-1].price; got != 50 {
		t.Fatalf("最后一笔价格应为 50, 实际 %v", got)
	}
}

// 保留期之外的老样本要被清掉, 保证内存有界。
func TestTrimDropsOldSamples(t *testing.T) {
	w := NewPriceWindow(testWindow, testPoll)
	feed(w, t0, testPoll, 100, 100, 100, 100, 100)
	if w.Len() != 5 {
		t.Fatalf("应有 5 个样本, 实际 %d", w.Len())
	}
	// 跳到很久以后灌一个样本, 之前的应当全部过期
	w.Add(t0.Add(2*time.Hour), 100)
	if w.Len() != 1 {
		t.Fatalf("过期样本应被清理, 实际剩 %d 个", w.Len())
	}
}
