//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

func hideProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
