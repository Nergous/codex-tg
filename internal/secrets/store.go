package secrets

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("secret not found")

const TelegramBotToken = "codex-tg/telegram-bot-token"

type Store interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Set(ctx context.Context, name string, value []byte) error
	Delete(ctx context.Context, name string) error
}
