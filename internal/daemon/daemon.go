// Package daemon 提供把自身进程脱离终端、转为后台常驻运行的能力。
package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// ChildEnv 是标记「当前进程已经是后台子进程」的环境变量名, 用于防止无限自我重启。
const ChildEnv = "RIVER_DAEMON_CHILD"

// IsChild 报告当前进程是否由 Spawn 启动的后台子进程。
func IsChild() bool {
	return os.Getenv(ChildEnv) == "1"
}

// Spawn 以脱离终端的方式重新启动当前可执行文件(沿用原有命令行参数),
// 并把子进程的标准输出与错误重定向到 logFile。返回子进程的 PID。
func Spawn(logFile string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), ChildEnv+"=1")
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
