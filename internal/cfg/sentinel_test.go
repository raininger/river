package cfg

import (
	"strings"
	"testing"
	"time"
)

// validSentinel 返回一份能通过校验的哨兵配置。
func validSentinel() Sentinel {
	return Sentinel{
		Config: Config{
			APIKey:    "k",
			APISecret: "s",
			BotToken:  "t",
			ChatID:    "c",
		},
		PollInterval:       15 * time.Second,
		DropWindow:         time.Hour,
		DropPct:            3.0,
		MinProfitPct:       0,
		MaxOrdersPerCycle:  10,
		OrderMinGap:        150 * time.Millisecond,
		AlertCooldown:      30 * time.Minute,
		Overrides:          map[string]float64{"ETHUSDT": 2.0},
		ReboundOverrides:   map[string]float64{"DOGEUSDT": 4.0},
		ReboundPct:         0,
		ReboundFallbackPct: 2.0,
		VolMult:            2.0,
		VolLookback:        7 * 24 * time.Hour,
		VolFloorPct:        0.5,
	}
}

func TestParseOverrides(t *testing.T) {
	got, err := parseOverrides("OVERRIDES", " ethusdt:2.0 , DOGEUSDT:8 ,, ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应解析出 2 项, 实际 %d 项: %v", len(got), got)
	}
	// symbol 统一转大写, 便于和 Bybit 返回的 symbol 直接比较
	if got["ETHUSDT"] != 2.0 {
		t.Fatalf("ETHUSDT 阈值应为 2.0, 实际 %v", got["ETHUSDT"])
	}
	if got["DOGEUSDT"] != 8.0 {
		t.Fatalf("DOGEUSDT 阈值应为 8.0, 实际 %v", got["DOGEUSDT"])
	}
}

func TestParseOverridesEmpty(t *testing.T) {
	got, err := parseOverrides("OVERRIDES", "")
	if err != nil {
		t.Fatalf("空值不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空值应解析为空 map, 实际 %v", got)
	}
}

func TestParseOverridesInvalid(t *testing.T) {
	for _, in := range []string{
		"ETHUSDT",     // 缺少冒号和阈值
		"ETHUSDT:",    // 阈值为空
		":2.0",        // 缺少品种
		"ETHUSDT:abc", // 阈值不是数字
	} {
		if _, err := parseOverrides("OVERRIDES", in); err == nil {
			t.Fatalf("%q 应解析失败, 但通过了", in)
		}
	}
}

// 窗口内采样点太少时, 窗口最高价只由寥寥几个点构成, 回撤会被系统性高估, 必须拒绝启动。
func TestValidateRejectsTooFewSamplesPerWindow(t *testing.T) {
	s := validSentinel()
	s.PollInterval = 30 * time.Second
	s.DropWindow = time.Minute // 只有 2 个采样点

	err := s.Validate()
	if err == nil {
		t.Fatal("应因窗口内采样点太少而报错")
	}
	if !strings.Contains(err.Error(), "DROP_WINDOW") {
		t.Fatalf("错误信息应指明是 DROP_WINDOW 的问题, 实际: %v", err)
	}
}

func TestValidateAcceptsDefaultConfig(t *testing.T) {
	if err := validSentinel().Validate(); err != nil {
		t.Fatalf("默认配置应通过校验, 实际: %v", err)
	}
}

func TestValidateRejectsBadThresholds(t *testing.T) {
	cases := map[string]func(*Sentinel){
		"DROP_PCT 为 0":       func(s *Sentinel) { s.DropPct = 0 },
		"DROP_PCT 为负":        func(s *Sentinel) { s.DropPct = -1 },
		"MIN_PROFIT_PCT 为负":  func(s *Sentinel) { s.MinProfitPct = -0.5 },
		"单轮上限为 0":            func(s *Sentinel) { s.MaxOrdersPerCycle = 0 },
		"下单间隔为负":             func(s *Sentinel) { s.OrderMinGap = -time.Second },
		"覆盖阈值为 0":            func(s *Sentinel) { s.Overrides = map[string]float64{"X": 0} },
		"缺少凭据":               func(s *Sentinel) { s.APIKey = "" },
		"DROP_WINDOW 超过接口上限": func(s *Sentinel) { s.DropWindow = 1001 * time.Minute },
		"VOL_MULT 为 0":       func(s *Sentinel) { s.VolMult = 0 },
		"VOL_LOOKBACK 太短":    func(s *Sentinel) { s.VolLookback = time.Hour },
		"VOL_LOOKBACK 超过上限":  func(s *Sentinel) { s.VolLookback = 1001 * time.Hour },
		"VOL_FLOOR_PCT 为负":   func(s *Sentinel) { s.VolFloorPct = -1 },
		"REBOUND_PCT 为负":     func(s *Sentinel) { s.ReboundPct = -1 },
		"兜底 B 为 0":           func(s *Sentinel) { s.ReboundFallbackPct = 0 },
		"B 的覆盖阈值为 0":         func(s *Sentinel) { s.ReboundOverrides = map[string]float64{"X": 0} },
	}
	for name, mutate := range cases {
		s := validSentinel()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: 应当报错但通过了", name)
		}
	}
}

// 逐品种覆盖优先于全局阈值, 且 symbol 大小写不敏感。
func TestDropPctFor(t *testing.T) {
	s := validSentinel()
	s.DropPct = 3.0
	s.Overrides = map[string]float64{"ETHUSDT": 2.0}

	if got := s.DropPctFor("ETHUSDT"); got != 2.0 {
		t.Fatalf("ETHUSDT 应取覆盖值 2.0, 实际 %v", got)
	}
	if got := s.DropPctFor("ethusdt"); got != 2.0 {
		t.Fatalf("symbol 比较应不区分大小写, 实际 %v", got)
	}
	if got := s.DropPctFor("BTCUSDT"); got != 3.0 {
		t.Fatalf("无覆盖的品种应取全局值 3.0, 实际 %v", got)
	}
}

// B 的优先级: 逐品种覆盖 > 全局固定值 > 波动率换算(下限) > 兜底值。
func TestReboundForPrecedence(t *testing.T) {
	s := validSentinel()
	s.ReboundPct = 0
	s.VolMult = 2.0
	s.VolFloorPct = 0.5
	s.ReboundFallbackPct = 2.0
	s.ReboundOverrides = map[string]float64{"DOGEUSDT": 4.0}

	// 逐品种覆盖最优先, 与波动率无关
	if got := s.ReboundFor("DOGEUSDT", 0.05); got != 4.0 {
		t.Fatalf("有覆盖的品种应取 4.0, 实际 %v", got)
	}
	// symbol 大小写不敏感
	if got := s.ReboundFor("dogeusdt", 0.05); got != 4.0 {
		t.Fatalf("symbol 比较应不区分大小写, 实际 %v", got)
	}
	// 无覆盖: B = VOL_MULT × σ × 100 = 2 × 0.01 × 100 = 2.0
	if got := s.ReboundFor("BTCUSDT", 0.01); got != 2.0 {
		t.Fatalf("应取波动率换算值 2.0, 实际 %v", got)
	}
	// σ 只有 0.1% 时换算出来 0.2%, 应被下限抬到 0.5%
	if got := s.ReboundFor("BTCUSDT", 0.001); got != 0.5 {
		t.Fatalf("应被下限抬到 0.5, 实际 %v", got)
	}
	// 波动率拿不到(K 线不足或接口失败) → 兜底值
	if got := s.ReboundFor("BTCUSDT", 0); got != 2.0 {
		t.Fatalf("波动率不可得时应取兜底值 2.0, 实际 %v", got)
	}
	// 设了全局固定值就压过波动率换算
	s.ReboundPct = 1.5
	if got := s.ReboundFor("BTCUSDT", 0.01); got != 1.5 {
		t.Fatalf("设了全局固定值应取 1.5, 实际 %v", got)
	}
	// 但压不过逐品种覆盖
	if got := s.ReboundFor("DOGEUSDT", 0.01); got != 4.0 {
		t.Fatalf("逐品种覆盖应仍优先, 实际 %v", got)
	}
}

func TestEnvParsersRejectGarbage(t *testing.T) {
	t.Setenv("TEST_DUR", "not-a-duration")
	if _, err := durEnv("TEST_DUR", time.Second); err == nil {
		t.Error("非法时长应报错")
	}

	t.Setenv("TEST_FLOAT", "abc")
	if _, err := floatEnv("TEST_FLOAT", 1); err == nil {
		t.Error("非法数字应报错")
	}

	t.Setenv("TEST_INT", "1.5")
	if _, err := intEnv("TEST_INT", 1); err == nil {
		t.Error("非法整数应报错")
	}
}

func TestEnvParsersUseDefaultWhenUnset(t *testing.T) {
	if v, err := durEnv("TEST_UNSET_DUR", 7*time.Second); err != nil || v != 7*time.Second {
		t.Fatalf("未设置时应返回默认值, 实际 %v err=%v", v, err)
	}
	if v, err := floatEnv("TEST_UNSET_FLOAT", 2.5); err != nil || v != 2.5 {
		t.Fatalf("未设置时应返回默认值, 实际 %v err=%v", v, err)
	}
	if v, err := intEnv("TEST_UNSET_INT", 9); err != nil || v != 9 {
		t.Fatalf("未设置时应返回默认值, 实际 %v err=%v", v, err)
	}
}
