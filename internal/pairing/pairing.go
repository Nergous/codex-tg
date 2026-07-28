package pairing

import (
	"context"
	"errors"
	"strings"

	"github.com/Nergous/codex-tg/internal/telegram"
)

type Bot interface {
	GetMe(context.Context) (telegram.User, error)
	DeleteWebhook(context.Context) error
	GetUpdates(context.Context, int64) ([]telegram.Update, error)
}

type BotIdentity struct{ Username string }
type Identity struct {
	UserID, ChatID int64
	Username       string
}
type Confirm func(BotIdentity, Identity) (bool, error)

func Pair(ctx context.Context, bot Bot, confirm Confirm) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	me, err := bot.GetMe(ctx)
	if err != nil {
		return Identity{}, err
	}
	if err := bot.DeleteWebhook(ctx); err != nil {
		return Identity{}, err
	}
	baseline, err := bot.GetUpdates(ctx, 0)
	if err != nil {
		return Identity{}, err
	}
	offset := int64(0)
	for _, update := range baseline {
		if update.UpdateID >= offset {
			offset = update.UpdateID + 1
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return Identity{}, err
		}
		updates, err := bot.GetUpdates(ctx, offset)
		if err != nil {
			return Identity{}, err
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			message := update.Message
			if message == nil || message.Chat.Type != "private" {
				continue
			}
			fields := strings.Fields(message.Text)
			if len(fields) == 0 || fields[0] != "/start" {
				continue
			}
			identity := Identity{UserID: message.From.ID, ChatID: message.Chat.ID, Username: message.From.Username}
			approved, err := confirm(BotIdentity{Username: me.Username}, identity)
			if err != nil {
				return Identity{}, err
			}
			if !approved {
				return Identity{}, errors.New("telegram identity was not confirmed")
			}
			return identity, nil
		}
	}
}
