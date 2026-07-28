package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultSupervisorReadyTimeout = 15 * time.Second
	defaultSupervisorPollInterval = 250 * time.Millisecond
	supervisorRestartWindow       = 5 * time.Minute
	stderrCaptureLimit            = 64 * 1024
)

var (
	ErrIncompatibleAppServer   = errors.New("incompatible app server")
	ErrRestartLimitReached     = errors.New("supervisor restart limit reached")
	ErrSupervisionNotStarted   = errors.New("supervisor not started")
	ErrMissingSupervisorConfig = errors.New("missing supervisor configuration")
)

type AppServerEndpoint struct {
	URL   string
	Token string
}

type Supervisor struct {
	Binary     string
	Listen     string
	RuntimeDir string

	startProcess func(context.Context, string, []string) (supervisorProcess, error)
	httpClient   *http.Client
	now          func() time.Time
	sleep        func(time.Duration)
	readinessFn  func(context.Context, string) error

	restartDelays    []time.Duration
	readinessTimeout time.Duration
	readyInterval    time.Duration
	versionFn        func(context.Context) (string, error)
	compatibilityFn  func(context.Context, string, string) error

	onFault func(error)

	mu      sync.Mutex
	proc    supervisorProcess
	logs    *ringBuffer
	started AppServerEndpoint
}

type supervisorProcess interface {
	Wait() error
	Kill() error
	Stderr() io.ReadCloser
}

func (s *Supervisor) ensureDefaults() {
	if s.startProcess == nil {
		s.startProcess = s.defaultStartProcess
	}
	if s.httpClient == nil {
		s.httpClient = &http.Client{Timeout: 2 * time.Second}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.sleep == nil {
		s.sleep = time.Sleep
	}
	if s.readinessTimeout == 0 {
		s.readinessTimeout = defaultSupervisorReadyTimeout
	}
	if s.readyInterval == 0 {
		s.readyInterval = defaultSupervisorPollInterval
	}
	if s.restartDelays == nil {
		s.restartDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	}
	if s.versionFn == nil {
		s.versionFn = s.defaultCodexVersion
	}
	if s.compatibilityFn == nil {
		s.compatibilityFn = s.ProbeCompatibility
	}
	if s.readinessFn == nil {
		s.readinessFn = s.waitForReadiness
	}
	if s.logs == nil {
		s.logs = newRingBuffer(stderrCaptureLimit)
	}
}

func (s *Supervisor) Start(ctx context.Context) (AppServerEndpoint, error) {
	s.ensureDefaults()

	if strings.TrimSpace(s.Binary) == "" || strings.TrimSpace(s.Listen) == "" {
		return AppServerEndpoint{}, ErrMissingSupervisorConfig
	}

	s.mu.Lock()
	procEndpoint := s.started
	running := s.proc != nil
	s.mu.Unlock()
	if running {
		return procEndpoint, nil
	}

	windowStart := s.now()
	restarts := 0

	for {
		if ctx.Err() != nil {
			return AppServerEndpoint{}, ctx.Err()
		}

		endpoint, err := s.startCycle(ctx)
		if err == nil {
			return endpoint, nil
		}

		if !s.shouldRestart(&windowStart, &restarts) {
			if s.onFault != nil {
				s.onFault(fmt.Errorf("%w: %v", ErrRestartLimitReached, err))
			}
			return AppServerEndpoint{}, fmt.Errorf("%w: %v", ErrRestartLimitReached, err)
		}
		delay := s.restartDelays[restarts]
		restarts++
		s.sleep(delay)
	}
}

func (s *Supervisor) shouldRestart(windowStart *time.Time, restarts *int) bool {
	now := s.now()
	if now.Sub(*windowStart) > supervisorRestartWindow {
		*windowStart = now
		*restarts = 0
	}

	if *restarts >= len(s.restartDelays) {
		return false
	}

	return true
}

func (s *Supervisor) startCycle(ctx context.Context) (AppServerEndpoint, error) {
	token, err := generateCapabilityToken()
	if err != nil {
		return AppServerEndpoint{}, err
	}
	args, tokenFile, err := s.commandArgs([]byte(token))
	if err != nil {
		return AppServerEndpoint{}, err
	}

	if err := ensureRuntimeDir(filepath.Dir(tokenFile)); err != nil {
		return AppServerEndpoint{}, err
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		return AppServerEndpoint{}, err
	}

	proc, err := s.startProcess(ctx, s.Binary, args)
	if err != nil {
		return AppServerEndpoint{}, fmt.Errorf("start process: %w", err)
	}

	s.captureStderr(proc, token)

	wsURL := ensureWSScheme(s.Listen)
	if err := s.readinessFn(ctx, wsURL); err != nil {
		s.stopProcess(proc)
		return AppServerEndpoint{}, err
	}

	if err := s.compatibilityFn(ctx, wsURL, token); err != nil {
		s.stopProcess(proc)
		return AppServerEndpoint{}, err
	}

	s.mu.Lock()
	s.proc = proc
	s.started = AppServerEndpoint{
		URL:   wsURL,
		Token: token,
	}
	s.mu.Unlock()

	return s.started, nil
}

func (s *Supervisor) Stop() error {
	s.mu.Lock()
	proc := s.proc
	s.proc = nil
	s.mu.Unlock()
	if proc == nil {
		return nil
	}
	return proc.Kill()
}

func (s *Supervisor) Wait() error {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc == nil {
		return ErrSupervisionNotStarted
	}
	return proc.Wait()
}

func (s *Supervisor) Logs() string {
	s.ensureDefaults()
	return s.logs.String()
}

func (s *Supervisor) ProbeCompatibility(ctx context.Context, wsURL, capabilityToken string) error {
	version, err := s.versionFn(ctx)
	if err != nil {
		return s.wrapCompatibilityError(fmt.Errorf("codex --version failed: %w", err), "unknown")
	}

	client, err := Dial(ctx, wsURL, capabilityToken)
	if err != nil {
		return s.wrapCompatibilityError(fmt.Errorf("connect to websocket: %w", err), version)
	}
	defer client.Close()

	info := ClientInfo{
		Name:    "codex-tg",
		Title:   "codex-tg bridge",
		Version: "0.1.0",
	}
	if err := client.Initialize(ctx, info); err != nil {
		return s.wrapCompatibilityError(fmt.Errorf("initialize: %w", err), version)
	}

	var raw json.RawMessage
	if err := client.call(ctx, "thread/list", map[string]int{"limit": 1}, &raw); err != nil {
		return s.wrapCompatibilityError(err, version)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return s.wrapCompatibilityError(err, version)
	}
	if _, ok := payload["threads"]; !ok {
		return s.wrapCompatibilityError(errors.New("missing required thread.list result fields"), version)
	}
	return nil
}

func (s *Supervisor) commandArgs(token []byte) ([]string, string, error) {
	if strings.TrimSpace(s.Listen) == "" {
		return nil, "", ErrMissingSupervisorConfig
	}
	runtimeDir, err := s.runtimeDirectory()
	if err != nil {
		return nil, "", err
	}
	if err := ensureRuntimeDir(runtimeDir); err != nil {
		return nil, "", err
	}

	tokenFile := filepath.Join(runtimeDir, "app-server-token")
	encoded := strings.TrimSpace(string(token))
	if encoded == "" {
		return nil, "", errors.New("empty capability token")
	}
	args := []string{
		"app-server",
		"--listen", ensureWSScheme(s.Listen),
		"--ws-auth", "capability-token",
		"--ws-token-file", tokenFile,
	}

	return args, tokenFile, nil
}

func (s *Supervisor) waitForReadiness(ctx context.Context, wsURL string) error {
	readyURL := strings.TrimPrefix(wsURL, "ws://")
	readyURL = strings.TrimPrefix(readyURL, "wss://")
	readyURL = "http://" + readyURL + "/readyz"

	deadline := s.now().Add(s.readinessTimeout)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.now().After(deadline) {
			if lastErr == nil {
				return fmt.Errorf("app server not ready within timeout")
			}
			return fmt.Errorf("app server not ready within timeout: %w", lastErr)
		}

		reqCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, readyURL, nil)
		if err == nil {
			resp, err := s.httpClient.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					cancel()
					return nil
				}
				lastErr = fmt.Errorf("readyz status %s", resp.Status)
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}
		cancel()
		s.sleep(s.readyInterval)
	}
}

func (s *Supervisor) defaultStartProcess(ctx context.Context, binary string, args []string) (supervisorProcess, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCommand(cmd)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stderr.Close()
		return nil, err
	}

	return &realSupervisorProcess{
		cmd:    cmd,
		stderr: stderr,
	}, nil
}

func (s *Supervisor) defaultCodexVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, s.Binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Supervisor) captureStderr(proc supervisorProcess, sensitive string) {
	stderr := proc.Stderr()
	if stderr == nil {
		return
	}

	go func() {
		defer stderr.Close()
		buf := make([]byte, 4*1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				chunk := bytes.Clone(buf[:n])
				chunk = redactToken(chunk, sensitive)
				s.logs.Write(chunk)
				s.logs.Write([]byte("\n"))
			}
			if err != nil {
				return
			}
		}
	}()
}

func (s *Supervisor) stopProcess(proc supervisorProcess) {
	if proc == nil {
		return
	}
	_ = proc.Kill()
	_ = proc.Wait()
}

func ensureWSScheme(listen string) string {
	if strings.HasPrefix(listen, "ws://") || strings.HasPrefix(listen, "wss://") {
		return listen
	}
	return "ws://" + listen
}

func (s *Supervisor) runtimeDirectory() (string, error) {
	if strings.TrimSpace(s.RuntimeDir) != "" {
		return s.RuntimeDir, nil
	}

	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		userBase, err := os.UserConfigDir()
		if err != nil {
			return "", errors.New("runtime directory not configured")
		}
		base = userBase
	}
	return filepath.Join(base, "codex-tg", "runtime"), nil
}

func ensureRuntimeDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

func generateCapabilityToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Supervisor) wrapCompatibilityError(err error, version string) error {
	if version == "" {
		version = "unknown"
	}
	return fmt.Errorf("%w (codex %s): %v", ErrIncompatibleAppServer, version, err)
}

func redactToken(data []byte, token string) []byte {
	if token == "" {
		return data
	}
	return bytes.ReplaceAll(data, []byte(token), []byte("[redacted]"))
}

type ringBuffer struct {
	maxBytes int
	mu       sync.Mutex
	buf      bytes.Buffer
}

func newRingBuffer(maxBytes int) *ringBuffer {
	return &ringBuffer{maxBytes: maxBytes}
}

func (r *ringBuffer) Write(data []byte) {
	if r.maxBytes <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(data) == 0 {
		return
	}
	if len(data) >= r.maxBytes {
		data = data[len(data)-r.maxBytes:]
	}

	if r.buf.Len() > r.maxBytes {
		r.buf.Reset()
	}
	if r.buf.Len()+len(data) > r.maxBytes {
		extra := r.buf.Len() + len(data) - r.maxBytes
		kept := r.buf.Bytes()[extra:]
		tmp := append([]byte{}, kept...)
		tmp = append(tmp, data...)
		r.buf.Reset()
		r.buf.Write(tmp)
		return
	}
	r.buf.Write(data)
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

type realSupervisorProcess struct {
	cmd    *exec.Cmd
	stderr io.ReadCloser
}

func (p *realSupervisorProcess) Wait() error {
	return p.cmd.Wait()
}

func (p *realSupervisorProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
		return err
	}
	return nil
}

func (p *realSupervisorProcess) Stderr() io.ReadCloser {
	return p.stderr
}
