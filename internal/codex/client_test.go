package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/testutil"
)

func TestIsThreadRolloutNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "missing rollout",
			err:  &RPCError{Code: -32603, Message: "no rollout found for thread id 019fa876"},
			want: true,
		},
		{
			name: "other server error",
			err:  &RPCError{Code: -32603, Message: "database unavailable"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isThreadRolloutNotFound(tt.err); got != tt.want {
				t.Fatalf("isThreadRolloutNotFound() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestIsThreadRolloutNotReady(t *testing.T) {
	err := &RPCError{
		Code: -32603,
		Message: "thread-store internal error: failed to read session metadata " +
			`C:\Users\test\.codex\sessions\rollout.jsonl: rollout at ` +
			`C:\Users\test\.codex\sessions\rollout.jsonl is empty`,
	}
	if !isThreadRolloutNotReady(err) {
		t.Fatal("isThreadRolloutNotReady() = false, want true")
	}
}

func TestClientParsesNestedThreadStartedEvent(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := fake.SendNotification(context.Background(), "thread/started", map[string]any{
		"thread": map[string]any{"id": "thr-tui"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-client.Events():
		if event.Method != "thread/started" || event.ThreadID != "thr-tui" {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for thread/started")
	}
}

func TestClientNormalizesV2TurnAndAgentDeltaEvents(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := fake.SendNotification(context.Background(), "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-1",
		"delta":    "answer",
	}); err != nil {
		t.Fatal(err)
	}
	delta := <-client.Events()
	if delta.ThreadID != "thr-1" || delta.TurnID != "turn-1" || delta.ItemID != "item-1" || delta.Text != "answer" {
		t.Fatalf("delta=%+v", delta)
	}

	if err := fake.SendNotification(context.Background(), "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	}); err != nil {
		t.Fatal(err)
	}
	completed := <-client.Events()
	if completed.ThreadID != "thr-1" || completed.TurnID != "turn-1" {
		t.Fatalf("completed=%+v", completed)
	}
}

func TestClientInitializesStartsThreadAndStreamsCompletion(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)

	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)
	go func() {
		seen := map[string]struct{}{}
		for {
			select {
			case <-scriptCtx.Done():
				return
			case <-time.After(time.Millisecond * 5):
			}

			frame, ok := nextIncomingFrame(t, fake, time.Second, seen, nil)
			if !ok {
				continue
			}
			switch frame.method {
			case "thread/start":
				_ = fake.SendOutOfOrderResponse(scriptCtx, frame.id, map[string]any{
					"thread": map[string]any{"id": "thr-1"},
				})
			case "turn/start":
				_ = fake.SendOutOfOrderResponse(scriptCtx, frame.id, map[string]any{
					"turn": map[string]any{"id": "turn-1"},
				})
				_ = fake.SendNotification(scriptCtx, "turn/completed", map[string]any{
					"threadId": "thr-1",
					"turnId":   "turn-1",
					"text":     "done",
				})
			}
		}
	}()

	t.Cleanup(func() { _ = client.Close() })

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	threadID, err := client.StartThread(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if threadID != "thr-1" {
		t.Fatalf("thread = %q", threadID)
	}

	turnID, err := client.StartTurn(context.Background(), threadID, "continue")
	if err != nil {
		t.Fatal(err)
	}

	event := <-client.Events()
	if event.Method != "turn/completed" || event.TurnID != turnID {
		t.Fatalf("event = %#v", event)
	}
}

func TestClientHandlesServerInitiatedRequests(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)

	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	serverReqID := json.RawMessage(`"srv-1"`)
	if err := fake.SendServerRequest(context.Background(), "srv-1", "approval/request", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"text":     "approve",
	}); err != nil {
		t.Fatal(err)
	}

	event := <-client.Events()
	if event.Method != "approval/request" {
		t.Fatalf("event = %#v", event)
	}
	if string(event.RequestID) != string(serverReqID) {
		t.Fatalf("request id mismatch: got %s want %s", event.RequestID, serverReqID)
	}

	if err := client.Respond(context.Background(), event.RequestID, map[string]any{"approved": true}); err != nil {
		t.Fatal(err)
	}

	got := waitForFrameWithID(t, fake, serverReqID)
	if got == nil {
		t.Fatal("did not receive response")
	}
	var response struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result any             `json:"result"`
	}
	if err := json.Unmarshal(*got, &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != string(serverReqID) || response.Method != "" || response.Result == nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestClientRejectsInvalidRespondID(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)

	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	cases := [][]byte{
		nil,
		[]byte{},
		[]byte("null"),
		[]byte("true"),
		[]byte(`{}`),
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("id_%d", i), func(t *testing.T) {
			err := client.Respond(context.Background(), tc, map[string]any{"ok": true})
			if !errors.Is(err, ErrInvalidRequestID) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestClientInitializeIsIdempotent(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	respondToInitialize(t, fake, context.Background())

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Initialize() error = %v, want %v", err, ErrAlreadyInitialized)
	}
}

func TestClientExtractsThreadAndTurnIDsFromNestedResults(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)

	scriptSeen := map[string]struct{}{}
	go func() {
		for {
			frame, ok := nextIncomingFrame(t, fake, time.Second, scriptSeen, func(frame incomingFrame) bool {
				return frame.method == "thread/start" || frame.method == "turn/start"
			})
			if !ok {
				continue
			}

			switch frame.method {
			case "thread/start":
				_ = fake.SendOutOfOrderResponse(scriptCtx, frame.id, map[string]any{"thread": map[string]any{"id": "thread-nested"}})
			case "turn/start":
				_ = fake.SendOutOfOrderResponse(scriptCtx, frame.id, map[string]any{"turn": map[string]any{"id": "turn-nested"}})
			}
		}
	}()

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	threadID, err := client.StartThread(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if threadID != "thread-nested" {
		t.Fatalf("threadID = %q, want thread-nested", threadID)
	}

	turnID, err := client.StartTurn(context.Background(), threadID, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if turnID != "turn-nested" {
		t.Fatalf("turnID = %q, want turn-nested", turnID)
	}
}

func TestClientConcurrentRequestsReceiveResponsesOutOfOrder(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	requests := make(chan incomingFrame, 2)
	seen := map[string]struct{}{}
	go func() {
		for {
			frame, ok := nextIncomingFrame(t, fake, time.Second, seen, func(f incomingFrame) bool {
				return f.method == "thread/start"
			})
			if !ok {
				continue
			}

			select {
			case requests <- frame:
			default:
			}

			if len(requests) == 2 {
				first := <-requests
				second := <-requests
				_ = fake.SendOutOfOrderResponse(scriptCtx, second.id, map[string]any{
					"thread": map[string]any{"id": "thread-two"},
				})
				_ = fake.SendOutOfOrderResponse(scriptCtx, first.id, map[string]any{
					"thread": map[string]any{"id": "thread-one"},
				})
				return
			}
		}
	}()

	out := make(chan string, 2)
	go func() {
		id, err := client.StartThread(context.Background(), "thread-one")
		if err != nil {
			out <- "ERR:" + err.Error()
			return
		}
		out <- id
	}()
	go func() {
		id, err := client.StartThread(context.Background(), "thread-two")
		if err != nil {
			out <- "ERR:" + err.Error()
			return
		}
		out <- id
	}()

	got := map[string]struct{}{}
	for range 2 {
		value := <-out
		if strings.HasPrefix(value, "ERR:") {
			t.Fatal(value)
		}
		got[value] = struct{}{}
	}
	if _, ok := got["thread-one"]; !ok {
		t.Fatalf("results = %+v, want thread-one", got)
	}
	if _, ok := got["thread-two"]; !ok {
		t.Fatalf("results = %+v, want thread-two", got)
	}
}

func TestClientCancelOneCallWithoutCancelingOthers(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	scriptCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	respondToInitialize(t, fake, scriptCtx)

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	cancelCtx, cancelCall := context.WithCancel(context.Background())
	t.Cleanup(cancelCall)

	seen := map[string]struct{}{}
	keepID := make(chan json.RawMessage, 1)
	go func() {
		for {
			frame, ok := nextIncomingFrame(t, fake, time.Second, seen, func(f incomingFrame) bool {
				return f.method == "thread/start"
			})
			if !ok {
				continue
			}

			var payload struct {
				Params struct {
					CWD string `json:"cwd"`
				} `json:"params"`
			}
			if err := json.Unmarshal(frame.raw, &payload); err != nil {
				continue
			}

			switch payload.Params.CWD {
			case "cancel-me":
				cancelCall()
			case "keep-going":
				select {
				case keepID <- frame.id:
				default:
				}
				_ = fake.SendOutOfOrderResponse(scriptCtx, frame.id, map[string]any{
					"thread": map[string]any{"id": "thread-kept"},
				})
			}
		}
	}()

	results := make(chan error, 2)
	go func() { _, err := client.StartThread(cancelCtx, "cancel-me"); results <- err }()
	go func() { _, err := client.StartThread(context.Background(), "keep-going"); results <- err }()

	var keptID json.RawMessage
	select {
	case keptID = <-keepID:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting keep-going request")
	}
	_ = keptID

	var canceled bool
	var kept bool
	for range 2 {
		err := <-results
		switch {
		case errors.Is(err, context.Canceled):
			canceled = true
		case err == nil:
			kept = true
		default:
			t.Fatalf("call failed unexpectedly: %v", err)
		}
	}
	if !canceled {
		t.Fatal("expected one canceled call")
	}
	if !kept {
		t.Fatal("expected one successful non-canceled call")
	}
}

func TestClientAbruptDisconnectFailsPendingCalls(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	respondToInitialize(t, fake, context.Background())

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}

	results := make(chan error, 2)
	go func() { _, err := client.StartThread(context.Background(), "one"); results <- err }()
	go func() { _, err := client.StartThread(context.Background(), "two"); results <- err }()

	// Wait for both calls to be in-flight.
	_, ok := nextIncomingFrame(t, fake, time.Second, seen, func(f incomingFrame) bool {
		return f.method == "thread/start"
	})
	if !ok {
		t.Fatal("timeout waiting thread/start")
	}
	_, ok = nextIncomingFrame(t, fake, time.Second, seen, func(f incomingFrame) bool {
		return f.method == "thread/start"
	})
	if !ok {
		t.Fatal("timeout waiting second thread/start")
	}

	fake.Disconnect()

	for range 2 {
		err := <-results
		if err == nil || !strings.Contains(err.Error(), ErrDisconnected.Error()) {
			t.Fatalf("pending call err = %v, want disconnected", err)
		}
	}
}

func TestClientDoesNotAutoRespondToApprovalRequests(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	respondToInitialize(t, fake, context.Background())

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	if err := fake.SendServerRequest(context.Background(), "approval-autop", "approval/request", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"text":     "approve",
	}); err != nil {
		t.Fatal(err)
	}

	event := <-client.Events()
	if event.Method != "approval/request" {
		t.Fatalf("event = %#v", event)
	}

	time.Sleep(20 * time.Millisecond)
	for _, frame := range fake.Received() {
		var parsed struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(frame, &parsed); err != nil {
			continue
		}
		if parsed.Method == "" && string(parsed.ID) == `"approval-autop"` {
			t.Fatalf("unexpected automatic response: %s", frame)
		}
	}
}

func TestClientQueuesBurstEventsWithoutDisconnecting(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	respondToInitialize(t, fake, context.Background())

	if err := client.Initialize(context.Background(), ClientInfo{
		Name:    "codex_tg",
		Title:   "Codex Telegram Bridge",
		Version: "test",
	}); err != nil {
		t.Fatal(err)
	}

	turnSeen := make(chan incomingFrame, 1)
	go func() {
		seen := map[string]struct{}{}
		for {
			frame, ok := nextIncomingFrame(t, fake, time.Second, seen, func(f incomingFrame) bool {
				return f.method == "turn/start"
			})
			if !ok {
				continue
			}
			select {
			case turnSeen <- frame:
			default:
			}
			return
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		_, err := client.StartTurn(context.Background(), "thread-backpressure", "ping")
		errCh <- err
	}()

	turnFrame := <-turnSeen
	for i := 0; i < 20; i++ {
		_ = fake.SendNotification(context.Background(), "turn/notification", map[string]any{
			"threadId": "thread-backpressure",
			"turnId":   fmt.Sprintf("turn-%d", i),
			"text":     "noisy",
		})
	}

	if err := fake.SendOutOfOrderResponse(context.Background(), turnFrame.id, map[string]any{
		"turn": map[string]any{"id": "turn-started"},
	}); err != nil {
		t.Fatal(err)
	}

	if callErr := <-errCh; callErr != nil {
		t.Fatalf("turn/start error = %v, want nil", callErr)
	}
	for i := 0; i < 20; i++ {
		select {
		case event := <-client.Events():
			if event.TurnID != fmt.Sprintf("turn-%d", i) {
				t.Fatalf("event %d turn = %q", i, event.TurnID)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestClientErrorsDoNotLeakAuthorizationToken(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	secretToken := "super-secret-token-" + t.Name()

	_, err := Dial(context.Background(), fake.URL(), secretToken)
	if err == nil {
		t.Fatal("Dial() succeeded with invalid token")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
		t.Fatalf("Dial() error = %q, want 401/unauthorized", err)
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("error leaks token: %q", err)
	}
}

func TestClientAcceptsLargeAppServerMessages(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	want := strings.Repeat("x", 64*1024)
	if err := fake.SendNotification(context.Background(), "item/agentMessage/delta", map[string]any{
		"threadId": "thr-large",
		"turnId":   "turn-large",
		"text":     want,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-client.Events():
		if event.Text != want {
			t.Fatalf("event text length = %d, want %d", len(event.Text), len(want))
		}
	case err := <-client.Errors():
		t.Fatalf("client disconnected: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for large event")
	}
}

func TestStartThreadExplicitlyPersistsSession(t *testing.T) {
	fake := testutil.NewFakeAppServer(t)
	client, err := Dial(context.Background(), fake.URL(), fake.Token())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	respondToInitialize(t, fake, context.Background())
	if err := client.Initialize(context.Background(), ClientInfo{Name: "test", Title: "test", Version: "test"}); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		frame, ok := nextIncomingFrame(t, fake, time.Second, map[string]struct{}{}, func(f incomingFrame) bool { return f.method == "thread/start" })
		if !ok {
			errCh <- errors.New("thread/start not received")
			return
		}
		var payload struct {
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(frame.raw, &payload); err != nil {
			errCh <- err
			return
		}
		value, exists := payload.Params["ephemeral"]
		responseErr := fake.SendOutOfOrderResponse(context.Background(), frame.id, map[string]any{"thread": map[string]any{"id": "thr-persisted"}})
		if responseErr != nil {
			errCh <- responseErr
			return
		}
		if !exists || value != false {
			errCh <- fmt.Errorf("ephemeral = %v, exists=%t", value, exists)
			return
		}
		errCh <- nil
	}()

	if _, err := client.StartThread(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

type incomingFrame struct {
	raw    json.RawMessage
	id     json.RawMessage
	method string
}

func waitForFrameWithID(t *testing.T, fake *testutil.FakeAppServer, id json.RawMessage) *json.RawMessage {
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond * 10)
	defer tick.Stop()

	seen := make(map[string]struct{})
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for frame id %s", id)
			return nil
		case <-tick.C:
			for _, frame := range fake.Received() {
				key := string(frame)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				var parsed struct {
					ID json.RawMessage `json:"id"`
				}
				if err := json.Unmarshal(frame, &parsed); err != nil {
					continue
				}
				if string(parsed.ID) == string(id) {
					out := frame
					return &out
				}
			}
		}
	}
}

func nextIncomingFrame(
	t *testing.T,
	fake *testutil.FakeAppServer,
	timeout time.Duration,
	skip map[string]struct{},
	match func(incomingFrame) bool,
) (incomingFrame, bool) {
	deadline := time.After(timeout)
	tick := time.NewTicker(time.Millisecond * 5)
	defer tick.Stop()

	for {
		select {
		case <-deadline:
			return incomingFrame{}, false
		case <-tick.C:
			for _, frame := range fake.Received() {
				key := string(frame)
				if _, ok := skip[key]; ok {
					continue
				}
				skip[key] = struct{}{}

				var parsed struct {
					Method string          `json:"method"`
					ID     json.RawMessage `json:"id"`
				}
				if err := json.Unmarshal(frame, &parsed); err != nil {
					continue
				}
				incoming := incomingFrame{raw: frame, id: parsed.ID, method: parsed.Method}
				if match != nil && !match(incoming) {
					continue
				}
				return incoming, true
			}
		}
	}
}

func respondToInitialize(t *testing.T, fake *testutil.FakeAppServer, ctx context.Context) {
	scriptCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case <-scriptCtx.Done():
				return
			case <-time.After(time.Millisecond * 5):
			}

			for _, frame := range fake.Received() {
				var parsed struct {
					Method string `json:"method"`
					ID     any    `json:"id"`
				}
				if err := json.Unmarshal(frame, &parsed); err != nil {
					t.Errorf("failed to parse received frame: %v", err)
					continue
				}
				if parsed.Method != "initialize" {
					continue
				}
				_ = fake.SendOutOfOrderResponse(scriptCtx, parsed.ID, struct{}{})
			}
		}
	}()
}
