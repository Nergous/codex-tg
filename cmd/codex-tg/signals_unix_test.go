//go:build !windows

package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
)

func TestShutdownSignalsIncludeInterruptAndTerminate(t *testing.T) {
	got := shutdownSignals()
	if !slices.Contains(got, os.Interrupt) || !slices.Contains(got, os.Signal(syscall.SIGTERM)) {
		t.Fatalf("shutdownSignals() = %v", got)
	}
}
