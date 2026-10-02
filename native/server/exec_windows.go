//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// 避免 ffmpeg 在 Windows 上弹出黑色控制台窗口。
func attachProcess(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
}
