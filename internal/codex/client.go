package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	defaultEventBufferSize = 16
)

var (
	ErrNotInitialized     = errors.New("not initialized")
	ErrAlreadyInitialized = errors.New("already initialized")
	ErrDisconnected       = errors.New("app server disconnected")
	ErrEventBackpressure  = errors.New("event channel full")
	ErrInvalidRPCFrame    = errors.New("invalid rpc frame")
	ErrInvalidRequestID   = errors.New("invalid request id")
)

type Client struct {
	conn   *websocket.Conn
	nextID int64

	initialized uint32

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool

	pendingMu sync.Mutex
	pending   map[int64]chan response

	events chan Event
}

type incomingRPC struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error,omitempty"`
	Params json.RawMessage `json:"params"`
}

type outgoingResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result"`
}

func Dial(ctx context.Context, wsURL, token string) (*Client, error) {
	u, err := toWebSocketURL(wsURL)
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		return nil, err
	}

	client := &Client{
		conn:    conn,
		pending: make(map[int64]chan response),
		events:  make(chan Event, defaultEventBufferSize),
	}
	go client.readLoop()
	return client, nil
}

func (c *Client) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()

	c.failPending(ErrDisconnected)
	_ = c.conn.Close(websocket.StatusNormalClosure, "client closing")
	return nil
}

func (c *Client) Initialize(ctx context.Context, info ClientInfo) error {
	if c.conn == nil {
		return ErrDisconnected
	}
	if !atomic.CompareAndSwapUint32(&c.initialized, 0, 1) {
		return ErrAlreadyInitialized
	}

	if err := c.call(ctx, "initialize", initializeParams{ClientInfo: info}, &struct{}{}); err != nil {
		atomic.StoreUint32(&c.initialized, 0)
		return err
	}
	if err := c.sendInitialized(ctx); err != nil {
		atomic.StoreUint32(&c.initialized, 0)
		return err
	}
	return nil
}

func (c *Client) StartThread(ctx context.Context, cwd string) (string, error) {
	if err := c.ensureInitialized(); err != nil {
		return "", err
	}
	var out threadStartResult
	if err := c.call(ctx, "thread/start", threadStartParams{
		CWD:            filepath.Clean(cwd),
		Sandbox:        "workspace-write",
		ApprovalPolicy: "on-request",
	}, &out); err != nil {
		return "", err
	}
	return out.Thread.ID, nil
}

func (c *Client) ResumeThread(ctx context.Context, threadID string) error {
	if err := c.ensureInitialized(); err != nil {
		return err
	}
	var out threadResumeResult
	if err := c.call(ctx, "thread/resume", threadResumeParams{ThreadID: threadID}, &out); err != nil {
		return err
	}
	if out.Thread.ID != threadID {
		return fmt.Errorf("unexpected thread id: %q", out.Thread.ID)
	}
	return nil
}

func (c *Client) StartTurn(ctx context.Context, threadID, prompt string) (string, error) {
	if err := c.ensureInitialized(); err != nil {
		return "", err
	}
	var out turnStartResult
	if err := c.call(ctx, "turn/start", turnStartParams{
		ThreadID: threadID,
		Input: []turnInput{{
			Type: "text",
			Text: prompt,
		}},
	}, &out); err != nil {
		return "", err
	}
	return out.Turn.ID, nil
}

func (c *Client) InterruptTurn(ctx context.Context, threadID, turnID string) error {
	if err := c.ensureInitialized(); err != nil {
		return err
	}
	var out struct{}
	return c.call(ctx, "turn/interrupt", turnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}, &out)
}

func (c *Client) Exec(ctx context.Context, cwd, command string) (CommandResult, error) {
	if err := c.ensureInitialized(); err != nil {
		return CommandResult{}, err
	}
	var out commandExecResult
	if err := c.call(ctx, "command/exec", commandExecParams{
		CWD:     filepath.Clean(cwd),
		Command: command,
	}, &out); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		ExitCode: out.ExitCode,
		Stdout:   out.Stdout,
		Stderr:   out.Stderr,
	}, nil
}

func (c *Client) Respond(ctx context.Context, id json.RawMessage, result any) error {
	id = normalizeRawID(id)
	if len(id) == 0 || bytes.Equal(id, []byte("null")) {
		return ErrInvalidRequestID
	}
	if !isValidRPCID(id) {
		return ErrInvalidRequestID
	}
	return c.sendPayload(ctx, outgoingResponse{
		ID:     id,
		Result: result,
	})
}

func (c *Client) Events() <-chan Event {
	return c.events
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	reply := make(chan response, 1)
	if err := c.startPending(id, reply); err != nil {
		return err
	}
	defer c.endPending(id)

	if err := c.sendRequest(ctx, id, method, params); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp, ok := <-reply:
		if !ok {
			return ErrDisconnected
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (c *Client) sendInitialized(ctx context.Context) error {
	return c.sendPayload(ctx, struct {
		Method string `json:"method"`
	}{Method: "initialized"})
}

func (c *Client) sendRequest(ctx context.Context, id int64, method string, params any) error {
	msg := request{
		Method: method,
		ID:     id,
		Params: params,
	}
	return c.sendPayload(ctx, msg)
}

func (c *Client) sendPayload(ctx context.Context, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return ErrDisconnected
	}
	return wsjson.Write(ctx, c.conn, payload)
}

func (c *Client) startPending(id int64, ch chan response) error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.isClosed() {
		close(ch)
		return ErrDisconnected
	}
	c.pending[id] = ch
	return nil
}

func (c *Client) endPending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) popPending(id int64) (chan response, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	return ch, ok
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	channels := make([]chan response, 0, len(c.pending))
	for _, ch := range c.pending {
		channels = append(channels, ch)
	}
	c.pending = make(map[int64]chan response)
	c.pendingMu.Unlock()

	for _, ch := range channels {
		resp := response{
			Error: &RPCError{
				Code:    0,
				Message: err.Error(),
			},
		}
		c.trySendResponse(ch, resp)
	}
}

func (c *Client) trySendResponse(ch chan response, resp response) {
	select {
	case ch <- resp:
		close(ch)
	default:
		close(ch)
	}
}

func (c *Client) readLoop() {
	for {
		var raw json.RawMessage
		if err := wsjson.Read(context.Background(), c.conn, &raw); err != nil {
			_ = c.Close()
			return
		}

		var frame incomingRPC
		if err := json.Unmarshal(raw, &frame); err != nil {
			c.failPending(ErrInvalidRPCFrame)
			c.Close()
			return
		}

		id := normalizeRawID(frame.ID)
		hasID := id != nil
		hasMethod := strings.TrimSpace(frame.Method) != ""
		switch {
		case hasID && !hasMethod:
			c.handleResponse(id, frame)
		case hasMethod && !hasID:
			c.handleEvent(frame.Method, nil, frame.Params)
		case hasMethod && hasID:
			c.handleEvent(frame.Method, frame.ID, frame.Params)
		default:
			c.failPending(ErrInvalidRPCFrame)
			c.Close()
			return
		}
	}
}

func (c *Client) handleResponse(rawID []byte, frame incomingRPC) {
	reqID, err := parseNumericID(rawID)
	if err != nil {
		c.failPending(ErrInvalidRPCFrame)
		c.Close()
		return
	}
	respCh, ok := c.popPending(reqID)
	if !ok {
		return
	}
	c.trySendResponse(respCh, response{
		ID:     frame.ID,
		Result: frame.Result,
		Error:  frame.Error,
	})
}

func (c *Client) handleEvent(method string, rawID json.RawMessage, params json.RawMessage) {
	event := Event{
		Method:    method,
		RequestID: rawID,
		Raw:       params,
	}
	var body struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Text     string `json:"text"`
	}
	_ = json.Unmarshal(params, &body)
	event.ThreadID = body.ThreadID
	event.TurnID = body.TurnID
	event.Text = body.Text

	select {
	case c.events <- event:
	default:
		c.failPending(ErrEventBackpressure)
		c.Close()
	}
}

func (c *Client) isClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closed
}

func normalizeRawID(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}

func parseNumericID(raw []byte) (int64, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var n any
	if err := dec.Decode(&n); err != nil {
		return 0, err
	}
	switch value := n.(type) {
	case json.Number:
		return value.Int64()
	case float64:
		return 0, ErrInvalidRPCFrame
	case string:
		return strconv.ParseInt(value, 10, 64)
	default:
		return 0, ErrInvalidRPCFrame
	}
}

func isValidRPCID(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch v.(type) {
	case string:
		return true
	case float64:
		return true
	case int64:
		return true
	case json.Number:
		return true
	default:
		return false
	}
}

func toWebSocketURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	switch u.Scheme {
	case "ws", "wss":
		return raw, nil
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme: %q", u.Scheme)
	}
	return u.String(), nil
}

func (e RPCError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "rpc error"
}

func (c *Client) ensureInitialized() error {
	if atomic.LoadUint32(&c.initialized) == 0 {
		return ErrNotInitialized
	}
	return nil
}
