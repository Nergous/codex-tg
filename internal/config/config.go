package config

import (
	"encoding/json"
	"os"

	"github.com/Nergous/codex-tg/internal/models"
)

type Config struct {
	Telegram  TelegramConfig   `json:"telegram"`
	AppServer AppServerConfig  `json:"app_server"`
	Projects  []models.Project `json:"projects"`
}

func Load(path string) (*Config, error) {
	return load(path, os.Open)
}

func load(path string, openFile func(string) (*os.File, error)) (*Config, error) {
	var cfg Config

	info, err := isExist(path, ErrConfigNotFound)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, ErrInvalidConfigFile
	}

	file, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (cfg *Config) Validate() error {
	if err := cfg.Telegram.validate(); err != nil {
		return err
	}

	if err := cfg.AppServer.validate(); err != nil {
		return err
	}

	if err := validateProjects(cfg.Projects); err != nil {
		return err
	}

	return nil
}

func validateProjects(ps []models.Project) error {
	return validateProjectsWith(ps, canonicalPath, isExist)
}

func validateProjectsWith(
	ps []models.Project,
	canonicalize func(string) (string, error),
	stat func(string, error) (os.FileInfo, error),
) error {
	if !models.IsProjectsUnique(ps) {
		return ErrProjectsNotUnique
	}

	for i := range ps {
		canon, err := canonicalize(ps[i].Path)
		if err != nil {
			if os.IsNotExist(err) {
				return ErrProjectNotFound
			}

			return err
		}

		info, err := stat(canon, ErrProjectNotFound)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			return ErrInvalidProject
		}

		ps[i].Path = canon
	}

	return nil
}
