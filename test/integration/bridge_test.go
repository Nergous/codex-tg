package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Nergous/codex-tg/internal/app"
	"github.com/Nergous/codex-tg/internal/approval"
	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/telegram"
)

type approvals struct{ resolved int }

func (a *approvals) Request(context.Context, approval.Request) (string, error) { return "nonce", nil }
func (a *approvals) Resolve(context.Context, int64, string, approval.Decision) error {
	a.resolved++
	return nil
}

type responder struct{ calls int }

func (r *responder) Respond(context.Context, json.RawMessage, any) error { r.calls++; return nil }

type recordingMessenger struct{ keyboard *telegram.InlineKeyboard }

func (m *recordingMessenger) Send(_ context.Context, _ int64, _ string, k *telegram.InlineKeyboard, _ telegram.MessageOptions) (int64, error) {
	m.keyboard = k
	return 1, nil
}
func (*recordingMessenger) Edit(context.Context, int64, int64, string, *telegram.InlineKeyboard, telegram.MessageOptions) error {
	return nil
}
func (*recordingMessenger) AnswerCallback(context.Context, string, string) error { return nil }

type updates struct {
	offset int64
	batch  []telegram.Update
}

type coordinator struct{ submits int }

func (c *coordinator) ListProjects(context.Context) ([]models.Project, error) {
	return []models.Project{{Name: "demo", Path: "D:\\repo"}}, nil
}
func (c *coordinator) OpenProject(context.Context, string, bool) (models.Session, error) {
	return models.Session{ThreadID: "thr-1", ProjectPath: "D:\\repo", Active: true}, nil
}
func (c *coordinator) ResumeThread(context.Context, string) error     { return nil }
func (c *coordinator) Submit(context.Context, string, string) error   { c.submits++; return nil }
func (c *coordinator) Status(context.Context, string) (string, error) { return "idle", nil }
func (c *coordinator) Cancel(context.Context, string) error           { return nil }
func (c *coordinator) RecentSessions(context.Context, string, int) ([]models.Session, error) {
	return nil, nil
}
func (c *coordinator) Exec(context.Context, string, string) (string, error) { return "", nil }

type messenger struct{}

func (messenger) Send(context.Context, int64, string, *telegram.InlineKeyboard, telegram.MessageOptions) (int64, error) {
	return 1, nil
}
func (messenger) Edit(context.Context, int64, int64, string, *telegram.InlineKeyboard, telegram.MessageOptions) error {
	return nil
}
func (messenger) AnswerCallback(context.Context, string, string) error { return nil }
func TestBridgeRejectsUnauthorizedAndGroupUpdates(t *testing.T) {
	c := &coordinator{}
	h := telegram.NewHandler(telegram.HandlerOptions{Coordinator: c, Messenger: messenger{}, AllowedUserID: 100, AllowedChatID: 200})
	for _, update := range []telegram.Update{{Message: &telegram.Message{Text: "prompt", From: telegram.User{ID: 101}, Chat: telegram.Chat{ID: 200, Type: "private"}}}, {Message: &telegram.Message{Text: "prompt", From: telegram.User{ID: 100}, Chat: telegram.Chat{ID: 200, Type: "group"}}}} {
		if err := h.Handle(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	if c.submits != 0 {
		t.Fatalf("submits=%d", c.submits)
	}
}

func TestBridgeApprovalRoundTripUsesOneSharedThread(t *testing.T) {
	c := &coordinator{}
	m := &recordingMessenger{}
	a := &approvals{}
	r := &responder{}
	h := telegram.NewHandler(telegram.HandlerOptions{Coordinator: c, Messenger: m, AllowedUserID: 100, AllowedChatID: 200, ApprovalService: a, ApprovalResponder: r})
	message := telegram.Update{Message: &telegram.Message{Text: "change", From: telegram.User{ID: 100}, Chat: telegram.Chat{ID: 200, Type: "private"}}}
	if err := h.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if c.submits != 1 {
		t.Fatalf("submits=%d", c.submits)
	}
	if err := h.OnEvent(context.Background(), codex.Event{Method: "approval/request", ThreadID: "thr-1", TurnID: "turn-1", RequestID: json.RawMessage(`1`), Text: "show status"}); err != nil {
		t.Fatal(err)
	}
	if m.keyboard == nil || len(m.keyboard.InlineKeyboard) == 0 {
		t.Fatal("missing approval keyboard")
	}
	callback := m.keyboard.InlineKeyboard[0][0].CallbackData
	update := telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "cb", From: telegram.User{ID: 100}, Data: callback, Message: &telegram.Message{Chat: telegram.Chat{ID: 200, Type: "private"}}}}
	if err := h.Handle(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if a.resolved != 1 || r.calls != 1 {
		t.Fatalf("resolved=%d responses=%d", a.resolved, r.calls)
	}
}

func (u *updates) GetUpdates(_ context.Context, offset int64) ([]telegram.Update, error) {
	if offset != 0 {
		return nil, nil
	}
	return u.batch, nil
}
func (u *updates) UpdateOffset(context.Context) (int64, error) { return u.offset, nil }
func (u *updates) SaveUpdateOffset(_ context.Context, offset int64) error {
	u.offset = offset
	return nil
}

func TestBridgePollPersistsOffsetAndDoesNotReplayAfterRestart(t *testing.T) {
	stream := &updates{batch: []telegram.Update{{UpdateID: 42, Message: &telegram.Message{Text: "hello"}}}}
	service := app.New(&supervisor{})
	var prompts []string
	handle := func(_ context.Context, update telegram.Update) error {
		prompts = append(prompts, update.Message.Text)
		return nil
	}
	if err := service.PollOnce(context.Background(), stream, handle); err != nil {
		t.Fatal(err)
	}
	if got, want := stream.offset, int64(43); got != want {
		t.Fatalf("offset=%d want=%d", got, want)
	}
	if err := service.PollOnce(context.Background(), stream, handle); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 {
		t.Fatalf("prompts=%v", prompts)
	}
}

type supervisor struct{}

func (supervisor) Start(context.Context) (codex.AppServerEndpoint, error) {
	return codex.AppServerEndpoint{}, nil
}
func (supervisor) Stop() error { return nil }
