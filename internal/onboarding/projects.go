package onboarding

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/models"
)

type Confirm func(string) (bool, error)

func EnsureProject(cfg *config.Config, path string, confirm Confirm) (models.Project, bool, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return models.Project{}, false, fmt.Errorf("resolve project path: %w", err)
	}
	for _, project := range cfg.Projects {
		existing, resolveErr := canonicalPath(project.Path)
		if resolveErr == nil && existing == canonical {
			return project, false, nil
		}
	}

	name := filepath.Base(canonical)
	for _, project := range cfg.Projects {
		if project.Name == name && project.Path != canonical {
			suffix := sha256.Sum256([]byte(canonical))
			name = fmt.Sprintf("%s-%x", name, suffix[:3])
			break
		}
	}
	project := models.Project{Name: name, Path: canonical, Enabled: true}
	ok, err := confirm(fmt.Sprintf("Add project %q?\n  %s\nConfirm [y/N]: ", name, canonical))
	if err != nil {
		return models.Project{}, false, err
	}
	if !ok {
		return project, false, nil
	}
	cfg.Projects = append(cfg.Projects, project)
	return project, true, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
