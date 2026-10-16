//go:build windows

package tools

import "syscall"

// hiddenWindowAttr 返回隐藏窗口的 SysProcAttr（Windows 专用）
func hiddenWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow: true,
	}
}
