package app

import (
	"context"
	"errors"
	"sync"

	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/telegram"
)

var ErrNotStarted = errors.New("service not started")

type Supervisor interface {
	Start(context.Context) (codex.AppServerEndpoint, error)
	Stop() error
}

type Updates interface {
	GetUpdates(context.Context, int64) ([]telegram.Update, error)
	UpdateOffset(context.Context) (int64, error)
	SaveUpdateOffset(context.Context, int64) error
}

type Service struct {
	supervisor         Supervisor
	mu                 sync.Mutex
	endpoint           codex.AppServerEndpoint
	started            bool
	open               func(context.Context, string, bool) (string, error)
	prepareInteractive func(context.Context, string) error
	adoptInteractive   func(context.Context, string, string) error
	recover            func(context.Context) error
	afterStart         func(context.Context, codex.AppServerEndpoint) error
	registerProject    func(context.Context, ipc.ProjectRequest) error
	ipc                *ipc.Server
	threadID           string
	projectPath        string
	interactivePending bool
}

func (s *Service) ConfigureProjectRegistration(register func(context.Context, ipc.ProjectRequest) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerProject = register
}

func (s *Service) RegisterProject(ctx context.Context, project ipc.ProjectRequest) error {
	s.mu.Lock()
	register := s.registerProject
	s.mu.Unlock()
	if register == nil {
		return errors.New("project registration is not configured")
	}
	return register(ctx, project)
}

type CodexEventHandler func(context.Context, codex.Event) error
type TurnCompleter func(context.Context, string, string) error

func PumpCodexEvents(ctx context.Context, events <-chan codex.Event, disconnects <-chan error, handle CodexEventHandler, complete TurnCompleter) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-disconnects:
			if ok && err != nil {
				return err
			}
			disconnects = nil
		case event, ok := <-events:
			if !ok {
				select {
				case err := <-disconnects:
					if err != nil {
						return err
					}
				default:
				}
				return codex.ErrDisconnected
			}
			if err := handle(ctx, event); err != nil {
				return err
			}
			switch event.Method {
			case "turn/completed", "turn/failed", "turn/interrupted", "turn/faulted":
				if err := complete(ctx, event.ThreadID, event.TurnID); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Service) RunBridge(
	ctx context.Context,
	updates Updates,
	handleUpdate func(context.Context, telegram.Update) error,
	events <-chan codex.Event,
	disconnects <-chan error,
	handleEvent CodexEventHandler,
	complete TurnCompleter,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, 2)
	go func() { results <- s.Poll(runCtx, updates, handleUpdate) }()
	go func() { results <- PumpCodexEvents(runCtx, events, disconnects, handleEvent, complete) }()

	err := <-results
	if errors.Is(err, context.Canceled) && ctx.Err() == nil {
		return nil
	}
	return err
}

func New(supervisor Supervisor) *Service {
	return &Service{supervisor: supervisor}
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if s.recover != nil {
		if err := s.recover(ctx); err != nil {
			return err
		}
	}
	endpoint, err := s.supervisor.Start(ctx)
	if err != nil {
		return err
	}
	s.endpoint = endpoint
	if s.afterStart != nil {
		if err := s.afterStart(ctx, endpoint); err != nil {
			_ = s.supervisor.Stop()
			s.endpoint = codex.AppServerEndpoint{}
			return err
		}
	}
	s.started = true
	return nil
}

func (s *Service) Configure(open func(context.Context, string, bool) (string, error), recover func(context.Context) error, afterStart func(context.Context, codex.AppServerEndpoint) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = open
	s.recover = recover
	s.afterStart = afterStart
}

func (s *Service) ConfigureInteractive(prepare func(context.Context, string) error, adopt func(context.Context, string, string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepareInteractive = prepare
	s.adoptInteractive = adopt
}

func (s *Service) AdoptInteractiveThread(ctx context.Context, threadID string) error {
	s.mu.Lock()
	path := s.projectPath
	adopt := s.adoptInteractive
	pending := s.interactivePending
	s.mu.Unlock()
	if !pending {
		return nil
	}
	if path == "" || threadID == "" || adopt == nil {
		return errors.New("interactive thread adoption not configured")
	}
	if err := adopt(ctx, path, threadID); err != nil {
		return err
	}
	s.mu.Lock()
	s.threadID = threadID
	s.interactivePending = false
	s.mu.Unlock()
	return nil
}

func (s *Service) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil
	}
	if s.ipc != nil {
		_ = s.ipc.Close()
		s.ipc = nil
	}
	err := s.supervisor.Stop()
	s.started = false
	s.endpoint = codex.AppServerEndpoint{}
	s.threadID = ""
	s.projectPath = ""
	s.interactivePending = false
	return err
}

func (s *Service) StartIPC(ctx context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return "", ErrNotStarted
	}
	if s.ipc != nil {
		return s.ipc.Address(), nil
	}
	server := ipc.NewServer(s, token)
	address, err := server.Start(ctx)
	if err != nil {
		return "", err
	}
	s.ipc = server
	return address, nil
}

func (s *Service) Endpoint() (codex.AppServerEndpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return codex.AppServerEndpoint{}, ErrNotStarted
	}
	return s.endpoint, nil
}

func (s *Service) Open(ctx context.Context, req ipc.OpenRequest) (ipc.OpenResponse, error) {
	endpoint, err := s.Endpoint()
	if err != nil {
		return ipc.OpenResponse{}, err
	}
	if req.Interactive {
		if s.prepareInteractive == nil {
			return ipc.OpenResponse{}, errors.New("interactive open service not configured")
		}
		if err := s.prepareInteractive(ctx, req.ProjectPath); err != nil {
			return ipc.OpenResponse{}, err
		}
		s.mu.Lock()
		s.threadID = ""
		s.projectPath = req.ProjectPath
		s.interactivePending = true
		s.mu.Unlock()
		return ipc.OpenResponse{Endpoint: endpoint.URL, Token: endpoint.Token}, nil
	}
	if s.open == nil {
		return ipc.OpenResponse{}, errors.New("open service not configured")
	}
	thread, err := s.open(ctx, req.ProjectPath, req.NewSession)
	if err != nil {
		return ipc.OpenResponse{}, err
	}
	s.mu.Lock()
	s.threadID = thread
	s.projectPath = req.ProjectPath
	s.interactivePending = false
	s.mu.Unlock()
	return ipc.OpenResponse{ThreadID: thread, Endpoint: endpoint.URL, Token: endpoint.Token}, nil
}

func (s *Service) Status(context.Context) (ipc.StatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return ipc.StatusResponse{}, ErrNotStarted
	}
	return ipc.StatusResponse{Running: s.endpoint.URL != "", ThreadID: s.threadID, ProjectPath: s.projectPath}, nil
}

func (s *Service) PollOnce(ctx context.Context, updates Updates, handle func(context.Context, telegram.Update) error) error {
	offset, err := updates.UpdateOffset(ctx)
	if err != nil {
		return err
	}
	batch, err := updates.GetUpdates(ctx, offset)
	if err != nil {
		return err
	}
	for _, update := range batch {
		if err := handle(ctx, update); err != nil {
			return err
		}
		if err := updates.SaveUpdateOffset(ctx, update.UpdateID+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Poll(ctx context.Context, updates Updates, handle func(context.Context, telegram.Update) error) error {
	for ctx.Err() == nil {
		if err := s.PollOnce(ctx, updates, handle); err != nil {
			return err
		}
	}
	return ctx.Err()
}
