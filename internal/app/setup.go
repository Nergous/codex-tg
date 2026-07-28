package app

import (
	"bytes"
	"context"
	"errors"

	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/secrets"
	"github.com/Nergous/codex-tg/internal/telegram"
)

type SetupBot interface {
	GetMe(context.Context) (telegram.User, error)
	DeleteWebhook(context.Context) error
}

func Setup(ctx context.Context, bot SetupBot, store secrets.Store, token []byte, configPath string, cfg *config.Config) error {
	if len(bytes.TrimSpace(token)) == 0 {
		return errors.New("telegram bot token is required")
	}
	if _, err := bot.GetMe(ctx); err != nil {
		return err
	}
	if err := bot.DeleteWebhook(ctx); err != nil {
		return err
	}
	if err := store.Set(ctx, secrets.TelegramBotToken, token); err != nil {
		return err
	}
	return config.Save(configPath, cfg)
}
