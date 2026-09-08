package monitor

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"bybit-position-monitor/internal/bybit"
	"bybit-position-monitor/internal/cfg"
	"bybit-position-monitor/internal/tg"
)

const moveThreshold = 10.0

type Monitor struct {
	cfg cfg.Config
	bc  *bybit.Client
	tg  *tg.Bot
	now func() time.Time
}

type hit struct {
	pos       bybit.Position
	prevClose float64
	yestClose float64
	deltaPct  float64
}

func New(c cfg.Config) *Monitor {
	return &Monitor{
		cfg: c,
		bc:  bybit.New(c.APIKey, c.APISecret, c.Testnet),
		tg:  tg.New(c.BotToken, c.ChatID),
		now: time.Now,
	}
}

func (m *Monitor) RunOnce(ctx context.Context) error {
	now := m.now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	from := utcMidnight(now.AddDate(0, 0, -2))
	to := utcMidnight(now).Add(-time.Millisecond)

	var allHits []hit
	totalPos := 0
	sumPnlAll := 0.0

	for _, sc := range []struct {
		cat   string
		settle string
	}{
		{cat: "linear", settle: "USDT"},
		{cat: "linear", settle: "USDC"},
		{cat: "inverse"},
	} {
		positions, err := m.bc.Positions(ctx, sc.cat, sc.settle)
		if err != nil {
			return fmt.Errorf("获取 %s(%s) 持仓失败: %w", sc.cat, sc.settle, err)
		}
		for _, p := range positions {
			if sizeF(p.Size) == 0 {
				continue
			}
			totalPos++
			sumPnlAll += floatOf(p.UnrealisedPnl)

			candles, err := m.bc.DailyCandles(ctx, sc.cat, p.Symbol, from, to)
			if err != nil {
				log.Printf("获取 %s 日线失败: %v", p.Symbol, err)
				continue
			}
			if len(candles) < 2 {
				log.Printf("%s 日线不足(得到 %d 根), 跳过", p.Symbol, len(candles))
				continue
			}
			yestClose, okY := closeAt(candles, utcMidnight(yesterday).UnixMilli())
			prevClose, okP := closeAt(candles, from.UnixMilli())
			if !okY || !okP || prevClose <= 0 {
				log.Printf("%s 缺少昨日/前日日线, 跳过", p.Symbol)
				continue
			}
			delta := (yestClose - prevClose) / prevClose * 100
			log.Printf("持仓 %s(%s) size=%s 前日收盘=%.4f 昨日收盘=%.4f 昨日涨跌=%+.2f%%",
				p.Symbol, sc.cat, p.Size, prevClose, yestClose, delta)
			if math.Abs(delta) > moveThreshold {
				allHits = append(allHits, hit{pos: p, prevClose: prevClose, yestClose: yestClose, deltaPct: delta})
			}
		}
	}

	sort.Slice(allHits, func(i, j int) bool {
		return math.Abs(allHits[i].deltaPct) > math.Abs(allHits[j].deltaPct)
	})

	var sumPnlHits float64
	for _, h := range allHits {
		sumPnlHits += floatOf(h.pos.UnrealisedPnl)
	}

	dateStr := fmt.Sprintf("%04d-%02d-%02d", yesterday.Year(), yesterday.Month(), yesterday.Day())
	text := buildReport(dateStr, totalPos, sumPnlAll, allHits, sumPnlHits)
	log.Printf("扫描完成: 持仓=%d, 异动(>±%.0f%%)=%d, 发送 Telegram...", totalPos, moveThreshold, len(allHits))
	if err := m.tg.Send(ctx, text); err != nil {
		return fmt.Errorf("发送 Telegram 失败: %w", err)
	}
	return nil
}

func buildReport(dateStr string, totalPos int, sumPnlAll float64, hits []hit, sumPnlHits float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bybit 永续持仓日报\n统计日期(UTC): %s\n", dateStr)
	fmt.Fprintf(&b, "扫描持仓: %d 个品种 | 昨日涨跌超 ±%.0f%%: %d\n\n", totalPos, moveThreshold, len(hits))

	if len(hits) == 0 {
		fmt.Fprintf(&b, "今日无品种昨日涨跌幅超过 ±%.0f%%。\n", moveThreshold)
		fmt.Fprintf(&b, "全账户持仓未实现盈亏: %s %s\n", signedMoney(sumPnlAll), "USDT")
		return b.String()
	}

	b.WriteString("———— 异动品种概览 ————\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "%2d) %-12s %s  %s\n", i+1, h.pos.Symbol, sideLabel(h.pos.Side), pctStr(h.deltaPct))
	}

	b.WriteString("\n———— 持仓明细 ————\n")
	for i, h := range hits {
		p := h.pos
		avg := floatOf(p.AvgPrice)
		size := sizeF(p.Size)
		pnl := floatOf(p.UnrealisedPnl)
		unit := settleUnit(p.Symbol)
		fmt.Fprintf(&b, "\n%d) %s [%s]\n", i+1, p.Symbol, sideLabel(p.Side))
		fmt.Fprintf(&b, "   持仓量: %s | 杠杆: %sx\n", numTrim(size), numTrim(floatOf(p.Leverage)))
		fmt.Fprintf(&b, "   开仓均价: %s\n", moneyTrim(avg))
		fmt.Fprintf(&b, "   昨日收盘: %s\n", moneyTrim(h.yestClose))
		fmt.Fprintf(&b, "   当前标记价: %s\n", moneyTrim(floatOf(p.MarkPrice)))
		fmt.Fprintf(&b, "   昨日涨跌: %s (%s)\n", pctStr(h.deltaPct), upDownLabel(h.deltaPct))
		fmt.Fprintf(&b, "   未实现盈亏: %s %s\n", signedMoney(pnl), unit)
		if r, ok := marginReturn(p, pnl); ok {
			fmt.Fprintf(&b, "   持仓收益率(基于保证金): %s%%\n", numTrim(r))
		}
	}

	fmt.Fprintf(&b, "\n———— 汇总 ————\n")
	fmt.Fprintf(&b, "异动持仓合计未实现盈亏: %s USDT\n", signedMoney(sumPnlHits))
	fmt.Fprintf(&b, "全账户持仓未实现盈亏: %s USDT\n", signedMoney(sumPnlAll))
	return b.String()
}

func utcMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func closeAt(candles []bybit.Candle, openMillis int64) (float64, bool) {
	want := strconv.FormatInt(openMillis, 10)
	for _, c := range candles {
		if c.OpenTime == want {
			return floatOf(c.Close), c.Close != ""
		}
	}
	return 0, false
}

func sideLabel(side string) string {
	if strings.EqualFold(side, "Buy") {
		return "做多"
	}
	return "做空"
}

func upDownLabel(delta float64) string {
	if delta > 0 {
		return "上涨"
	}
	return "下跌"
}

func pctStr(d float64) string {
	return fmt.Sprintf("%+.2f%%", d)
}

func marginReturn(p bybit.Position, pnl float64) (float64, bool) {
	margin := floatOf(p.PositionIM)
	if margin <= 0 {
		posVal := floatOf(p.PositionValue)
		lev := floatOf(p.Leverage)
		if lev > 0 && posVal > 0 {
			margin = posVal / lev
		}
	}
	if margin <= 0 {
		return 0, false
	}
	return pnl / margin * 100, true
}

func settleUnit(symbol string) string {
	for _, q := range []string{"USDT", "USDC"} {
		if strings.HasSuffix(symbol, q) {
			return q
		}
	}
	if strings.HasSuffix(symbol, "USD") {
		return strings.TrimSuffix(symbol, "USD")
	}
	return ""
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
