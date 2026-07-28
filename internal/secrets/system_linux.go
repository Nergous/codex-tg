//go:build linux

package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

const serviceName = "codex-tg"

var errSecretNotFound = errors.New("secret-tool: secret not found")

type secretToolRunner func(context.Context, []byte, ...string) ([]byte, error)

type LinuxStore struct {
	run secretToolRunner
}

func NewLinuxStore() *LinuxStore {
	return &LinuxStore{run: runSecretTool}
}

func NewSystemStore() Store {
	return NewLinuxStore()
}

func (s *LinuxStore) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := s.runner()(ctx, nil, "lookup", "service", serviceName, "name", name)
	if errors.Is(err, errSecretNotFound) {
		return nil, fmt.Errorf("get credential %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get credential %q: %w", name, err)
	}
	return bytes.Clone(bytes.TrimRight(out, "\r\n")), nil
}

func (s *LinuxStore) Set(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.runner()(ctx, bytes.Clone(value), "store", "--label=codex-tg Telegram bot token", "service", serviceName, "name", name)
	if err != nil {
		return fmt.Errorf("set credential %q: %w", name, err)
	}
	return nil
}

func (s *LinuxStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.runner()(ctx, nil, "clear", "service", serviceName, "name", name)
	if errors.Is(err, errSecretNotFound) {
		return fmt.Errorf("delete credential %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete credential %q: %w", name, err)
	}
	return nil
}

func (s *LinuxStore) runner() secretToolRunner {
	if s.run != nil {
		return s.run
	}
	return runSecretTool
}

func runSecretTool(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "secret-tool", args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(out) == 0 {
		return nil, errSecretNotFound
	}
	return nil, err
}

var _ Store = (*LinuxStore)(nil)
