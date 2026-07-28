package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/telegram"
)

func makeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex.exe")
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeSecrets struct{ value []byte }

func (f *fakeSecrets) Get(context.Context, string) ([]byte, error) { return nil, nil }
func (f *fakeSecrets) Set(_ context.Context, _ string, v []byte) error {
	f.value = append([]byte(nil), v...)
	return nil
}
func (f *fakeSecrets) Delete(context.Context, string) error { return nil }

type fakeBot struct{ me, deleted int }

func (f *fakeBot) GetMe(context.Context) (telegram.User, error) { f.me++; return telegram.User{}, nil }
func (f *fakeBot) DeleteWebhook(context.Context) error          { f.deleted++; return nil }
func TestSetupValidatesBotAndStoresOnlySecretOutsideConfig(t *testing.T) {
	token := []byte("bot-secret")
	bot := &fakeBot{}
	secrets := &fakeSecrets{}
	cfg := &config.Config{Telegram: config.TelegramConfig{AllowedUserID: 1, AllowedChatID: 2}, AppServer: config.AppServerConfig{Listen: "127.0.0.1:4500", CodexBinary: makeExecutable(t)}, Projects: []models.Project{{Name: "demo", Path: t.TempDir()}}}
	path := t.TempDir() + "\\config.json"
	if err := Setup(context.Background(), bot, secrets, token, path, cfg); err != nil {
		t.Fatal(err)
	}
	if bot.me != 1 || bot.deleted != 1 || string(secrets.value) != string(token) {
		t.Fatalf("bot=%+v secret=%q", bot, secrets.value)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Telegram.AllowedUserID != 1 {
		t.Fatalf("config=%+v err=%v", loaded, err)
	}
}
