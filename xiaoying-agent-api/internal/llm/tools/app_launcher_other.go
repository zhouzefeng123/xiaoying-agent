//go:build !windows

package tools

import "syscall"

// hiddenWindowAttr 非 Windows 平台的空实现
func hiddenWindowAttr() *syscall.SysProcAttr {
	return nil
}
