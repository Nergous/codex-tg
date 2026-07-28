//go:build windows

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

const TaskName = "CodexTgBridge"

type Scheduler struct {
	Executable string
	WorkDir    string
	Run        func(context.Context, ...string) ([]byte, error)
}

func (s Scheduler) Install(ctx context.Context) error {
	if s.Executable == "" || s.WorkDir == "" {
		return errors.New("autostart: executable and work directory are required")
	}
	file, err := os.CreateTemp("", "codex-tg-task-*.xml")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.WriteString(s.xml()); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, err = s.runner()(ctx, "/Create", "/TN", TaskName, "/XML", path, "/RL", "LIMITED", "/F")
	return err
}

func (s Scheduler) xml() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?><Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers><Principals><Principal id="Author"><RunLevel>LeastPrivilege</RunLevel><LogonType>InteractiveToken</LogonType></Principal></Principals><Settings><Hidden>true</Hidden><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings><Actions Context="Author"><Exec><Command>%s</Command><Arguments>serve</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions></Task>`, s.Executable, s.WorkDir)
}
func (s Scheduler) Status(ctx context.Context) (bool, error) {
	out, err := s.runner()(ctx, "/Query", "/TN", TaskName, "/FO", "LIST", "/V")
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), filepath.Base(s.Executable)), nil
}
func (s Scheduler) Remove(ctx context.Context) error {
	out, err := s.runner()(ctx, "/Query", "/TN", TaskName, "/FO", "LIST", "/V")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(s.Executable)) {
		return errors.New("autostart: task does not target current executable")
	}
	_, err = s.runner()(ctx, "/Delete", "/TN", TaskName, "/F")
	return err
}
func (s Scheduler) runner() func(context.Context, ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "schtasks.exe", args...).CombinedOutput()
	}
}
