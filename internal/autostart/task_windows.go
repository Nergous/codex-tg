//go:build windows

package autostart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const TaskName = "CodexTgBridge"

type Scheduler struct {
	Executable string
	WorkDir    string
	UserID     string
	Run        func(context.Context, ...string) ([]byte, error)
}

func (s Scheduler) Install(ctx context.Context) error {
	if s.Executable == "" || s.WorkDir == "" {
		return errors.New("autostart: executable and work directory are required")
	}
	userID, err := s.userID()
	if err != nil {
		return err
	}
	s.UserID = userID
	file, err := os.CreateTemp("", "codex-tg-task-*.xml")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(encodeUTF16LE(s.xml())); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	out, err := s.runner()(ctx, "/Create", "/TN", TaskName, "/XML", path, "/F")
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("create scheduled task: %w: %s", err, detail)
		}
		return fmt.Errorf("create scheduled task: %w", err)
	}
	return nil
}

func (s Scheduler) xml() string {
	userID := escapeXML(s.UserID)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?><Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers><Principals><Principal id="Author"><UserId>%s</UserId><RunLevel>LeastPrivilege</RunLevel><LogonType>InteractiveToken</LogonType></Principal></Principals><Settings><Hidden>true</Hidden><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings><Actions Context="Author"><Exec><Command>%s</Command><Arguments>serve</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions></Task>`, userID, userID, escapeXML(s.Executable), escapeXML(s.WorkDir))
}

func (s Scheduler) userID() (string, error) {
	if s.UserID != "" {
		return s.UserID, nil
	}
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve current user: %w", err)
	}
	if current.Uid == "" {
		return "", errors.New("autostart: current user SID is unavailable")
	}
	return current.Uid, nil
}

func encodeUTF16LE(value string) []byte {
	codeUnits := utf16.Encode([]rune(value))
	data := make([]byte, 2+len(codeUnits)*2)
	data[0], data[1] = 0xff, 0xfe
	for i, codeUnit := range codeUnits {
		data[2+i*2] = byte(codeUnit)
		data[3+i*2] = byte(codeUnit >> 8)
	}
	return data
}

func escapeXML(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
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
