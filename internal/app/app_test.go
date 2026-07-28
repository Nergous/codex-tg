package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/telegram"
)

func TestPumpCodexEventsRendersAndCompletesTerminalTurn(t *testing.T) {
	events := make(chan codex.Event, 2)
	events <- codex.Event{Method: "item/agentMessage/delta", ThreadID: "thr-1", TurnID: "turn-1"}
	events <- codex.Event{Method: "turn/completed", ThreadID: "thr-1", TurnID: "turn-1"}
	close(events)

	var rendered []string
	var completed []string
	err := PumpCodexEvents(
		context.Background(),
		events,
		func(_ context.Context, event codex.Event) error {
			rendered = append(rendered, event.Method)
			return nil
		},
		func(_ context.Context, threadID, turnID string) error {
			completed = append(completed, threadID+":"+turnID)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"item/agentMessage/delta", "turn/completed"}; !reflect.DeepEqual(rendered, want) {
		t.Fatalf("rendered=%v want=%v", rendered, want)
	}
	if want := []string{"thr-1:turn-1"}; !reflect.DeepEqual(completed, want) {
		t.Fatalf("completed=%v want=%v", completed, want)
	}
}

type fakeSupervisor struct{ starts, stops int }

func (f *fakeSupervisor) Start(context.Context) (codex.AppServerEndpoint, error) {
	f.starts++
	return codex.AppServerEndpoint{URL: "ws://127.0.0.1:4500", Token: "runtime"}, nil
}

type fakeUpdates struct {
	offset int64
	saved  int64
}

type blockingUpdates struct {
	called  bool
	saved   int64
	savedCh chan struct{}
}

func (f *blockingUpdates) GetUpdates(ctx context.Context, _ int64) ([]telegram.Update, error) {
	if !f.called {
		f.called = true
		return []telegram.Update{{UpdateID: 7}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingUpdates) UpdateOffset(context.Context) (int64, error) { return 0, nil }
func (f *blockingUpdates) SaveUpdateOffset(_ context.Context, offset int64) error {
	f.saved = offset
	close(f.savedCh)
	return nil
}

func TestRunBridgeConsumesTelegramAndCodexEvents(t *testing.T) {
	updates := &blockingUpdates{savedCh: make(chan struct{})}
	events := make(chan codex.Event, 1)
	go func() {
		<-updates.savedCh
		events <- codex.Event{Method: "turn/completed", ThreadID: "thr-1", TurnID: "turn-1"}
		close(events)
	}()

	service := New(&fakeSupervisor{})
	telegramCalls := 0
	codexCalls := 0
	completeCalls := 0
	err := service.RunBridge(
		context.Background(),
		updates,
		func(context.Context, telegram.Update) error { telegramCalls++; return nil },
		events,
		func(context.Context, codex.Event) error { codexCalls++; return nil },
		func(context.Context, string, string) error { completeCalls++; return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if telegramCalls != 1 || updates.saved != 8 || codexCalls != 1 || completeCalls != 1 {
		t.Fatalf("telegram=%d offset=%d codex=%d complete=%d", telegramCalls, updates.saved, codexCalls, completeCalls)
	}
}

func (f *fakeUpdates) GetUpdates(context.Context, int64) ([]telegram.Update, error) {
	return []telegram.Update{{UpdateID: 42}}, nil
}
func (f *fakeUpdates) UpdateOffset(context.Context) (int64, error)       { return f.offset, nil }
func (f *fakeUpdates) SaveUpdateOffset(_ context.Context, v int64) error { f.saved = v; return nil }
func TestPollOncePersistsOffsetAfterHandler(t *testing.T) {
	f := &fakeUpdates{}
	s := New(&fakeSupervisor{})
	called := 0
	if err := s.PollOnce(context.Background(), f, func(context.Context, telegram.Update) error { called++; return nil }); err != nil {
		t.Fatal(err)
	}
	if called != 1 || f.saved != 43 {
		t.Fatalf("called=%d offset=%d", called, f.saved)
	}
}

func TestServiceStartsIPC(t *testing.T) {
	service := New(&fakeSupervisor{})
	service.open = func(context.Context, string, bool) (string, error) { return "thr-1", nil }
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	address, err := service.StartIPC(context.Background(), "control-token")
	if err != nil {
		t.Fatal(err)
	}
	if address == "" {
		t.Fatal("empty address")
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *fakeSupervisor) Stop() error { f.stops++; return nil }

func TestServiceStartAndStop(t *testing.T) {
	fake := &fakeSupervisor{}
	service := New(fake)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.starts != 1 {
		t.Fatalf("starts=%d", fake.starts)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.stops != 1 {
		t.Fatalf("stops=%d", fake.stops)
	}
}

func TestServiceRunsRecoveryBeforeSupervisor(t *testing.T) {
	fake := &fakeSupervisor{}
	service := New(fake)
	called := false
	service.recover = func(context.Context) error { called = true; return nil }
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called || fake.starts != 1 {
		t.Fatalf("recovered=%v starts=%d", called, fake.starts)
	}
}

func TestServiceOpenReturnsRuntimeEndpoint(t *testing.T) {
	service := New(&fakeSupervisor{})
	service.open = func(context.Context, string, bool) (string, error) { return "thr-1", nil }
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := service.Open(context.Background(), ipc.OpenRequest{ProjectPath: "D:\\repo"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "thr-1" || got.Endpoint != "ws://127.0.0.1:4500" || got.Token != "runtime" {
		t.Fatalf("open=%+v", got)
	}
}
