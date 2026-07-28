package onboarding

import (
	"path/filepath"
	"testing"
)

func TestStateResumesCompletedSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "onboarding.json")
	want := State{CommandLineComplete: true, TelegramComplete: true}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestMissingStateStartsFresh(t *testing.T) {
	got, err := LoadState(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || got != (State{}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
