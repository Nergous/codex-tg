package approval

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

func TestApprovalNonceIsSingleUseAndBoundToChat(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, time.Now)
	reqNonce, err := svc.Request(context.Background(), Request{
		ChatID:   200,
		ThreadID: "thr-1",
		RPCID:    json.RawMessage(`12`),
		Kind:     "command",
		Summary:  "go test ./...",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Resolve(context.Background(), 201, reqNonce, Deny); !errors.Is(err, state.ErrUnauthorized) {
		t.Fatalf("wrong chat error = %v", err)
	}
	if err := svc.Resolve(context.Background(), 200, reqNonce, ApproveOnce); err != nil {
		t.Fatal(err)
	}
	if err := svc.Resolve(context.Background(), 200, reqNonce, ApproveOnce); !errors.Is(err, state.ErrAlreadyResolved) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestApprovalServiceRejectsExpiredNonce(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	svc := New(store)

	expired := time.Now().Add(-time.Minute)
	if err := store.CreateApproval(context.Background(), &models.Approval{
		Nonce:     "expired-nonce",
		RequestID: `"13"`,
		ThreadID:  "thr-2",
		ChatID:    200,
		Kind:      "command",
		ExpiresAt: expired.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Resolve(context.Background(), 200, "expired-nonce", Deny); !errors.Is(err, state.ErrExpired) {
		t.Fatalf("Resolve() error = %v, want %v", err, state.ErrExpired)
	}
}
func TestApprovalServiceRejectsUnknownDecision(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, time.Now)
	nonce, err := svc.Request(context.Background(), Request{
		ChatID:   200,
		ThreadID: "thr-2",
		RPCID:    json.RawMessage(`"13"`),
		Kind:     "command",
		Summary:  "go test ./...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Resolve(context.Background(), 200, nonce, Decision("weird")); err == nil {
		t.Fatalf("Resolve() error = %v, want invalid decision", err)
	}
}

func TestApprovalServiceRejectsEmptyRequestID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t, time.Now)
	if _, err := svc.Request(context.Background(), Request{
		ChatID:   200,
		ThreadID: "thr-3",
		RPCID:    nil,
	}); !errors.Is(err, ErrNoRequestID) {
		t.Fatalf("Request() error = %v", err)
	}
}

func newTestService(t *testing.T, now func() time.Time) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(context.Background(), "sqlite:///"+dbPath)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return New(store, WithNow(now))
}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(context.Background(), "sqlite:///"+dbPath)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
