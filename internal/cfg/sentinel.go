package cfg

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"bybit-position-monitor/internal/bybit"
)

// Sentinel 是急跌止盈哨兵的配置。凭据沿用 Config, 但哨兵的 key 需要
// 「合约-交易」权限, 与只读的日报 key 分开放在各自的 .env 里。
type Sentinel struct {
	Config

	// DryRun 只告警不下单。由命令行 -dry-run 控制, 默认 true。
	DryRun bool

	// PollInterval 轮询持仓的间隔。
	PollInterval time.Duration
	// DropWindow 是衡量回撤的回看窗口。
	DropWindow time.Duration
	// DropPct 是武装阈值 A(%): 现价相对 DropWindow 内最高价回撤多少算一场暴跌。
	DropPct float64
	// MinProfitPct 是浮盈门槛(%), 0 表示只要浮盈为正即可。
	MinProfitPct float64
	// MaxOrdersPerCycle 单轮最多下多少个平仓单, 超出的留到下一轮。
	MaxOrdersPerCycle int
	// OrderMinGap 相邻两个平仓单之间的最小间隔, 用于规避下单限频。
	OrderMinGap time.Duration
	// AlertCooldown 是 dry-run 模式下同一品种的告警去重窗口。
	AlertCooldown time.Duration

	// Overrides 是按品种覆盖的武装阈值 A(%), key 为大写 symbol。
	Overrides map[string]float64

	// ReboundOverrides 是按品种固定的反弹阈值 B(%), key 为大写 symbol。
	ReboundOverrides map[string]float64
	// ReboundPct 是全局固定的反弹阈值 B(%)。为 0 表示由波动率自动计算,
	// 这也是默认行为——100+ 个品种的波动率能差一个数量级, 一个固定值必然
	// 在某些品种上过紧(被噪音甩出去)、在另一些上过松(回吐太多)。
	ReboundPct float64
	// ReboundFallbackPct 是波动率不可得时(新上市 K 线不足、接口失败)的兜底 B。
	ReboundFallbackPct float64
	// VolMult 是自动模式下 B 的倍数: B = VolMult × 单根 K 线的收益率标准差。
	VolMult float64
	// VolLookback 是估计波动率的回看时长, 用 1 小时 K 线。
	VolLookback time.Duration
	// VolFloorPct 是 B 的下限, 防止做市/稳定币这类极低波动品种触发过紧。
	VolFloorPct float64
}

// DropPctFor 返回某个品种适用的武装阈值 A: 优先取逐品种覆盖, 否则用全局默认值。
func (s Sentinel) DropPctFor(symbol string) float64 {
	if v, ok := s.Overrides[strings.ToUpper(symbol)]; ok {
		return v
	}
	return s.DropPct
}

// ReboundFor 返回某个品种适用的反弹阈值 B(%)。优先级:
//
//	逐品种覆盖 > 全局固定值 > 波动率换算(下限 VolFloorPct) > 兜底值
//
// vol 是该品种单根小时 K 线的对数收益率标准差; 取不到时为 0, 走兜底值。
func (s Sentinel) ReboundFor(symbol string, vol float64) float64 {
	if v, ok := s.ReboundOverrides[strings.ToUpper(symbol)]; ok {
		return v
	}
	if s.ReboundPct > 0 {
		return s.ReboundPct
	}
	if vol <= 0 {
		return s.ReboundFallbackPct
	}
	b := s.VolMult * vol * 100
	if b < s.VolFloorPct {
		return s.VolFloorPct
	}
	return b
}

func LoadSentinel(envFile string) (Sentinel, error) {
	loadDotEnv(envFile)

	s := Sentinel{
		Config: Config{
			APIKey:    os.Getenv("BYBIT_API_KEY"),
			APISecret: os.Getenv("BYBIT_API_SECRET"),
			Testnet:   boolEnv(os.Getenv("BYBIT_TESTNET")),
			BotToken:  os.Getenv("TG_BOT_TOKEN"),
			ChatID:    os.Getenv("TG_CHAT_ID"),
		},
	}

	var err error
	if s.PollInterval, err = durEnv("POLL_INTERVAL", 15*time.Second); err != nil {
		return s, err
	}
	if s.DropWindow, err = durEnv("DROP_WINDOW", time.Hour); err != nil {
		return s, err
	}
	if s.DropPct, err = floatEnv("DROP_PCT", 3.0); err != nil {
		return s, err
	}
	if s.MinProfitPct, err = floatEnv("MIN_PROFIT_PCT", 0); err != nil {
		return s, err
	}
	if s.MaxOrdersPerCycle, err = intEnv("MAX_ORDERS_PER_CYCLE", 10); err != nil {
		return s, err
	}
	if s.OrderMinGap, err = durEnv("ORDER_MIN_GAP", 150*time.Millisecond); err != nil {
		return s, err
	}
	if s.AlertCooldown, err = durEnv("ALERT_COOLDOWN", 30*time.Minute); err != nil {
		return s, err
	}
	if s.ReboundPct, err = floatEnv("REBOUND_PCT", 0); err != nil {
		return s, err
	}
	if s.ReboundFallbackPct, err = floatEnv("REBOUND_FALLBACK_PCT", 2.0); err != nil {
		return s, err
	}
	if s.VolMult, err = floatEnv("VOL_MULT", 2.0); err != nil {
		return s, err
	}
	if s.VolLookback, err = durEnv("VOL_LOOKBACK", 7*24*time.Hour); err != nil {
		return s, err
	}
	if s.VolFloorPct, err = floatEnv("VOL_FLOOR_PCT", 0.5); err != nil {
		return s, err
	}
	if s.Overrides, err = parseOverrides("OVERRIDES", os.Getenv("OVERRIDES")); err != nil {
		return s, err
	}
	if s.ReboundOverrides, err = parseOverrides("REBOUND_OVERRIDES", os.Getenv("REBOUND_OVERRIDES")); err != nil {
		return s, err
	}
	return s, nil
}

// Validate 校验配置。宁可在启动时就报错退出, 也不要带着错配置去动真钱。
func (s Sentinel) Validate() error {
	if err := s.Config.Validate(); err != nil {
		return err
	}
	if s.PollInterval <= 0 {
		return fmt.Errorf("POLL_INTERVAL 必须大于 0")
	}
	if s.DropWindow < 4*s.PollInterval {
		return fmt.Errorf(
			"DROP_WINDOW(%s) 至少要达到 POLL_INTERVAL(%s) 的 4 倍: 窗口内采样点太少时, "+
				"窗口最高价只由寥寥几个点构成, 回撤幅度会被系统性高估",
			s.DropWindow, s.PollInterval)
	}
	if s.DropWindow > bybit.MaxKlineLimit*time.Minute {
		return fmt.Errorf(
			"DROP_WINDOW(%s) 超过 %d 分钟: 启动回补用的是 1 分钟 K 线, 一次最多只能拉 %d 根, "+
				"更长的窗口永远填不满, 也就永远不会武装",
			s.DropWindow, bybit.MaxKlineLimit, bybit.MaxKlineLimit)
	}
	if s.DropPct <= 0 {
		return fmt.Errorf("DROP_PCT 必须大于 0, 当前为 %g", s.DropPct)
	}
	if s.MinProfitPct < 0 {
		return fmt.Errorf("MIN_PROFIT_PCT 不能为负, 当前为 %g", s.MinProfitPct)
	}
	if s.MaxOrdersPerCycle < 1 {
		return fmt.Errorf("MAX_ORDERS_PER_CYCLE 至少为 1")
	}
	if s.OrderMinGap < 0 {
		return fmt.Errorf("ORDER_MIN_GAP 不能为负")
	}
	if s.AlertCooldown < 0 {
		return fmt.Errorf("ALERT_COOLDOWN 不能为负")
	}
	if s.VolMult <= 0 {
		return fmt.Errorf("VOL_MULT 必须大于 0, 当前为 %g", s.VolMult)
	}
	if s.VolLookback < 24*time.Hour {
		return fmt.Errorf("VOL_LOOKBACK(%s) 至少要 24h: 用更短的样本估波动率, 标准差本身就不稳定", s.VolLookback)
	}
	if s.VolLookback > bybit.MaxKlineLimit*time.Hour {
		return fmt.Errorf("VOL_LOOKBACK(%s) 超过 %dh: 1 小时 K 线一次最多只能拉 %d 根",
			s.VolLookback, bybit.MaxKlineLimit, bybit.MaxKlineLimit)
	}
	if s.VolFloorPct < 0 {
		return fmt.Errorf("VOL_FLOOR_PCT 不能为负, 当前为 %g", s.VolFloorPct)
	}
	if s.ReboundPct < 0 {
		return fmt.Errorf("REBOUND_PCT 不能为负(0 表示由波动率自动计算), 当前为 %g", s.ReboundPct)
	}
	if s.ReboundFallbackPct <= 0 {
		return fmt.Errorf("REBOUND_FALLBACK_PCT 必须大于 0, 当前为 %g", s.ReboundFallbackPct)
	}
	if err := checkPositive(s.Overrides, "OVERRIDES"); err != nil {
		return err
	}
	return checkPositive(s.ReboundOverrides, "REBOUND_OVERRIDES")
}

func checkPositive(m map[string]float64, key string) error {
	for sym, v := range m {
		if v <= 0 {
			return fmt.Errorf("%s 中 %s 的阈值必须大于 0, 当前为 %g", key, sym, v)
		}
	}
	return nil
}

// parseOverrides 解析 "ETHUSDT:2.0,DOGEUSDT:8.0" 形式的逐品种阈值覆盖。
// key 只用于报错信息。
func parseOverrides(key, raw string) (map[string]float64, error) {
	m := make(map[string]float64)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.LastIndex(part, ":")
		if i <= 0 || i == len(part)-1 {
			return nil, fmt.Errorf("%s 中的 %q 格式应为 SYMBOL:阈值, 例如 ETHUSDT:2.0", key, part)
		}
		sym := strings.ToUpper(strings.TrimSpace(part[:i]))
		v, err := strconv.ParseFloat(strings.TrimSpace(part[i+1:]), 64)
		if err != nil {
			return nil, fmt.Errorf("%s 中 %s 的阈值无法解析: %w", key, sym, err)
		}
		m[sym] = v
	}
	return m, nil
}

func durEnv(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return def, fmt.Errorf("%s 无法解析为时长(如 15s/5m/1h): %w", key, err)
	}
	return v, nil
}

func floatEnv(key string, def float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def, fmt.Errorf("%s 无法解析为数字: %w", key, err)
	}
	return v, nil
}

func intEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def, fmt.Errorf("%s 无法解析为整数: %w", key, err)
	}
	return v, nil
}
