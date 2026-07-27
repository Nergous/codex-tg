//go:build windows

package codex

import (
	"os/exec"
	"syscall"
)

// configureCommand applies windows-specific process settings for the App Server supervisor.
func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
}
