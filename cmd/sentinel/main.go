package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bybit-position-monitor/internal/cfg"
	"bybit-position-monitor/internal/daemon"
	"bybit-position-monitor/internal/sentinel"
)

func main() {
	var (
		runOnce   bool
		dryRun    bool
		envFile   string
		daemonize bool
		logFile   string
		verbose   bool

		interval  time.Duration
		window    time.Duration
		dropPct   float64
		volMult   float64
		maxOrders int
	)

	flag.BoolVar(&runOnce, "once", false, "只跑一轮就退出(冒烟测试/配合外部调度)")
	flag.BoolVar(&dryRun, "dry-run", true, "只告警不下单; 要真实平仓需显式传 -dry-run=false")
	flag.StringVar(&envFile, "env", ".env.sentinel", "配置文件路径")
	flag.BoolVar(&daemonize, "daemon", false, "以守护进程模式运行(脱离终端后台常驻)")
	flag.StringVar(&logFile, "log", "sentinel.log", "-daemon 模式下写日志的文件")
	flag.BoolVar(&verbose, "v", false, "打印每个被监控品种的明细")

	flag.DurationVar(&interval, "interval", 0, "轮询间隔, 覆盖 POLL_INTERVAL")
	flag.DurationVar(&window, "window", 0, "回撤回看窗口, 覆盖 DROP_WINDOW")
	flag.Float64Var(&dropPct, "drop-pct", 0, "武装阈值 A(%), 覆盖 DROP_PCT")
	flag.Float64Var(&volMult, "vol-mult", 0, "B 的波动率倍数, 覆盖 VOL_MULT")
	flag.IntVar(&maxOrders, "max-orders", 0, "单轮最多平仓数, 覆盖 MAX_ORDERS_PER_CYCLE")
	flag.Parse()

	if daemonize && !daemon.IsChild() {
		pid, err := daemon.Spawn(logFile)
		if err != nil {
			log.Fatalf("后台启动失败: %v", err)
		}
		fmt.Printf("已在后台启动, PID=%d, 日志: %s\n", pid, logFile)
		return
	}

	c, err := cfg.LoadSentinel(envFile)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 只有显式传了的 flag 才覆盖 .env, 否则会把默认值 0 覆盖上去
	c.DryRun = dryRun
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "interval":
			c.PollInterval = interval
		case "window":
			c.DropWindow = window
		case "drop-pct":
			c.DropPct = dropPct
		case "vol-mult":
			c.VolMult = volMult
		case "max-orders":
			c.MaxOrdersPerCycle = maxOrders
		}
	})

	if err := c.Validate(); err != nil {
		log.Fatalf("配置校验失败: %v\n参考 .env.sentinel.example 进行配置", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := sentinel.New(c)
	s.SetVerbose(verbose || runOnce)

	mode := "dry-run(只告警, 不会下单)"
	if !c.DryRun {
		mode = "实盘(会真实市价平仓)"
	}
	log.Printf("回撤止盈哨兵 | 模式: %s | testnet=%v", mode, c.Testnet)
	log.Printf("参数: 轮询 %s | 回撤窗口 %s | 默认 A=%.2f%% | B=%s | 浮盈门槛 %.2f%% | 单轮上限 %d | 下单间隔 %s",
		c.PollInterval, c.DropWindow, c.DropPct, s.ReboundLabel(), c.MinProfitPct, c.MaxOrdersPerCycle, c.OrderMinGap)
	log.Printf("逐品种覆盖: A %d 个, B %d 个 | 波动率: 1 小时 K 线回看 %s",
		len(c.Overrides), len(c.ReboundOverrides), c.VolLookback)
	if !c.DryRun {
		log.Printf("!! 实盘模式已开启, 触发后会对空头仓位下市价平仓单 !!")
	}

	log.Printf("开始回补(价格窗口 %s + 波动率 %s)...", c.DropWindow, c.VolLookback)
	res, err := s.Warmup(ctx)
	if err != nil {
		// 不因此拒绝启动: Bybit 可能只是暂时不可达, 进入轮询后每轮都会重试。
		// 但在窗口就绪之前不会有任何品种被触发。
		log.Printf("回补失败: %v", err)
		log.Printf("将以空窗口进入轮询, 各品种需要积累满 %s 的数据后才会开始判定", c.DropWindow)
	} else {
		log.Printf("回补完成: 价格窗口成功 %d 个、失败 %d 个; 波动率算出 %d 个、退回兜底值 %d 个",
			res.WindowOK, res.WindowFailed, res.VolOK, res.VolFallback)
	}

	sendStartupNotice(ctx, s, c, mode, res)

	if runOnce {
		if err := s.RunOnce(ctx); err != nil {
			log.Fatalf("运行失败: %v", err)
		}
		log.Println("完成")
		return
	}

	log.Printf("进入轮询, 每 %s 一轮", c.PollInterval)
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()

	run := func() {
		start := time.Now()
		if err := s.RunOnce(ctx); err != nil {
			log.Printf("本轮失败: %v", err)
			return
		}
		if d := time.Since(start); d > c.PollInterval {
			log.Printf("注意: 本轮耗时 %s 超过轮询间隔 %s, 超时的轮次不会补跑",
				d.Truncate(time.Millisecond), c.PollInterval)
		}
	}

	run() // 回补完立即先跑一轮, 不必等满一个间隔
	for {
		select {
		case <-ctx.Done():
			log.Println("已退出")
			return
		case <-ticker.C:
			run()
		}
	}
}

func sendStartupNotice(ctx context.Context, s *sentinel.Sentinel, c cfg.Sentinel, mode string, res sentinel.WarmupResult) {
	msg := fmt.Sprintf("🚀 回撤止盈哨兵已启动\n模式: %s\n回撤窗口 %s | 默认 A=%.2f%% | 浮盈门槛 %.2f%%\nB=%s\n轮询 %s | 单轮上限 %d\n回补: 价格窗口 %d 个, 波动率 %d 个(兜底 %d 个)",
		mode, c.DropWindow, c.DropPct, c.MinProfitPct, s.ReboundLabel(),
		c.PollInterval, c.MaxOrdersPerCycle, res.WindowOK, res.VolOK, res.VolFallback)
	if !c.DryRun {
		msg += "\n\n!! 实盘模式: 触发即真实市价平仓 !!"
	}
	if err := s.SendTelegram(ctx, msg); err != nil {
		log.Printf("发送启动通知失败: %v", err)
	}
}
