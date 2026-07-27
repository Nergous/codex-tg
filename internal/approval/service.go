package approval

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

var ErrNoRequestID = errors.New("approval request id is required")

const defaultApprovalTTL = 5 * time.Minute

type Decision string

const (
	ApproveOnce Decision = "approve_once"
	Deny        Decision = "deny"
	CancelTask  Decision = "cancel_task"
)

type Request struct {
	ChatID   int64
	ThreadID string
	RPCID    json.RawMessage
	Kind     string
	Summary  string
}

type Service struct {
	now  func() time.Time
	rand func() ([16]byte, error)
	db   *state.Store
}

type ServiceOption func(*Service)

func WithNow(now func() time.Time) ServiceOption {
	return func(s *Service) {
		s.now = now
	}
}

func WithRandSource(source func() ([16]byte, error)) ServiceOption {
	return func(s *Service) {
		s.rand = source
	}
}

func New(store *state.Store, opts ...ServiceOption) *Service {
	service := &Service{
		now:  time.Now,
		rand: randomNonce,
		db:   store,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

func (s *Service) Request(ctx context.Context, req Request) (string, error) {
	if s.db == nil {
		return "", errors.New("approval service: nil store")
	}
	if len(req.RPCID) == 0 {
		return "", ErrNoRequestID
	}

	nonce, err := generateNonce(s.rand)
	if err != nil {
		return "", err
	}

	approval := &models.Approval{
		Nonce:     nonce,
		RequestID: string(req.RPCID),
		ThreadID:  req.ThreadID,
		ChatID:    req.ChatID,
		Kind:      req.Kind,
		ExpiresAt: s.now().Add(defaultApprovalTTL).Unix(),
	}
	if err := s.db.CreateApproval(ctx, approval); err != nil {
		return "", err
	}
	return nonce, nil
}

func (s *Service) Resolve(ctx context.Context, chatID int64, nonce string, decision Decision) error {
	if s.db == nil {
		return errors.New("approval service: nil store")
	}
	if nonce == "" {
		return errors.New("approval service: empty nonce")
	}
	if decision != ApproveOnce && decision != Deny && decision != CancelTask {
		return fmt.Errorf("approval service: invalid decision %q", decision)
	}
	return s.db.ResolveApproval(ctx, chatID, nonce, string(decision))
}

func generateNonce(rng func() ([16]byte, error)) (string, error) {
	seed, err := rng()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(seed[:]), nil
}

func randomNonce() ([16]byte, error) {
	var raw [16]byte
	_, err := rand.Read(raw[:])
	return raw, err
}
