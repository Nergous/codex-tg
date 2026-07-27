//go:build windows

package launcher

import (
	"os/exec"
	"syscall"
)

// configureCommand applies windows-specific defaults for user-facing Codex launch.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
