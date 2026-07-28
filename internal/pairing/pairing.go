package pairing

import (
	"context"
	"errors"
	"strings"
	"time"

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
	UpdateOffset   int64
}
type Confirm func(BotIdentity, Identity) (bool, error)

type immediateUpdates interface {
	GetUpdatesWithTimeout(context.Context, int64, int) ([]telegram.Update, error)
}

func Pair(ctx context.Context, bot Bot, confirm Confirm) (Identity, error) {
	return PairWithReady(ctx, bot, nil, confirm)
}

func PairWithReady(ctx context.Context, bot Bot, ready func(BotIdentity), confirm Confirm) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	validationCtx, cancelValidation := context.WithTimeout(ctx, 15*time.Second)
	defer cancelValidation()
	me, err := bot.GetMe(validationCtx)
	if err != nil {
		return Identity{}, err
	}
	if err := bot.DeleteWebhook(validationCtx); err != nil {
		return Identity{}, err
	}
	var baseline []telegram.Update
	if immediate, ok := bot.(immediateUpdates); ok {
		baseline, err = immediate.GetUpdatesWithTimeout(validationCtx, 0, 0)
	} else {
		baseline, err = bot.GetUpdates(validationCtx, 0)
	}
	if err != nil {
		return Identity{}, err
	}
	cancelValidation()
	offset := int64(0)
	for _, update := range baseline {
		if update.UpdateID >= offset {
			offset = update.UpdateID + 1
		}
	}
	botIdentity := BotIdentity{Username: me.Username}
	if ready != nil {
		ready(botIdentity)
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
			identity := Identity{UserID: message.From.ID, ChatID: message.Chat.ID, Username: message.From.Username, UpdateOffset: update.UpdateID + 1}
			approved, err := confirm(botIdentity, identity)
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
