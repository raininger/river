package sentinel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"bybit-position-monitor/internal/bybit"
	"bybit-position-monitor/internal/cfg"
	"bybit-position-monitor/internal/numfmt"
	"bybit-position-monitor/internal/tg"
)

const (
	// retryInterval 实盘模式下同一品种两次下单尝试之间的最小间隔。
	// 用它而不是长冷却, 是因为「仓位还在就该继续平」才是正确行为。
	retryInterval = time.Minute
	// maxFailures 同一品种连续下单失败多少次后放弃, 避免无限重试打爆接口。
	maxFailures = 5
	// maxListed 单条 Telegram 消息里最多列出多少个品种。
	maxListed = 20
	// rateLimitRetries 遇到限频时的额外重试次数。
	rateLimitRetries = 2
	// rateLimitBackoff 限频重试的退避基数, 第 n 次退避 n 倍。
	rateLimitBackoff = time.Second

	// backfillInterval 启动回补使用的 K 线周期(Bybit 的写法, "1" 即 1 分钟)。
	backfillInterval = "1"
	// backfillStep 与 backfillInterval 对应的时间跨度。
	backfillStep = time.Minute

	// volInterval 估计波动率使用的 K 线周期。用小时线而不是日线, 是因为日线一天
	// 才更新一次, 暴跌当天完全感知不到——而那正是最需要反弹阈值变宽的时候。
	volInterval = "60"
	// volStep 与 volInterval 对应的时间跨度。
	volStep = time.Hour
	// volMaxAge 波动率多久重算一次。小时线一小时才出一根, 更频繁没有意义。
	volMaxAge = 90 * time.Minute
	// volFetchPerCycle 单轮最多补算多少个品种的波动率, 把请求摊到多轮,
	// 避免 100+ 个品种同时突发流量。
	volFetchPerCycle = 10

	// warmupPace 回补期间相邻两个请求之间的间隔。启动时每个品种要拉两次 K 线,
	// 100+ 品种不加节流会逼近 IP 限频(600 次/5s)。
	warmupPace = 50 * time.Millisecond

	// warmupLogEvery 回补期间每处理多少个品种打一条进度日志。
	warmupLogEvery = 25
)

// 需要分别查询的持仓范围。linear 有 USDT / USDC 两种结算币, 要各查一次。
var scopes = []struct {
	category string
	settle   string
}{
	{category: "linear", settle: "USDT"},
	{category: "linear", settle: "USDC"},
	{category: "inverse"},
}

type held struct {
	category string
	pos      bybit.Position
}

func (h held) key() string { return h.category + ":" + h.pos.Symbol }

// candidate 是一个「已武装且自最低点反弹够 B」的空单。
type candidate struct {
	category  string
	pos       bybit.Position
	armFall   float64 // 武装当时的回撤幅度(%)
	low       float64 // 自武装以来的最低价
	rebound   float64 // 自最低点的反弹幅度(%)
	threshold float64 // 该品种适用的反弹阈值 B(%)
	profitPct float64 // 浮动盈亏占仓位的百分比
}

// volEntry 是缓存的波动率估计。vol 为 0 表示 K 线不足, 应退回兜底阈值。
type volEntry struct {
	vol float64
	at  time.Time
}

// WarmupResult 汇总启动期回补的结果, 供日志展示。
type WarmupResult struct {
	VolOK        int // 算出波动率的品种数
	VolFallback  int // 退回兜底阈值的品种数(K 线不足或接口失败)
	WindowOK     int // 价格窗口回补成功的品种数
	WindowFailed int // 价格窗口回补失败的品种数
}

type Sentinel struct {
	cfg     cfg.Sentinel
	bc      *bybit.Client
	tg      *tg.Bot
	now     func() time.Time
	verbose bool

	windows map[string]*PriceWindow
	states  map[string]*symbolState
	vols    map[string]volEntry

	lastOrderAt time.Time
}

func New(c cfg.Sentinel) *Sentinel {
	return &Sentinel{
		cfg:     c,
		bc:      bybit.New(c.APIKey, c.APISecret, c.Testnet),
		tg:      tg.New(c.BotToken, c.ChatID),
		now:     time.Now,
		windows: make(map[string]*PriceWindow),
		states:  make(map[string]*symbolState),
		vols:    make(map[string]volEntry),
	}
}

// SetVerbose 打开后会把每个被监控品种的明细打进日志, 适合 -once 冒烟时肉眼核对。
func (s *Sentinel) SetVerbose(v bool) { s.verbose = v }

// SendTelegram 直接发送一条文本, 供启动通告之类的场景使用。
func (s *Sentinel) SendTelegram(ctx context.Context, text string) error {
	return s.tg.Send(ctx, text)
}

func (s *Sentinel) window(key string) *PriceWindow {
	w, ok := s.windows[key]
	if !ok {
		w = NewPriceWindow(s.cfg.DropWindow, s.cfg.PollInterval)
		s.windows[key] = w
	}
	return w
}

func (s *Sentinel) state(key string) *symbolState {
	st, ok := s.states[key]
	if !ok {
		st = &symbolState{}
		s.states[key] = st
	}
	return st
}

// Warmup 做两件启动期的事, 都是为了让进程一上来就能正确判定:
//
//  1. 用 1 小时 K 线算出各空单品种的波动率, 决定反弹阈值 B;
//  2. 用 1 分钟 K 线回补价格窗口, 并重放状态机, 使进程在暴跌中途重启时
//     已经跟踪到的最低点不会丢失。
//
// 连持仓都拉不到时返回 error; 单个品种失败只计数, 不中断整体回补。
func (s *Sentinel) Warmup(ctx context.Context) (WarmupResult, error) {
	var res WarmupResult
	now := s.now()

	held, err := s.positions(ctx)
	if err != nil {
		return res, err
	}

	from := now.Add(-s.cfg.DropWindow - 2*backfillStep)
	limit := s.backfillLimit()

	for i, h := range held {
		if ctx.Err() != nil {
			log.Printf("回补被中断")
			break
		}
		if !isShort(h.pos) {
			continue // 只监控空单, 多单不做任何准备工作
		}

		sleepCtx(ctx, warmupPace)
		vol, err := s.fetchVol(ctx, h, now)
		switch {
		case err != nil:
			res.VolFallback++
			log.Printf("计算 %s 波动率失败, 暂用兜底阈值 %g%%: %v", h.pos.Symbol, s.cfg.ReboundFallbackPct, err)
		case vol <= 0:
			res.VolFallback++
		default:
			s.vols[h.key()] = volEntry{vol: vol, at: now}
			res.VolOK++
		}

		sleepCtx(ctx, warmupPace)
		cs, err := s.bc.Candles(ctx, h.category, h.pos.Symbol, backfillInterval, from, now, limit)
		if err != nil {
			res.WindowFailed++
			log.Printf("回补 %s 价格窗口失败: %v", h.pos.Symbol, err)
			continue
		}
		s.replay(s.state(h.key()), s.window(h.key()), cs, now,
			profitPct(h.pos), s.cfg.DropPctFor(h.pos.Symbol), s.reboundFor(h))
		res.WindowOK++

		if i > 0 && i%warmupLogEvery == 0 {
			log.Printf("回补进度: %d/%d", i, len(held))
		}
	}
	return res, nil
}

// backfillLimit 计算回补所需的 K 线根数, 并夹在接口允许的范围内。
func (s *Sentinel) backfillLimit() int {
	n := int(s.cfg.DropWindow/backfillStep) + 5
	if n > bybit.MaxKlineLimit {
		n = bybit.MaxKlineLimit
	}
	if n < 1 {
		n = 1
	}
	return n
}

// replay 用回补的 1 分钟 K 线推进状态机, 使进程在暴跌中途重启时不会丢失
// 已经跟踪到的最低点——否则会以重启时的价格重新武装, 把一个已经发生的反弹
// 当成新的起点, 导致过早平仓。
//
// 浮盈无法回溯, 这里用当前浮盈做代理: 只有当前仍满足浮盈门槛时才允许历史武装。
// 对一个止盈场景来说浮盈状态基本稳定, 这个近似可以接受。
//
// 重放期间忽略触发结果: 不为历史 K 线下单。若反弹确实已经发生过,
// 紧接着的第一轮运行会立刻触发, 不需要在这里补。
func (s *Sentinel) replay(st *symbolState, w *PriceWindow, candles []bybit.Candle, now time.Time, profit, armPct, reboundPct float64) {
	for _, b := range barSeries(candles, backfillStep, now) {
		w.Add(b.t, b.close)
		fall, ok := w.Fall(b.t, b.close)
		if !ok {
			continue // 窗口尚未覆盖满, 与运行时的就绪判断一致
		}
		st.step(b.t, b.close, fall, profit, armPct, reboundPct, s.cfg.MinProfitPct)
	}
}

// ensureVol 为缺失或过期的品种补算波动率。每轮最多补 volFetchPerCycle 个,
// 把请求摊到多轮, 也顺带覆盖了运行中新开的仓位。
func (s *Sentinel) ensureVol(ctx context.Context, held []held, now time.Time) {
	budget := volFetchPerCycle
	for _, h := range held {
		if budget <= 0 || ctx.Err() != nil {
			return
		}
		if !isShort(h.pos) {
			continue
		}
		if e, ok := s.vols[h.key()]; ok && now.Sub(e.at) < volMaxAge {
			continue
		}
		budget--

		vol, err := s.fetchVol(ctx, h, now)
		if err != nil {
			log.Printf("计算 %s 波动率失败, 暂用兜底阈值 %g%%: %v", h.pos.Symbol, s.cfg.ReboundFallbackPct, err)
			continue
		}
		s.vols[h.key()] = volEntry{vol: vol, at: now}
	}
}

// fetchVol 拉 1 小时 K 线并算出单根收益率的标准差。K 线不足时返回 0 而不是错误。
func (s *Sentinel) fetchVol(ctx context.Context, h held, now time.Time) (float64, error) {
	limit := int(s.cfg.VolLookback/volStep) + 2
	if limit > bybit.MaxKlineLimit {
		limit = bybit.MaxKlineLimit
	}
	cs, err := s.bc.Candles(ctx, h.category, h.pos.Symbol, volInterval, now.Add(-s.cfg.VolLookback), now, limit)
	if err != nil {
		return 0, err
	}
	vol, n := HourlyVol(barSeries(cs, volStep, now), volStep)
	if n < volMinBars {
		log.Printf("%s 可用的 1 小时 K 线只有 %d 根(至少需要 %d 根), 波动率无法估计",
			h.pos.Symbol, n, volMinBars)
	}
	return vol, nil
}

// reboundFor 返回品种适用的反弹阈值 B。波动率拿不到时由 cfg 退回兜底值。
func (s *Sentinel) reboundFor(h held) float64 {
	return s.cfg.ReboundFor(h.pos.Symbol, s.vols[h.key()].vol)
}

// ReboundLabel 概括 B 的来源, 用于启动日志与通知抬头。
func (s *Sentinel) ReboundLabel() string {
	label := fmt.Sprintf("波动率自适应(VOL_MULT=%.2f, 兜底 %.2f%%)", s.cfg.VolMult, s.cfg.ReboundFallbackPct)
	if s.cfg.ReboundPct > 0 {
		label = fmt.Sprintf("固定 %.2f%%", s.cfg.ReboundPct)
	}
	if n := len(s.cfg.ReboundOverrides); n > 0 {
		label = fmt.Sprintf("%s + %d 个品种固定值", label, n)
	}
	return label
}

// sleepCtx 是可被取消打断的休眠。
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// RunOnce 跑一轮: 拉持仓 → 更新价格窗口 → 推进各品种的移动止盈状态机 →
// 筛出触发平仓的 → 按浮盈排序截断 → 执行。
func (s *Sentinel) RunOnce(ctx context.Context) error {
	now := s.now()

	held, err := s.positions(ctx)
	if err != nil {
		return err
	}
	// 波动率要在判定之前补齐, 否则新仓位或过期的缓存会让 B 退回兜底值
	s.ensureVol(ctx, held, now)

	seen := make(map[string]bool, len(held))
	var cands []candidate
	watched, notReady := 0, 0

	for _, h := range held {
		key := h.key()
		seen[key] = true

		price := floatOf(h.pos.MarkPrice)
		if price <= 0 {
			// 脏数据会让回撤算成 100%, 直接跳过
			continue
		}
		w := s.window(key)
		w.Add(now, price)

		if !isShort(h.pos) {
			continue // 只动空单, 多单完全不碰
		}
		watched++

		fall, ready := w.Fall(now, price)
		if !ready {
			notReady++
			if s.verbose {
				log.Printf("空单 %s(%s) 窗口未就绪(样本 %d), 本轮跳过", h.pos.Symbol, h.category, w.Len())
			}
			continue
		}

		st := s.state(key)
		profit := profitPct(h.pos)
		armPct := s.cfg.DropPctFor(h.pos.Symbol)
		reboundPct := s.reboundFor(h)

		trigger := st.step(now, price, fall, profit, armPct, reboundPct, s.cfg.MinProfitPct)

		if s.verbose {
			log.Printf("空单 %-14s %-7s 现价=%-12.6g %v回撤=%s A=%.2f%% B=%.2f%% 浮盈=%s %s",
				h.pos.Symbol, h.category, price, s.cfg.DropWindow,
				numfmt.Pct(fall), armPct, reboundPct, numfmt.Pct(profit), st.describe(price, trigger))
		}
		if !trigger {
			continue
		}
		cands = append(cands, candidate{
			category:  h.category,
			pos:       h.pos,
			armFall:   st.armFall,
			low:       st.low,
			rebound:   st.rebound(price),
			threshold: reboundPct,
			profitPct: profit,
		})
	}

	s.prune(seen)

	triggered := len(cands)
	if triggered == 0 {
		if s.verbose {
			log.Printf("本轮扫描: 持仓 %d 个, 空单 %d 个, 窗口未就绪 %d 个, 触发 0 个", len(held), watched, notReady)
		}
		return nil
	}

	// 先剔除冷却/重试间隔内的品种, 再排序截断。顺序反过来的话, 被上限截掉的
	// 品种会一直占着名额, 后面的品种永远轮不到。
	actionable := cands[:0]
	skipped := 0
	for _, c := range cands {
		if ok, reason := s.readyToAct(now, c.key()); ok {
			actionable = append(actionable, c)
		} else {
			skipped++
			if s.verbose {
				log.Printf("跳过 %s: %s", c.pos.Symbol, reason)
			}
		}
	}
	sort.Slice(actionable, func(i, j int) bool { return actionable[i].profitPct > actionable[j].profitPct })
	if len(actionable) > s.cfg.MaxOrdersPerCycle {
		actionable = actionable[:s.cfg.MaxOrdersPerCycle]
	}

	log.Printf("本轮触发 %d 个空单(另有 %d 个在冷却/重试间隔内), 本轮处理 %d 个",
		triggered, skipped, len(actionable))

	lines := make([]string, 0, len(actionable))
	for _, c := range actionable {
		// 失败详情 act 内部已经记过日志了, 这里只收集要发出去的结果
		if line := s.act(ctx, now, c); line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return nil
	}
	s.notify(ctx, now, triggered, lines)
	return nil
}

func (c candidate) key() string { return c.category + ":" + c.pos.Symbol }

// readyToAct 判断该品种本轮是否可以动手。返回的 reason 仅用于日志。
func (s *Sentinel) readyToAct(now time.Time, key string) (bool, string) {
	st := s.state(key)
	if st.gaveUp {
		return false, "连续失败过多已放弃"
	}
	if s.cfg.DryRun {
		// dry-run 下条件会持续成立, 不去重的话每轮都发一条 Telegram
		if !st.lastAlert.IsZero() && now.Sub(st.lastAlert) < s.cfg.AlertCooldown {
			return false, "告警冷却中"
		}
	} else if !st.lastAttempt.IsZero() && now.Sub(st.lastAttempt) < retryInterval {
		return false, "重试间隔未到"
	}
	return true, ""
}

// act 对单个候选执行 dry-run 告警或真实平仓, 返回要写进 Telegram 的一行。
// 成功与失败的细节都记录在日志里, 返回值只用于拼装通知。
func (s *Sentinel) act(ctx context.Context, now time.Time, c candidate) string {
	st := s.state(c.key())
	label := fmt.Sprintf("%s (%s) 空单", c.pos.Symbol, c.category)
	// 把完整决策依据带上: 事后复盘调参时, 只看「已平仓」是判断不出 B 该收还是该放的
	metrics := fmt.Sprintf("武装回撤 %s | 低点 %.6g | 反弹 %s (阈值 %.2f%%) | 浮盈 %s",
		numfmt.Pct(c.armFall), c.low, numfmt.Pct(c.rebound), c.threshold, numfmt.Pct(c.profitPct))

	if s.cfg.DryRun {
		st.lastAlert = now
		log.Printf("[dry-run] 本应平仓 %s %s qty=%s", label, metrics, c.pos.Size)
		return fmt.Sprintf("%s %s | 本应市价平仓", label, metrics)
	}

	st.lastAttempt = now
	orderID, err := s.placeClose(ctx, c)
	switch {
	case err == nil:
		st.failures = 0
		log.Printf("已平仓 %s %s qty=%s orderId=%s", label, metrics, c.pos.Size, orderID)
		return fmt.Sprintf("%s %s | ✅ 已平仓 orderId=%s", label, metrics, orderID)

	case errors.Is(err, errAlreadyClosed):
		st.failures = 0
		log.Printf("%s 仓位已不存在, 视为已平", label)
		return fmt.Sprintf("%s %s | 仓位已不存在(可能已平), 跳过", label, metrics)

	default:
		st.failures++
		var extra string
		if st.failures >= maxFailures && !st.gaveUp {
			st.gaveUp = true
			extra = fmt.Sprintf(" (连续失败 %d 次, 已放弃自动平仓)", st.failures)
		}
		log.Printf("平仓失败 %s %s: %v%s", label, metrics, err, extra)
		return fmt.Sprintf("%s %s | ❌ 平仓失败: %v%s", label, metrics, err, extra)
	}
}

// errAlreadyClosed 表示仓位已不存在, 不是真正的失败。
var errAlreadyClosed = errors.New("仓位已不存在")

// placeClose 带节流与限频退避地下一个市价平仓单。
func (s *Sentinel) placeClose(ctx context.Context, c candidate) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= rateLimitRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(rateLimitBackoff * time.Duration(attempt)):
			}
			log.Printf("限频退避后重试 %s (第 %d 次)", c.pos.Symbol, attempt)
		}
		s.pace()

		id, err := s.bc.CloseShort(ctx, c.category, c.pos.Symbol, c.pos.Size, c.pos.PositionIdx)
		if err == nil {
			return id, nil
		}
		lastErr = err

		if bybit.IsAlreadyClosed(err) {
			return "", errAlreadyClosed
		}
		if bybit.IsRateLimited(err) {
			continue // 退避后重试
		}
		// 其他错误(权限/数量/余额等)重试也没有意义, 直接返回
		return "", err
	}
	return "", lastErr
}

// pace 保证相邻两个下单请求之间至少间隔 OrderMinGap。
// 官方限频是 10 单/秒/UID, 串行 + 间隔可以留足余量。
func (s *Sentinel) pace() {
	if s.cfg.OrderMinGap <= 0 {
		return
	}
	if d := time.Since(s.lastOrderAt); d < s.cfg.OrderMinGap {
		time.Sleep(s.cfg.OrderMinGap - d)
	}
	s.lastOrderAt = time.Now()
}

// prune 清掉已经不在持仓列表里的品种, 避免状态无限增长。
func (s *Sentinel) prune(seen map[string]bool) {
	for k := range s.windows {
		if !seen[k] {
			delete(s.windows, k)
		}
	}
	for k := range s.states {
		if !seen[k] {
			delete(s.states, k)
		}
	}
	for k := range s.vols {
		if !seen[k] {
			delete(s.vols, k)
		}
	}
}

func (s *Sentinel) notify(ctx context.Context, now time.Time, triggered int, lines []string) {
	text := s.buildMessage(now, triggered, lines)
	if err := s.tg.Send(ctx, text); err != nil {
		// 下单可能已经成功, 不能因为通知失败就让整轮算失败
		log.Printf("发送 Telegram 失败: %v", err)
	}
}

func (s *Sentinel) buildMessage(now time.Time, triggered int, lines []string) string {
	var b strings.Builder
	if s.cfg.DryRun {
		b.WriteString("🔎 回撤止盈触发 (dry-run, 未真实下单)\n")
	} else {
		b.WriteString("✅ 回撤止盈执行结果\n")
	}
	fmt.Fprintf(&b, "时间(UTC): %s\n", now.UTC().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "窗口 %s | 默认 A=%.2f%% | B=%s | 浮盈门槛 %.2f%%\n\n",
		s.cfg.DropWindow, s.cfg.DropPct, s.ReboundLabel(), s.cfg.MinProfitPct)

	shown := lines
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	for i, l := range shown {
		fmt.Fprintf(&b, "%d) %s\n", i+1, l)
	}
	if triggered > len(shown) {
		fmt.Fprintf(&b, "\n本轮共 %d 个品种触发, 以上为 %d 个; 其余会在后续轮次继续处理。\n",
			triggered, len(shown))
	}
	return b.String()
}

// positions 拉取各范围下所有 size > 0 的持仓。100+ 品种仍是常数级请求量,
// 因为 position/list 不传 symbol 时只返回有仓位的记录, 且 limit 可放到 200。
func (s *Sentinel) positions(ctx context.Context) ([]held, error) {
	var out []held
	for _, sc := range scopes {
		ps, err := s.bc.Positions(ctx, sc.category, sc.settle)
		if err != nil {
			return nil, fmt.Errorf("获取 %s(%s) 持仓失败: %w", sc.category, sc.settle, err)
		}
		for _, p := range ps {
			if sizeF(p.Size) == 0 {
				continue
			}
			out = append(out, held{category: sc.category, pos: p})
		}
	}
	return out, nil
}

func isShort(p bybit.Position) bool {
	return strings.EqualFold(p.Side, "Sell")
}

// profitPct 是浮动盈亏占仓位价值的百分比。仓位价值为 0 时无法计算, 返回 0,
// 这样在 MinProfitPct 默认 0 的情况下会被判为「未超过门槛」而跳过。
func profitPct(p bybit.Position) float64 {
	v := sizeF(p.PositionValue)
	if v == 0 {
		return 0
	}
	return floatOf(p.UnrealisedPnl) / v * 100
}

func floatOf(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func sizeF(s string) float64 {
	v := floatOf(s)
	if v < 0 {
		return -v
	}
	return v
}
