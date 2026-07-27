package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/testutil"
)

func TestClient_GetUpdates(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	fake.EnqueueUpdate(testutil.Update{
		UpdateID: 10,
		Message: &testutil.Message{
			MessageID: 1,
			Text:      "hello",
		},
	})
	fake.EnqueueUpdate(testutil.Update{
		UpdateID: 11,
		Message: &testutil.Message{
			MessageID: 2,
			Text:      "world",
		},
	})

	client := NewClient(fake.URL(), fake.Token(), fake.HTTPClient())
	updates, err := client.GetUpdates(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetUpdates() error = %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("GetUpdates() len = %d, want 2", len(updates))
	}
	if fake.LastOffset() != 10 {
		t.Fatalf("last offset = %d, want 10", fake.LastOffset())
	}
	if fake.LastGetUpdatesLimit() != 100 {
		t.Fatalf("last limit = %d, want 100", fake.LastGetUpdatesLimit())
	}
	if fake.LastGetUpdatesTimeout() != 35 {
		t.Fatalf("getUpdates timeout = %d, want 35", fake.LastGetUpdatesTimeout())
	}
}

func TestClient_RetryOn429WithRetryAfter(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	fake.EnqueueResponse("getUpdates", http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"retry","parameters":{"retry_after":7}}`)
	fake.EnqueueUpdate(testutil.Update{
		UpdateID: 1,
		Message: &testutil.Message{
			MessageID: 1,
			Text:      "ok",
		},
	})

	var sleeps []time.Duration
	client := NewClient(fake.URL(), fake.Token(), fake.HTTPClient())
	client.sleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.retryDelay = func() time.Duration { return 0 }

	updates, err := client.GetUpdates(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetUpdates() error = %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("GetUpdates() len = %d, want 1", len(updates))
	}
	if got := fake.Calls("getUpdates"); got != 2 {
		t.Fatalf("getUpdates calls = %d, want 2", got)
	}
	if len(sleeps) != 1 || sleeps[0] != 7*time.Second {
		t.Fatalf("sleep durations = %#v, want [7s]", sleeps)
	}
}

func TestClient_NoRetryOnUnauthorized(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	client := NewClient(fake.URL(), "wrong-token", fake.HTTPClient())

	_, err := client.Send(context.Background(), 1, "ping", nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want %v", err, ErrUnauthorized)
	}
	if fake.Calls("sendMessage") != 1 {
		t.Fatalf("sendMessage calls = %d, want 1", fake.Calls("sendMessage"))
	}
}

func TestClient_RetryOn5xx(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	fake.EnqueueResponse("sendMessage", http.StatusBadGateway, `{"ok":false,"description":"upstream","error_code":502}`)
	fake.EnqueueResponse("sendMessage", http.StatusOK, `{"ok":true,"result":{"message_id":77}}`)

	client := NewClient(fake.URL(), fake.Token(), fake.HTTPClient())
	client.retryDelay = func() time.Duration { return 0 }
	client.sleep = func(time.Duration) {}

	messageID, err := client.Send(context.Background(), 1, "ping", nil)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if messageID != 77 {
		t.Fatalf("messageID = %d, want 77", messageID)
	}
	if fake.Calls("sendMessage") != 2 {
		t.Fatalf("sendMessage calls = %d, want 2", fake.Calls("sendMessage"))
	}
}

func TestClient_MalformedResponse(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	fake.EnqueueResponse("sendMessage", http.StatusOK, `invalid-json`)

	client := NewClient(fake.URL(), fake.Token(), fake.HTTPClient())
	_, err := client.Send(context.Background(), 1, "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("err = %v, want %v", err, ErrAPI)
	}
}

func TestClient_RedactsTokenFromError(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	secret := "super-secret-token-" + t.Name()
	client := NewClient(fake.URL(), secret, fake.HTTPClient())

	_, err := client.Send(context.Background(), 1, "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want %v", err, ErrUnauthorized)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token")
	}
}

func TestClient_EditAndDelete(t *testing.T) {
	t.Parallel()

	fake := testutil.NewFakeTelegram(t)
	client := NewClient(fake.URL(), fake.Token(), fake.HTTPClient())

	if err := client.Edit(context.Background(), 1, 2, "updated", nil); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if err := client.DeleteWebhook(context.Background()); err != nil {
		t.Fatalf("DeleteWebhook() error = %v", err)
	}
	if fake.Calls("editMessageText") != 1 {
		t.Fatalf("editMessageText calls = %d, want 1", fake.Calls("editMessageText"))
	}
	if fake.Calls("deleteWebhook") != 1 {
		t.Fatalf("deleteWebhook calls = %d, want 1", fake.Calls("deleteWebhook"))
	}
}
