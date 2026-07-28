//go:build !windows

package service

import (
	"os/exec"
	"syscall"
)

func configureDetached(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
