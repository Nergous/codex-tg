package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNotArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("run() code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunRecognizesCommands(t *testing.T) {
	for _, command := range []string{"setup", "serve", "open", "project", "status", "autostart"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{command}, &stdout, &stderr); code != exitError {
				t.Fatalf("run() code = %d, want %d", code, exitError)
			}
			if !strings.Contains(stderr.String(), "command not wired") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unknown"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("run() code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown command") ||
		!strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run() code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "usage:") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}
