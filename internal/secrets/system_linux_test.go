//go:build linux

package secrets

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
)

func TestLinuxStoreGetUsesSecretService(t *testing.T) {
	store := &LinuxStore{run: func(_ context.Context, input []byte, args ...string) ([]byte, error) {
		if len(input) != 0 {
			t.Fatalf("input = %q", input)
		}
		want := []string{"lookup", "service", serviceName, "name", TelegramBotToken}
		if !slices.Equal(args, want) {
			t.Fatalf("args = %q, want %q", args, want)
		}
		return []byte("telegram-token\n"), nil
	}}

	got, err := store.Get(context.Background(), TelegramBotToken)
	if err != nil || !bytes.Equal(got, []byte("telegram-token")) {
		t.Fatalf("Get() = %q, %v", got, err)
	}
}

func TestLinuxStoreSetPassesSecretThroughStdin(t *testing.T) {
	store := &LinuxStore{run: func(_ context.Context, input []byte, args ...string) ([]byte, error) {
		if !bytes.Equal(input, []byte("telegram-token")) {
			t.Fatalf("input = %q", input)
		}
		want := []string{"store", "--label=codex-tg Telegram bot token", "service", serviceName, "name", TelegramBotToken}
		if !slices.Equal(args, want) {
			t.Fatalf("args = %q, want %q", args, want)
		}
		return nil, nil
	}}

	if err := store.Set(context.Background(), TelegramBotToken, []byte("telegram-token")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxStoreMapsMissingSecret(t *testing.T) {
	store := &LinuxStore{run: func(context.Context, []byte, ...string) ([]byte, error) {
		return nil, errSecretNotFound
	}}

	_, err := store.Get(context.Background(), TelegramBotToken)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v", err)
	}
	if err := store.Delete(context.Background(), TelegramBotToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestLinuxStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	store := &LinuxStore{run: func(context.Context, []byte, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}}

	if _, err := store.Get(ctx, TelegramBotToken); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() error = %v", err)
	}
	if called {
		t.Fatal("secret-tool called after cancellation")
	}
}
