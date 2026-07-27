package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestSupervisorBuildsLoopbackAuthenticatedCommand(t *testing.T) {
	s := &Supervisor{
		Binary:     `C:\tools\codex.exe`,
		Listen:     "127.0.0.1:4500",
		RuntimeDir: t.TempDir(),
	}

	args, tokenFile, err := s.commandArgs([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"app-server",
		"--listen",
		"ws://127.0.0.1:4500",
		"--ws-auth",
		"capability-token",
		"--ws-token-file",
		tokenFile,
	}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d", len(args), len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	if got := tokenFile; got == "" || !strings.HasSuffix(got, "app-server-token") {
		t.Fatalf("token file = %q", got)
	}
}

func TestSupervisorStartsAfterReadinessAndCompatibility(t *testing.T) {
	server := newSupervisorCompatibilityServer(t, `{"threads":[]}`, 20*time.Millisecond)
	serverURL := server.Listener.Addr().String()
	defer server.Close()

	var commandRuns []string
	var tokenFromFile []byte
	var processLog string
	s := &Supervisor{
		Binary:           "ignored",
		Listen:           serverURL,
		RuntimeDir:       t.TempDir(),
		restartDelays:    []time.Duration{},
		readinessTimeout: 500 * time.Millisecond,
		readyInterval:    10 * time.Millisecond,
		startProcess: func(ctx context.Context, command string, args []string) (supervisorProcess, error) {
			commandRuns = append(commandRuns, command)
			commandRuns = append(commandRuns, strings.Join(args, ","))
			tokenFile := extractTokenFile(args)
			if tokenFile == "" {
				return nil, errors.New("missing token file")
			}
			data, err := os.ReadFile(tokenFile)
			if err != nil {
				return nil, err
			}
			tokenFromFile = data

			return &fakeSupervisorProcess{
				stderrPayload: "authorized token " + string(tokenFromFile),
			}, nil
		},
		versionFn: func(context.Context) (string, error) {
			return "codex-test", nil
		},
		compatibilityFn: nil,
		readinessFn:     nil,
	}

	endpoint, err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if endpoint.URL != ensureWSScheme(serverURL) {
		t.Fatalf("endpoint.URL = %q", endpoint.URL)
	}
	if string(tokenFromFile) != endpoint.Token {
		t.Fatalf("token file = %q, endpoint token = %q", tokenFromFile, endpoint.Token)
	}
	if len(commandRuns) == 0 {
		t.Fatal("process never started")
	}
	if len(commandRuns) >= 2 && commandRuns[1] != "" {
		if !strings.Contains(commandRuns[1], "app-server") {
			t.Fatalf("command args = %q", commandRuns[1])
		}
	}

	processLog = s.Logs()
	if strings.Contains(processLog, string(tokenFromFile)) {
		t.Fatalf("token leaked in logs: %q", processLog)
	}
	if !strings.Contains(processLog, "[redacted]") {
		t.Fatalf("logs were not redacted: %q", processLog)
	}
}

func TestSupervisorProbeCompatibilityRejectsMissingThreadListShape(t *testing.T) {
	server := newSupervisorCompatibilityServer(t, `{}`, 0)
	serverURL := server.Listener.Addr().String()
	defer server.Close()

	s := &Supervisor{
		Binary: "codex",
		versionFn: func(context.Context) (string, error) {
			return "codex-test", nil
		},
	}

	err := s.ProbeCompatibility(context.Background(), ensureWSScheme(serverURL), "capability-token")
	if err == nil {
		t.Fatal("ProbeCompatibility() = nil, want error")
	}
	if !errors.Is(err, ErrIncompatibleAppServer) {
		t.Fatalf("error = %v, want ErrIncompatibleAppServer", err)
	}
	if !strings.Contains(err.Error(), "codex-test") {
		t.Fatalf("error should include codex version: %v", err)
	}
}

func TestSupervisorRestartPolicyStopsAfterLimitedFailures(t *testing.T) {
	s := &Supervisor{
		Binary:           "ignored",
		Listen:           "127.0.0.1:1",
		RuntimeDir:       t.TempDir(),
		restartDelays:    []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		readinessTimeout: 50 * time.Millisecond,
		startProcess: func(context.Context, string, []string) (supervisorProcess, error) {
			return &fakeSupervisorProcess{}, nil
		},
		readinessFn: func(context.Context, string) error {
			return errors.New("readyz failed")
		},
	}

	delays := []time.Duration{}
	s.sleep = func(d time.Duration) {
		delays = append(delays, d)
	}

	_, err := s.Start(context.Background())
	if err == nil {
		t.Fatal("Start() = nil, want restart limit error")
	}
	if !errors.Is(err, ErrRestartLimitReached) {
		t.Fatalf("error = %v, want %v", err, ErrRestartLimitReached)
	}
	if len(delays) != 3 {
		t.Fatalf("sleep delays = %d, want 3", len(delays))
	}
	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for i, delay := range wantDelays {
		if delays[i] != delay {
			t.Fatalf("delay[%d] = %v, want %v", i, delays[i], delay)
		}
	}
}

type fakeSupervisorProcess struct {
	stderrPayload string
	waitErr       error
	killCalls     int
}

func (f *fakeSupervisorProcess) Wait() error {
	return f.waitErr
}

func (f *fakeSupervisorProcess) Kill() error {
	f.killCalls++
	return nil
}

func (f *fakeSupervisorProcess) Stderr() io.ReadCloser {
	if f.stderrPayload == "" {
		return io.NopCloser(strings.NewReader(""))
	}
	reader := strings.NewReader(f.stderrPayload)
	return io.NopCloser(reader)
}

func newSupervisorCompatibilityServer(t *testing.T, threadListResult string, readyAfter time.Duration) *httptest.Server {
	t.Helper()

	started := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			if readyAfter == 0 || time.Since(started) >= readyAfter {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("websocket accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			var raw json.RawMessage
			if err := wsjson.Read(context.Background(), conn, &raw); err != nil {
				return
			}

			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				continue
			}
			_ = request.Params

			var response any
			switch request.Method {
			case "initialize":
				response = map[string]any{"id": request.ID, "result": struct{}{}}
			case "thread/list":
				response = map[string]any{"id": request.ID}
				if strings.TrimSpace(threadListResult) != "" && threadListResult != "{}" {
					var decoded json.RawMessage
					_ = json.Unmarshal([]byte(threadListResult), &decoded)
					response = map[string]any{
						"id":     request.ID,
						"result": decoded,
					}
				} else {
					response = map[string]any{
						"id":     request.ID,
						"result": json.RawMessage(threadListResult),
					}
				}
			default:
				continue
			}
			if err := wsjson.Write(context.Background(), conn, response); err != nil {
				return
			}
		}
	}))
	return srv
}

func extractTokenFile(args []string) string {
	for i, arg := range args {
		if arg == "--ws-token-file" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
