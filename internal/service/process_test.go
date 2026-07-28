package service

import "testing"

func TestIsTestExecutable(t *testing.T) {
	for _, path := range []string{`C:\Temp\codex-tg.test.exe`, "/tmp/codex-tg.test"} {
		if !isTestExecutable(path) {
			t.Fatalf("isTestExecutable(%q)=false", path)
		}
	}
	if isTestExecutable(`C:\Tools\codex-tg.exe`) {
		t.Fatal("production executable classified as test")
	}
}
