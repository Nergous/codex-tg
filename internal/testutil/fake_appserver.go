package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type FakeAppServer struct {
	t      TestingT
	server *httptest.Server
	token  string

	mu      sync.Mutex
	conn    *websocket.Conn
	frames  []json.RawMessage
	connErr error
	connSet chan struct{}
}

type TestingT interface {
	Helper()
	Cleanup(func())
	FailNow()
	Fatalf(format string, args ...any)
}

type scriptFrame struct {
	ID     any             `json:"id"`
	Method string          `json:"method,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  any             `json:"error,omitempty"`
	Params any             `json:"params,omitempty"`
	RawID  json.RawMessage `json:"-"`
}

func NewFakeAppServer(t TestingT) *FakeAppServer {
	t.Helper()

	server := &FakeAppServer{
		t:       t,
		token:   "test-appserver-token",
		connSet: make(chan struct{}),
	}
	server.server = httptest.NewServer(http.HandlerFunc(server.serveWebsocket))

	t.Cleanup(server.Close)
	return server
}

func (f *FakeAppServer) URL() string {
	return f.server.URL
}

func (f *FakeAppServer) Token() string {
	return f.token
}

func (f *FakeAppServer) Close() {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()

	if conn != nil {
		_ = conn.CloseNow()
	}
	f.server.Close()
}

func (f *FakeAppServer) Received() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]json.RawMessage, len(f.frames))
	copy(out, f.frames)
	return out
}

func (f *FakeAppServer) SendOutOfOrderResponse(ctx context.Context, id any, result any) error {
	return f.sendFrame(ctx, scriptFrame{
		ID:     id,
		Result: result,
	})
}

func (f *FakeAppServer) SendNotification(ctx context.Context, method string, params any) error {
	return f.sendFrame(ctx, scriptFrame{
		Method: method,
		Params: params,
	})
}

func (f *FakeAppServer) SendServerRequest(ctx context.Context, id any, method string, params any) error {
	return f.sendFrame(ctx, scriptFrame{
		ID:     id,
		Method: method,
		Params: params,
	})
}

func (f *FakeAppServer) Disconnect() {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if conn == nil {
		f.t.Fatalf("disconnect called before websocket connection was established")
	}
	_ = conn.Close(websocket.StatusGoingAway, "server shutdown")
}

func (f *FakeAppServer) sendFrame(ctx context.Context, frame scriptFrame) error {
	conn, err := f.awaitConnection()
	if err != nil {
		return err
	}
	return wsjson.Write(ctx, conn, frame)
}

func (f *FakeAppServer) awaitConnection() (*websocket.Conn, error) {
	f.mu.Lock()
	if f.conn != nil {
		conn := f.conn
		f.mu.Unlock()
		return conn, nil
	}
	connReady := f.connSet
	f.mu.Unlock()

	select {
	case <-connReady:
		f.mu.Lock()
		conn := f.conn
		err := f.connErr
		f.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return conn, nil
	default:
		return nil, fmt.Errorf("no websocket connection yet")
	}
}

func (f *FakeAppServer) serveWebsocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	wantAuth := "Bearer " + f.token
	if auth != wantAuth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	if f.conn != nil {
		f.mu.Unlock()
		http.Error(w, "already connected", http.StatusConflict)
		return
	}
	f.mu.Unlock()

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		f.t.Fatalf("accept websocket: %v", err)
		return
	}

	f.mu.Lock()
	f.conn = conn
	f.connErr = nil
	close(f.connSet)
	f.mu.Unlock()

	ctx := context.Background()
	for {
		var frame json.RawMessage
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			return
		}

		f.mu.Lock()
		f.frames = append(f.frames, frame)
		f.mu.Unlock()
	}
}
