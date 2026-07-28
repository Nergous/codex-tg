package onboarding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/models"
)

func TestEnsureProject_ConfirmsCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	var prompt string
	project, added, err := EnsureProject(cfg, dir, func(message string) (bool, error) {
		prompt = message
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !added || len(cfg.Projects) != 1 {
		t.Fatalf("added=%t projects=%v", added, cfg.Projects)
	}
	if project.Path != filepath.Clean(dir) || !strings.Contains(prompt, project.Name) || !strings.Contains(prompt, project.Path) {
		t.Fatalf("project=%+v prompt=%q", project, prompt)
	}
}

func TestEnsureProject_RejectionDoesNotMutateConfig(t *testing.T) {
	cfg := &config.Config{}
	_, added, err := EnsureProject(cfg, t.TempDir(), func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if added || len(cfg.Projects) != 0 {
		t.Fatalf("added=%t projects=%v", added, cfg.Projects)
	}
}

func TestEnsureProject_NameCollisionGetsStableSuffix(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one", "project")
	second := filepath.Join(root, "two", "project")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Projects: []models.Project{{Name: "project", Path: first, Enabled: true}}}
	got, added, err := EnsureProject(cfg, second, func(string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !added || got.Name == "project" || !strings.HasPrefix(got.Name, "project-") {
		t.Fatalf("project=%+v", got)
	}
	cfg.Projects = cfg.Projects[:1]
	again, _, err := EnsureProject(cfg, second, func(string) (bool, error) { return true, nil })
	if err != nil || again.Name != got.Name {
		t.Fatalf("again=%+v err=%v", again, err)
	}
}
