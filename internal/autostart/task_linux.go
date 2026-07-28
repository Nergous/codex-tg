//go:build linux

package autostart

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const UnitName = "codex-tg.service"

type Scheduler struct {
	Executable string
	WorkDir    string
	UnitPath   string
	Run        func(context.Context, ...string) ([]byte, error)
}

func (s Scheduler) Install(ctx context.Context) error {
	if strings.TrimSpace(s.Executable) == "" || strings.TrimSpace(s.WorkDir) == "" {
		return errors.New("autostart: executable and work directory are required")
	}
	unitPath, err := s.unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		return fmt.Errorf("create user unit directory: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(s.unit()), 0o600); err != nil {
		return fmt.Errorf("write user unit: %w", err)
	}
	if _, err := s.runner()(ctx, "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload user systemd manager: %w", err)
	}
	if _, err := s.runner()(ctx, "--user", "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("enable user unit: %w", err)
	}
	return nil
}

func (s Scheduler) Status(ctx context.Context) (bool, error) {
	_, err := s.runner()(ctx, "--user", "is-enabled", "--quiet", UnitName)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (s Scheduler) Remove(ctx context.Context) error {
	unitPath, err := s.unitPath()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(unitPath)
	if err != nil {
		return fmt.Errorf("read user unit: %w", err)
	}
	expected := "ExecStart=" + quoteUnitValue(s.Executable) + " serve"
	if !strings.Contains(string(content), expected) {
		return errors.New("autostart: unit does not target current executable")
	}
	if _, err := s.runner()(ctx, "--user", "disable", "--now", UnitName); err != nil {
		return fmt.Errorf("disable user unit: %w", err)
	}
	if err := os.Remove(unitPath); err != nil {
		return fmt.Errorf("remove user unit: %w", err)
	}
	if _, err := s.runner()(ctx, "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload user systemd manager: %w", err)
	}
	return nil
}

func (s Scheduler) unit() string {
	return fmt.Sprintf(`[Unit]
Description=Codex Telegram bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve
WorkingDirectory=%s
Restart=on-failure
RestartSec=1s

[Install]
WantedBy=default.target
`, quoteUnitValue(s.Executable), quoteUnitValue(s.WorkDir))
}

func (s Scheduler) unitPath() (string, error) {
	if s.UnitPath != "" {
		return s.UnitPath, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "systemd", "user", UnitName), nil
}

func (s Scheduler) runner() func(context.Context, ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	}
}

func quoteUnitValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)
	return `"` + replacer.Replace(value) + `"`
}
