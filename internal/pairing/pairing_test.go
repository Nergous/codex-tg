package pairing

import (
	"context"
	"testing"

	"github.com/Nergous/codex-tg/internal/telegram"
)

type fakeBot struct {
	batches  [][]telegram.Update
	calls    int
	timeouts []int
}

func (b *fakeBot) GetUpdatesWithTimeout(ctx context.Context, offset int64, timeout int) ([]telegram.Update, error) {
	b.timeouts = append(b.timeouts, timeout)
	return b.GetUpdates(ctx, offset)
}

func (b *fakeBot) GetMe(context.Context) (telegram.User, error) {
	return telegram.User{Username: "bridge_bot"}, nil
}
func (b *fakeBot) DeleteWebhook(context.Context) error { return nil }
func (b *fakeBot) GetUpdates(context.Context, int64) ([]telegram.Update, error) {
	out := b.batches[b.calls]
	b.calls++
	return out, nil
}

func TestPairAcceptsOnlyNewPrivateStart(t *testing.T) {
	old := telegram.Update{UpdateID: 4, Message: &telegram.Message{Text: "/start", Chat: telegram.Chat{ID: 1, Type: "private"}, From: telegram.User{ID: 1}}}
	wrong := telegram.Update{UpdateID: 5, Message: &telegram.Message{Text: "hello", Chat: telegram.Chat{ID: 2, Type: "private"}, From: telegram.User{ID: 2}}}
	want := telegram.Update{UpdateID: 6, Message: &telegram.Message{Text: "/start", Chat: telegram.Chat{ID: 7, Type: "private"}, From: telegram.User{ID: 8, Username: "alice"}}}
	bot := &fakeBot{batches: [][]telegram.Update{{old}, {wrong, want}}}
	identity, err := Pair(context.Background(), bot, func(BotIdentity, Identity) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if identity.UserID != 8 || identity.ChatID != 7 || identity.Username != "alice" || identity.UpdateOffset != 7 {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestPairReportsBotBeforeWaitingForStart(t *testing.T) {
	want := telegram.Update{UpdateID: 1, Message: &telegram.Message{Text: "/start", Chat: telegram.Chat{ID: 7, Type: "private"}, From: telegram.User{ID: 8}}}
	bot := &fakeBot{batches: [][]telegram.Update{{}, {want}}}
	ready := ""
	_, err := PairWithReady(context.Background(), bot, func(identity BotIdentity) {
		ready = identity.Username
	}, func(BotIdentity, Identity) (bool, error) {
		if ready == "" {
			t.Fatal("ready callback must run before confirmation")
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready != "bridge_bot" {
		t.Fatalf("ready=%q", ready)
	}
	if len(bot.timeouts) == 0 || bot.timeouts[0] != 0 {
		t.Fatalf("baseline timeouts=%v", bot.timeouts)
	}
}

func TestPairRejectsNonPrivateStart(t *testing.T) {
	group := telegram.Update{UpdateID: 2, Message: &telegram.Message{Text: "/start", Chat: telegram.Chat{ID: -1, Type: "group"}, From: telegram.User{ID: 8}}}
	ctx, cancel := context.WithCancel(context.Background())
	bot := &fakeBot{batches: [][]telegram.Update{{}, {group}}}
	cancel()
	if _, err := Pair(ctx, bot, func(BotIdentity, Identity) (bool, error) { return true, nil }); err == nil {
		t.Fatal("expected cancellation")
	}
}
