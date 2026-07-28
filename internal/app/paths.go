package app

import (
	"os"
	"path/filepath"
)

func ConfigPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "codex-tg", "config.json")
}

func DataPath() string {
	return filepath.Join(filepath.Dir(ConfigPath()), "state.db")
}

func RuntimePath() string {
	return filepath.Join(filepath.Dir(ConfigPath()), "runtime.json")
}

func OnboardingPath() string { return filepath.Join(filepath.Dir(ConfigPath()), "onboarding.json") }
