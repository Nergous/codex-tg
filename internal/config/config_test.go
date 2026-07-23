package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nergous/codex-tg/internal/project"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(t *testing.T, cfg *Config)
		wantError error
	}{
		{
			name: "invalid telegram config",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
				cfg.Telegram.AllowedUserID = 0
			},
			wantError: ErrInvalidTelegramID,
		},
		{
			name: "invalid app server config",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
				cfg.AppServer.Listen = "0.0.0.0:4500"
			},
			wantError: ErrUnsafeListenAddress,
		},
		{
			name: "duplicate project names",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
				secondRoot := t.TempDir()
				cfg.Projects = append(cfg.Projects, project.Project{
					Name: "demo",
					Path: secondRoot,
				})
			},
			wantError: ErrProjectsNotUnique,
		},
		{
			name: "missing project",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
				cfg.Projects[0].Path = filepath.Join(t.TempDir(), "missing")
			},
			wantError: ErrProjectNotFound,
		},
		{
			name: "project is regular file",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
				path := filepath.Join(t.TempDir(), "project.txt")
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatalf("create project file: %v", err)
				}
				cfg.Projects[0].Path = path
			},
			wantError: ErrInvalidProject,
		},
		{
			name: "valid config",
			mutate: func(t *testing.T, cfg *Config) {
				t.Helper()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig(t)
			test.mutate(t, cfg)

			err := cfg.Validate()
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Validate() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestConfig_ValidateCanonicalizesProjectPaths(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t)
	cfg.Projects[0].Path = filepath.Join(cfg.Projects[0].Path, ".")

	wantPath, err := canonicalPath(cfg.Projects[0].Path)
	if err != nil {
		t.Fatalf("canonicalize expected path: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	if cfg.Projects[0].Path != wantPath {
		t.Fatalf("project path = %q, want %q", cfg.Projects[0].Path, wantPath)
	}
}

func TestValidateProjects_ReturnsCanonicalPathError(t *testing.T) {
	wantError := errors.New("canonical path failure")

	err := validateProjectsWith(
		[]project.Project{{Name: "demo", Path: "project"}},
		func(string) (string, error) { return "", wantError },
		isExist,
	)
	if !errors.Is(err, wantError) {
		t.Fatalf("validateProjectsWith() error = %v, want %v", err, wantError)
	}
}

func TestValidateProjects_ReturnsStatError(t *testing.T) {
	wantError := errors.New("stat failure")

	err := validateProjectsWith(
		[]project.Project{{Name: "demo", Path: "project"}},
		func(path string) (string, error) { return path, nil },
		func(string, error) (os.FileInfo, error) { return nil, wantError },
	)
	if !errors.Is(err, wantError) {
		t.Fatalf("validateProjectsWith() error = %v, want %v", err, wantError)
	}
}

func TestCanonicalPath_ReturnsAbsolutePathError(t *testing.T) {
	wantError := errors.New("absolute path failure")

	_, err := canonicalPathWith(
		"project",
		func(string) (string, error) { return "", wantError },
		filepath.EvalSymlinks,
	)
	if !errors.Is(err, wantError) {
		t.Fatalf("canonicalPathWith() error = %v, want %v", err, wantError)
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("missing config", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
		if !errors.Is(err, ErrConfigNotFound) {
			t.Fatalf("Load() error = %v, want %v", err, ErrConfigNotFound)
		}
	})

	t.Run("config is directory", func(t *testing.T) {
		_, err := Load(t.TempDir())
		if !errors.Is(err, ErrInvalidConfigFile) {
			t.Fatalf("Load() error = %v, want %v", err, ErrInvalidConfigFile)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		path := writeConfigFile(t, []byte(`{"telegram":`))

		_, err := Load(path)
		if err == nil {
			t.Fatal("Load() error = nil, want JSON decoding error")
		}
	})

	t.Run("unknown JSON field", func(t *testing.T) {
		path := writeConfigFile(t, []byte(`{"unknown":true}`))

		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("Load() error = %v, want unknown field error", err)
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		cfg := validConfig(t)
		cfg.Telegram.AllowedChatID = 0
		path := writeJSONConfig(t, cfg)

		_, err := Load(path)
		if !errors.Is(err, ErrInvalidTelegramID) {
			t.Fatalf("Load() error = %v, want %v", err, ErrInvalidTelegramID)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		want := validConfig(t)
		want.Projects[0].Path = filepath.Join(want.Projects[0].Path, ".")
		path := writeJSONConfig(t, want)

		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}

		wantPath, err := canonicalPath(want.Projects[0].Path)
		if err != nil {
			t.Fatalf("canonicalize expected path: %v", err)
		}
		if got.Telegram != want.Telegram {
			t.Fatalf("Telegram = %#v, want %#v", got.Telegram, want.Telegram)
		}
		if got.AppServer != want.AppServer {
			t.Fatalf("AppServer = %#v, want %#v", got.AppServer, want.AppServer)
		}
		if len(got.Projects) != 1 {
			t.Fatalf("len(Projects) = %d, want 1", len(got.Projects))
		}
		if got.Projects[0].Name != want.Projects[0].Name {
			t.Fatalf("project name = %q, want %q", got.Projects[0].Name, want.Projects[0].Name)
		}
		if got.Projects[0].Path != wantPath {
			t.Fatalf("project path = %q, want %q", got.Projects[0].Path, wantPath)
		}
	})
}

func TestLoad_ReturnsOpenError(t *testing.T) {
	path := writeConfigFile(t, []byte(`{}`))
	wantError := errors.New("open failure")

	_, err := load(path, func(string) (*os.File, error) {
		return nil, wantError
	})
	if !errors.Is(err, wantError) {
		t.Fatalf("load() error = %v, want %v", err, wantError)
	}
}

func validConfig(t *testing.T) *Config {
	t.Helper()

	root := t.TempDir()
	codexBinary := filepath.Join(root, "codex")
	if err := os.WriteFile(codexBinary, []byte("test binary"), 0o755); err != nil {
		t.Fatalf("create test codex binary: %v", err)
	}

	return &Config{
		Telegram: TelegramConfig{
			AllowedUserID: 1,
			AllowedChatID: 1,
		},
		AppServer: AppServerConfig{
			Listen:      "127.0.0.1:4500",
			CodexBinary: codexBinary,
		},
		Projects: []project.Project{
			{
				Name: "demo",
				Path: root,
			},
		},
	}
}

func writeJSONConfig(t *testing.T, cfg *Config) string {
	t.Helper()

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	return writeConfigFile(t, data)
}

func writeConfigFile(t *testing.T, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
