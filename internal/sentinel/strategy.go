package sentinel

import (
	"fmt"
	"time"
)

// symbolState 记录单个品种的告警/重试状态与移动止盈状态。
type symbolState struct {
	// ---- 告警与重试去重 ----
	lastAlert   time.Time // dry-run: 上次告警时间
	lastAttempt time.Time // 实盘: 上次下单尝试时间
	failures    int
	gaveUp      bool

	// ---- 移动止盈 ----
	armed   bool      // 是否已武装(回撤够 A 且浮盈达标)
	low     float64   // 自武装以来出现过的最低价
	lowAt   time.Time // 最低价出现的时刻
	armedAt time.Time // 武装时刻
	armFall float64   // 武装当时的回撤幅度(%), 用于通知
}

// step 推进一个品种的移动止盈状态机, 返回本轮是否应当平仓。
//
// fall 是现价相对窗口内最高价的回撤(%, 下跌为正), profit 是浮盈占仓位的百分比,
// 两者都由调用方算好。本方法不碰 I/O, 便于单测整条判定路径。
//
// 状态迁移:
//
//	未武装 --回撤>=A 且 浮盈达标--> 武装(low = 现价)
//	武装   --浮盈跌破门槛---------> 解除武装
//	武装   --现价 < low----------> low = 现价
//	武装   --现价自 low 反弹>=B---> 触发
//
// 做法是「确认反弹」而不是「预测跌不动」: 下跌是阶梯式的, 中途的停顿极具欺骗性,
// 拿来当离场信号会反复卖在半山腰。反弹是已经发生的事实, 不会骗人。
func (st *symbolState) step(now time.Time, price, fall, profit, armPct, reboundPct, minProfitPct float64) bool {
	// 浮盈跌破门槛就解除武装, 而不是保留: 价格如果涨回去把浮盈抹平, 这轮暴跌的
	// 前提就已经消失, 之后再跌会是新的一次暴跌, 从新低重新武装才是对的。
	// 这同时保证「浮盈是必要条件」在触发那一刻依然成立——绝不平一个浮亏的仓位。
	if st.armed && profit <= minProfitPct {
		st.disarm()
	}

	if !st.armed {
		if fall < armPct || profit <= minProfitPct {
			return false
		}
		st.armed = true
		st.armedAt = now
		st.armFall = fall
		st.low = price
		st.lowAt = now
		// 刚武装时 low 就是现价, 反弹幅度为 0, 因此不会在武装当轮触发(B > 0)
	}

	// low 是「自武装以来」的最低点, 不是滚动窗口内的最低点。若跟着窗口一起滑动,
	// low 会被动上移, 反弹幅度被系统性低估, 越晚越平不掉——这是本策略最容易写错的地方。
	if price < st.low {
		st.low = price
		st.lowAt = now
	}

	return st.rebound(price) >= reboundPct
}

// rebound 返回现价自武装以来最低点的反弹幅度(%, 上涨为正)。
func (st *symbolState) rebound(price float64) float64 {
	if st.low <= 0 {
		return 0
	}
	return (price - st.low) / st.low * 100
}

// describe 返回状态的简短描述, 仅用于 -v 的明细日志。
func (st *symbolState) describe(price float64, trigger bool) string {
	switch {
	case trigger:
		return "→ 触发平仓"
	case st.armed:
		return fmt.Sprintf("已武装 低点=%.6g 反弹=%.2f%%", st.low, st.rebound(price))
	default:
		return "待机"
	}
}

// disarm 回到「等暴跌」状态。lastAlert/lastAttempt/failures 是告警与重试的去重
// 状态, 与策略无关, 保留不动。
func (st *symbolState) disarm() {
	st.armed = false
	st.low = 0
	st.lowAt = time.Time{}
	st.armedAt = time.Time{}
	st.armFall = 0
}
