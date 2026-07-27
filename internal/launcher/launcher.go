package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const defaultTokenEnv = "CODEX_TG_REMOTE_TOKEN"

var ErrInvalidLauncherConfig = errors.New("invalid launcher configuration")

type LaunchArgs []string

type Launcher struct {
	CodexBinary string
	Endpoint    string
	TokenEnv    string

	runner func(context.Context, string, []string, []string) error
}

func New(codexBinary, endpoint string) Launcher {
	return Launcher{
		CodexBinary: codexBinary,
		Endpoint:    endpoint,
		TokenEnv:    defaultTokenEnv,
	}
}

func (l Launcher) Args(cwd, threadID string) LaunchArgs {
	return LaunchArgs{
		"--cd", cwd,
		"--remote", l.Endpoint,
		"--remote-auth-token-env", l.TokenEnv,
		"resume", threadID,
	}
}

func (l Launcher) Run(ctx context.Context, cwd, threadID, token string) error {
	if l.CodexBinary == "" {
		return ErrInvalidLauncherConfig
	}
	if l.TokenEnv == "" {
		return ErrInvalidLauncherConfig
	}
	if threadID == "" {
		return fmt.Errorf("%w: thread id", ErrInvalidLauncherConfig)
	}

	args := []string(l.Args(cwd, threadID))

	runner := l.runner
	if runner == nil {
		runner = runCommand
	}

	env := append(os.Environ(), l.TokenEnv+"="+token)
	return runner(ctx, l.CodexBinary, args, env)
}

func runCommand(ctx context.Context, binary string, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	configureCommand(cmd)
	return cmd.Run()
}
