package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bybit-position-monitor/internal/cfg"
	"bybit-position-monitor/internal/monitor"
)

func main() {
	var runOnce bool
	var dailyTime string
	var envFile string
	var daemon bool
	var logFile string
	flag.BoolVar(&runOnce, "once", false, "立即运行一次并退出(适合配合 cron)")
	flag.StringVar(&dailyTime, "daily-time", "00:01", "每日定时运行的 UTC 时间 (HH:MM), 守护进程模式使用")
	flag.StringVar(&envFile, "env", ".env", "配置文件路径, 默认读取当前目录下的 .env")
	flag.BoolVar(&daemon, "daemon", false, "以守护进程模式运行(脱离终端后台常驻)")
	flag.StringVar(&logFile, "log", "monitor.log", "-daemon 模式下写日志的文件")
	flag.Parse()

	if daemon && os.Getenv("RIVER_DAEMON_CHILD") != "1" {
		pid, err := spawnDaemon(logFile)
		if err != nil {
			log.Fatalf("后台启动失败: %v", err)
		}
		fmt.Printf("已在后台启动, PID=%d, 日志: %s\n", pid, logFile)
		return
	}

	hh, mm, err := parseClock(dailyTime)
	if err != nil {
		log.Fatalf("无效的 -daily-time: %v", err)
	}

	c, err := cfg.Load(envFile)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if err := c.Validate(); err != nil {
		log.Fatalf("配置校验失败: %v\n参考 .env.example 进行配置", err)
	}

	m := monitor.New(c)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if runOnce {
		log.Println("开始运行(once 模式)...")
		if err := m.RunOnce(ctx); err != nil {
			log.Fatalf("运行失败: %v", err)
		}
		log.Println("运行完成")
		return
	}

	log.Printf("守护进程模式: 每日 UTC %02d:%02d 定时执行", hh, mm)
	for {
		nxt := nextRunUTC(hh, mm)
		log.Printf("下次运行时间: %s", nxt.Format("2006-01-02 15:04:05"))
		timer := time.NewTimer(time.Until(nxt))
		select {
		case <-ctx.Done():
			timer.Stop()
			log.Println("已退出")
			return
		case <-timer.C:
			if err := m.RunOnce(ctx); err != nil {
				log.Printf("本次运行失败: %v", err)
			}
		}
	}
}

func spawnDaemon(logFile string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "RIVER_DAEMON_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		f.Close()
		return 0, err
	}
	go func() {
		cmd.Wait()
		f.Close()
	}()
	return cmd.Process.Pid, nil
}

func parseClock(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("格式应为 HH:MM")
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("时间超出范围")
	}
	return hh, mm, nil
}

func nextRunUTC(hh, mm int) time.Time {
	now := time.Now().UTC()
	t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
