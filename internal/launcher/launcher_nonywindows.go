//go:build !windows

package launcher

import "os/exec"

func configureCommand(cmd *exec.Cmd) {}
