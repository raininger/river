package sentinel

import (
	"testing"
	"time"
)

const (
	testArmPct     = 3.0 // A
	testReboundPct = 2.0 // B
	testMinProfit  = 0.0
)

type step struct {
	price  float64
	fall   float64 // 现价相对窗口最高价的回撤
	profit float64 // 浮盈占仓位的百分比
}

// drive 用一串采样依次推进状态机, 返回每一步是否触发。
// 回撤由调用方给出而不是从价格推, 是为了让测试专注于状态机本身。
func drive(st *symbolState, steps ...step) []bool {
	out := make([]bool, len(steps))
	for i, s := range steps {
		out[i] = st.step(t0.Add(time.Duration(i)*time.Minute), s.price, s.fall, s.profit,
			testArmPct, testReboundPct, testMinProfit)
	}
	return out
}

// 回撤够 A 且浮盈达标 → 武装; 之后自最低点反弹够 B → 触发。
func TestArmsThenTriggersOnRebound(t *testing.T) {
	st := &symbolState{}
	got := drive(st,

		step{price: 100, fall: 0.5, profit: 1},  // 回撤不够, 不武装
		step{price: 96, fall: 4.0, profit: 4},   // 武装, low=96
		step{price: 94, fall: 6.0, profit: 6},   // 创新低, low=94
		step{price: 95.5, fall: 4.5, profit: 5}, // 自 94 反弹 1.60%, 不到 2%
		step{price: 95.9, fall: 4.1, profit: 5}, // 自 94 反弹 2.02%, 触发
	)

	for i, want := range []bool{false, false, false, false, true} {
		if got[i] != want {
			t.Fatalf("第 %d 步触发=%v, 期望 %v (状态: %+v)", i, got[i], want, *st)
		}
	}
}

// 回撤够但浮亏的仓位绝不能武装——浮盈是必要条件。
func TestDoesNotArmWithoutProfit(t *testing.T) {
	st := &symbolState{}
	drive(st,
		step{price: 100, fall: 8, profit: -3})
	if st.armed {
		t.Fatal("浮亏的仓位不应被武装")
	}
}

// 武装当轮不能立刻触发, 否则等于「跌够了就直接平」, 回撤止盈就退化成追跌了。
func TestDoesNotTriggerOnArmingCycle(t *testing.T) {
	st := &symbolState{}
	if tr := st.step(t0, 100, 9.0, 5, testArmPct, testReboundPct, testMinProfit); tr {
		t.Fatal("武装当轮不应触发")
	}
	if !st.armed || st.low != 100 {
		t.Fatalf("应已武装且低点为 100, 实际 %+v", *st)
	}
}

// low 是「自武装以来」的最低点, 不随窗口滑动而上移。
func TestLowOnlyMovesDown(t *testing.T) {
	st := &symbolState{}
	drive(st,

		step{price: 100, fall: 4, profit: 5}, // 武装, low=100
		step{price: 90, fall: 12, profit: 15},
		step{price: 95, fall: 8, profit: 12},
		step{price: 92, fall: 10, profit: 13}, // 没有创新低
	)
	if st.low != 90 {
		t.Fatalf("低点应保持 90, 实际 %v", st.low)
	}
}

// 浮盈跌破门槛 → 解除武装; 之后可以按新的暴跌重新武装。
func TestDisarmsWhenProfitLost(t *testing.T) {
	st := &symbolState{}
	drive(st,

		step{price: 100, fall: 5, profit: 6},
		step{price: 110, fall: 0, profit: -2}, // 涨回去把浮盈抹平
	)
	if st.armed {
		t.Fatal("浮盈跌破门槛后应解除武装")
	}
	if st.low != 0 {
		t.Fatalf("解除武装后低点应清零, 实际 %v", st.low)
	}

	// 之后又跌了一场, 应能重新武装
	drive(st,
		step{price: 95, fall: 6, profit: 4})
	if !st.armed || st.low != 95 {
		t.Fatalf("应重新武装且低点为 95, 实际 %+v", *st)
	}
}

// 武装中浮盈跌破门槛时也不允许触发: 那一轮既要解除武装, 也不能平仓。
func TestNoTriggerOnTheCycleProfitIsLost(t *testing.T) {
	st := &symbolState{}
	drive(st,

		step{price: 100, fall: 5, profit: 6},
		step{price: 90, fall: 15, profit: 10},
	)
	// 价格自 90 反弹远超 B, 但浮盈转负
	if tr := st.step(t0.Add(time.Hour), 95, 10, -1, testArmPct, testReboundPct, testMinProfit); tr {
		t.Fatal("浮盈跌破门槛的那一轮不应触发")
	}
	if st.armed {
		t.Fatal("应已解除武装")
	}
}

// 反弹幅度按最低点算, 而不是按武装价算: 这一点决定了平仓价是否合理。
func TestReboundMeasuredFromLowNotArmPrice(t *testing.T) {
	st := &symbolState{}
	drive(st,

		step{price: 100, fall: 3, profit: 5},  // 武装于 100
		step{price: 80, fall: 20, profit: 20}, // 一路跌到 80
	)
	// 自 80 反弹 2% 就是 81.6; 若按武装价 100 算, 81.6 相对 100 还是亏的
	if tr := st.step(t0.Add(time.Hour), 81.7, 18, 18, testArmPct, testReboundPct, testMinProfit); !tr {
		t.Fatalf("自最低点 80 反弹 2.1%% 应触发, 实际未触发 (反弹 %.2f%%)", st.rebound(81.7))
	}
}

// 逐品种阈值确实生效: B 更宽时不触发, A 更严时不武装。
func TestPerSymbolThresholds(t *testing.T) {
	st := &symbolState{}
	// A=3 时不武装
	st.step(t0, 100, 2.5, 5, testArmPct, testReboundPct, testMinProfit)
	if st.armed {
		t.Fatal("回撤 2.5% 未达 A=3%, 不应武装")
	}
	// A=2 时武装
	st.step(t0, 100, 2.5, 5, 2.0, testReboundPct, testMinProfit)
	if !st.armed {
		t.Fatal("回撤 2.5% 达到 A=2%, 应武装")
	}
	// 自 100 反弹 2% 恰好够 B=2
	if tr := st.step(t0, 102, 0, 5, 2.0, testReboundPct, testMinProfit); !tr {
		t.Fatal("反弹恰好 2% 应触发")
	}
	// 同样的反弹, B=3 时不够
	st2 := &symbolState{}
	st2.step(t0, 100, 5, 5, 2.0, 3.0, testMinProfit)
	if tr := st2.step(t0, 102.5, 0, 5, 2.0, 3.0, testMinProfit); tr {
		t.Fatal("反弹 2.5% 未达 B=3%, 不应触发")
	}
}

// 浮盈门槛大于 0 时, 只有确实赚够了才武装。
func TestMinProfitGate(t *testing.T) {
	st := &symbolState{}
	st.step(t0, 100, 5, 0.5, testArmPct, testReboundPct, 1.0)
	if st.armed {
		t.Fatal("浮盈 0.5% 未达门槛 1.0%, 不应武装")
	}
	st.step(t0, 100, 5, 1.5, testArmPct, testReboundPct, 1.0)
	if !st.armed {
		t.Fatal("浮盈 1.5% 达到门槛 1.0%, 应武装")
	}
}
