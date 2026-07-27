package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Nergous/codex-tg/internal/codex"
)

type renderFakeMessenger struct {
	mu    sync.Mutex
	sends []sendCall
	edits []struct {
		chatID    int64
		messageID int64
		text      string
		opts      MessageOptions
	}
	sendErr func(chatID int64, text string, opts MessageOptions) error
	editErr error
}

func (f *renderFakeMessenger) Send(_ context.Context, chatID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) (int64, error) {
	if f.sendErr != nil {
		if err := f.sendErr(chatID, text, opts); err != nil {
			return 0, err
		}
	}
	f.mu.Lock()
	f.sends = append(f.sends, sendCall{
		chatID:   chatID,
		text:     text,
		keyboard: keyboard,
		opts:     opts,
	})
	id := int64(len(f.sends))
	f.mu.Unlock()
	return id, nil
}

func (f *renderFakeMessenger) Edit(_ context.Context, chatID, messageID int64, text string, _ *InlineKeyboard, opts MessageOptions) error {
	if f.editErr != nil {
		return f.editErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, struct {
		chatID    int64
		messageID int64
		text      string
		opts      MessageOptions
	}{chatID: chatID, messageID: messageID, text: text, opts: opts})
	return nil
}

func (f *renderFakeMessenger) AnswerCallback(_ context.Context, _, _ string) error {
	return nil
}

func (f *renderFakeMessenger) lastSend() sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends[len(f.sends)-1]
}

func (f *renderFakeMessenger) snapshotSends() []sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sendCall, len(f.sends))
	copy(out, f.sends)
	return out
}

func (f *renderFakeMessenger) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *renderFakeMessenger) editCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.edits)
}

func testEvent(method, threadID, turnID, raw string) codex.Event {
	return codex.Event{
		Method:   method,
		ThreadID: threadID,
		TurnID:   turnID,
		Raw:      json.RawMessage(raw),
	}
}

func TestRendererProgressIsThrottledByDefault(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.Local)
	messenger := &renderFakeMessenger{}
	r := NewRenderer(RendererOptions{
		Messenger: messenger,
		Now: func() time.Time {
			return now
		},
	})

	r.SetThread(1001, "thr-1", "/repo/demo")

	if err := r.OnEvent(context.Background(), testEvent("item/started", "thr-1", "turn-1", `{"activity":"initializing","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if got := messenger.sendCount(); got != 1 {
		t.Fatalf("send calls = %d, want 1", got)
	}

	now = now.Add(time.Second)
	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":"one ","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if got := messenger.editCount(); got != 0 {
		t.Fatalf("edit calls = %d, want 0", got)
	}

	now = now.Add(time.Second)
	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":"two","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if got := messenger.editCount(); got != 1 {
		t.Fatalf("edit calls = %d, want 1", got)
	}
	if messenger.lastSend().text == "" {
		t.Fatalf("missing progress payload")
	}

	now = now.Add(time.Millisecond)
	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":" three","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if got := messenger.editCount(); got != 1 {
		t.Fatalf("edit calls = %d, want 1", got)
	}
}

func TestRendererBuildsFinalFromAgentMessageLifecycle(t *testing.T) {
	now := time.Now()
	messenger := &renderFakeMessenger{}
	r := NewRenderer(RendererOptions{
		Messenger:      messenger,
		Now:            func() time.Time { return now },
		HeartbeatDelay: 24 * time.Hour,
	})
	r.SetThread(1001, "thr-1", "/repo/demo")

	if err := r.OnEvent(context.Background(), testEvent("item/started", "thr-1", "turn-1", `{"activity":"analyzing","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("started error = %v", err)
	}
	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":"hello ","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("delta error = %v", err)
	}
	if err := r.OnEvent(context.Background(), testEvent("item/completed", "thr-1", "turn-1", `{"text":"world","threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("completed error = %v", err)
	}
	if err := r.OnEvent(context.Background(), testEvent("turn/completed", "thr-1", "turn-1", `{"threadId":"thr-1","turnId":"turn-1"}`)); err != nil {
		t.Fatalf("turn/completed error = %v", err)
	}

	if got := messenger.sendCount(); got < 2 {
		t.Fatalf("final/satus sends = %d, want >=2", got)
	}
	snapshot := messenger.snapshotSends()
	final := snapshot[len(snapshot)-1]
	if got := final.text; got != "hello world" {
		t.Fatalf("final response = %q, want %q", got, "hello world")
	}
	if final.opts.ParseMode != "MarkdownV2" {
		t.Fatalf("final parse mode = %q, want MarkdownV2", final.opts.ParseMode)
	}
}

func TestRendererSplitsLargeFinalOutputAt3900UTF8(t *testing.T) {
	now := time.Now()
	huge := make([]byte, defaultFinalChunkBytes*2)
	for i := range huge {
		huge[i] = 'a'
	}

	messenger := &renderFakeMessenger{}
	r := NewRenderer(RendererOptions{
		Messenger:      messenger,
		Now:            func() time.Time { return now },
		HeartbeatDelay: 24 * time.Hour,
	})
	r.SetThread(1001, "thr-1", "/repo/demo")

	hugeEvent, _ := json.Marshal(string(huge))
	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":`+string(hugeEvent)+`}`)); err != nil {
		t.Fatalf("delta error = %v", err)
	}
	if err := r.OnEvent(context.Background(), testEvent("turn/completed", "thr-1", "turn-1", `{}`)); err != nil {
		t.Fatalf("turn/completed error = %v", err)
	}

	sendCalls := messenger.snapshotSends()
	if len(sendCalls) != 3 {
		t.Fatalf("send calls = %d, want 3", len(sendCalls))
	}
	for i, call := range sendCalls[1:] {
		if len(call.text) > defaultFinalChunkBytes {
			t.Fatalf("final chunk %d len = %d, want <= %d", i, len(call.text), defaultFinalChunkBytes)
		}
		if !utf8.ValidString(call.text) {
			t.Fatalf("final chunk %d is not valid utf-8", i)
		}
	}
	if sendCalls[len(sendCalls)-1].text != string(huge)[defaultFinalChunkBytes:] {
		t.Fatalf("unexpected final chunk text")
	}
}

func TestRendererFallsBackToPlainTextOnMarkdownParseError(t *testing.T) {
	messenger := &renderFakeMessenger{
		sendErr: func(_ int64, text string, opts MessageOptions) error {
			if opts.ParseMode == "MarkdownV2" {
				return errors.New("parse error")
			}
			return nil
		},
	}
	r := NewRenderer(RendererOptions{
		Messenger:      messenger,
		Now:            time.Now,
		HeartbeatDelay: 24 * time.Hour,
	})
	r.SetThread(1001, "thr-1", "/repo/demo")

	if err := r.OnEvent(context.Background(), testEvent("item/agentMessage/delta", "thr-1", "turn-1", `{"text":"first"}`)); err != nil {
		t.Fatalf("delta error = %v", err)
	}
	if err := r.OnEvent(context.Background(), testEvent("turn/completed", "thr-1", "turn-1", `{}`)); err != nil {
		t.Fatalf("turn/completed error = %v", err)
	}

	sendCalls := messenger.snapshotSends()
	if len(sendCalls) != 2 {
		t.Fatalf("send calls = %d, want 2", len(sendCalls))
	}
	plain := sendCalls[1]
	if plain.opts.ParseMode != "" {
		t.Fatalf("fallback parse mode = %q, want empty", plain.opts.ParseMode)
	}
	if plain.text != "first" {
		t.Fatalf("fallback payload = %q, want %q", plain.text, "first")
	}
}
