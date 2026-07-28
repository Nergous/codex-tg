package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	args := LaunchArgs{
		"--cd", cwd,
		"--remote", l.Endpoint,
		"--remote-auth-token-env", l.TokenEnv,
	}
	if threadID != "" {
		args = append(args, "resume", threadID)
	}
	return args
}

func (l Launcher) Run(ctx context.Context, cwd, threadID, token string) error {
	if l.CodexBinary == "" {
		return ErrInvalidLauncherConfig
	}
	if strings.TrimSpace(cwd) == "" {
		return fmt.Errorf("%w: cwd", ErrInvalidLauncherConfig)
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("%w: cwd", ErrInvalidLauncherConfig)
	}
	if l.TokenEnv == "" {
		return ErrInvalidLauncherConfig
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%w: token", ErrInvalidLauncherConfig)
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
	return newCommand(ctx, binary, args, env).Run()
}

func newCommand(ctx context.Context, binary string, args []string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	configureCommand(cmd)
	return cmd
}
