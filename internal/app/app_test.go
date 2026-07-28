package app

import (
	"context"
	"testing"

	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/telegram"
)

type fakeSupervisor struct{ starts, stops int }

func (f *fakeSupervisor) Start(context.Context) (codex.AppServerEndpoint, error) {
	f.starts++
	return codex.AppServerEndpoint{URL: "ws://127.0.0.1:4500", Token: "runtime"}, nil
}

type fakeUpdates struct {
	offset int64
	saved  int64
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
