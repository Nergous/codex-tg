//go:build windows

package autostart

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestInstallUsesCurrentExecutableAndLimitedRestart(t *testing.T) {
	var got []string
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, WorkDir: `C:\Users\me\AppData\Local\codex-tg`, UserID: `S-1-5-21-1000`, Run: func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}}
	if err := s.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"/Create", `CodexTgBridge`, `/XML`, `/F`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args=%q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "/RL") {
		t.Fatalf("/RL is invalid with /XML: args=%q", joined)
	}
	for _, want := range []string{`codex-tg.exe`, `serve`, `WorkingDirectory`, `Hidden>true`, `RestartOnFailure`, `Count>3`} {
		if !strings.Contains(s.xml(), want) {
			t.Fatalf("xml missing %q", want)
		}
	}
}

func TestInstallWritesUTF16LETaskXML(t *testing.T) {
	var xmlFile []byte
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, WorkDir: `C:\work`, UserID: `S-1-5-21-1000`, Run: func(_ context.Context, args ...string) ([]byte, error) {
		data, err := os.ReadFile(args[4])
		if err != nil {
			return nil, err
		}
		xmlFile = data
		return nil, nil
	}}
	if err := s.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	decoded := decodeUTF16LE(t, xmlFile)
	if !strings.HasPrefix(decoded, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatalf("decoded XML prefix=%q", decoded[:min(64, len(decoded))])
	}
}

func TestInstallTargetsCurrentUser(t *testing.T) {
	const userID = `S-1-5-21-1000`
	var xmlFile []byte
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, WorkDir: `C:\work`, UserID: userID, Run: func(_ context.Context, args ...string) ([]byte, error) {
		data, err := os.ReadFile(args[4])
		if err != nil {
			return nil, err
		}
		xmlFile = data
		return nil, nil
	}}
	if err := s.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	decoded := decodeUTF16LE(t, xmlFile)
	for _, want := range []string{
		`<LogonTrigger><Enabled>true</Enabled><UserId>` + userID + `</UserId></LogonTrigger>`,
		`<Principal id="Author"><UserId>` + userID + `</UserId><RunLevel>LeastPrivilege</RunLevel><LogonType>InteractiveToken</LogonType></Principal>`,
	} {
		if !strings.Contains(decoded, want) {
			t.Fatalf("task XML missing %q", want)
		}
	}
}

func decodeUTF16LE(t *testing.T, xmlFile []byte) string {
	t.Helper()
	if !bytes.HasPrefix(xmlFile, []byte{0xff, 0xfe}) {
		t.Fatalf("xml BOM=% x, want ff fe", xmlFile[:min(2, len(xmlFile))])
	}
	if len(xmlFile)%2 != 0 {
		t.Fatalf("xml byte length=%d, want even UTF-16LE length", len(xmlFile))
	}
	codeUnits := make([]uint16, (len(xmlFile)-2)/2)
	for i := range codeUnits {
		codeUnits[i] = binary.LittleEndian.Uint16(xmlFile[2+i*2:])
	}
	return string(utf16.Decode(codeUnits))
}

func TestInstallIncludesSchedulerDiagnostics(t *testing.T) {
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, WorkDir: `C:\work`, UserID: `S-1-5-21-1000`, Run: func(context.Context, ...string) ([]byte, error) {
		return []byte("invalid task XML"), errors.New("exit status 1")
	}}
	err := s.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid task XML") {
		t.Fatalf("error=%v", err)
	}
}

func TestRemoveRejectsDifferentExecutable(t *testing.T) {
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, Run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "/Query" {
			return []byte(`Task To Run: C:\other\codex-tg.exe serve`), nil
		}
		t.Fatal("must not delete")
		return nil, nil
	}}
	if err := s.Remove(context.Background()); err == nil {
		t.Fatal("Remove() error=nil")
	}
}
