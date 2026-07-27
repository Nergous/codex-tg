package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

type TestingT interface {
	Helper()
	Cleanup(func())
	Fatalf(format string, args ...any)
	FailNow()
}

type queuedResponse struct {
	status int
	body   string
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      User   `json:"from"`
	Text      string `json:"text"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

type FakeTelegram struct {
	t     TestingT
	srv   *httptest.Server
	token string

	mu        sync.Mutex
	updates   []Update
	responses map[string][]queuedResponse
	calls     map[string]int

	lastOffset  int64
	lastLimit   int
	lastTimeout int
}

type telegramEnvelope struct {
	OK          bool           `json:"ok"`
	Result      any            `json:"result,omitempty"`
	Description string         `json:"description,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Parameters  map[string]int `json:"parameters,omitempty"`
}

func NewFakeTelegram(t TestingT) *FakeTelegram {
	t.Helper()

	fake := &FakeTelegram{
		t:         t,
		token:     "test-telegram-token",
		updates:   []Update{},
		responses: map[string][]queuedResponse{},
		calls:     map[string]int{},
	}
	fake.srv = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (f *FakeTelegram) URL() string {
	return f.srv.URL
}

func (f *FakeTelegram) HTTPClient() *http.Client {
	return f.srv.Client()
}

func (f *FakeTelegram) Token() string {
	return f.token
}

func (f *FakeTelegram) Close() {
	f.srv.Close()
}

func (f *FakeTelegram) EnqueueUpdate(update Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, update)
}

func (f *FakeTelegram) EnqueueResponse(route string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route = normalizeRoute(route)
	f.responses[route] = append(f.responses[route], queuedResponse{status: status, body: body})
}

func (f *FakeTelegram) LastOffset() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastOffset
}

func (f *FakeTelegram) LastGetUpdatesTimeout() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTimeout
}

func (f *FakeTelegram) LastGetUpdatesLimit() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastLimit
}

func (f *FakeTelegram) Calls(route string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[normalizeRoute(route)]
}

func (f *FakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	route := normalizeRoute(r.URL.Path)
	f.bumpCalls(route)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gotAuth := r.Header.Get("Authorization")
	wantAuth := "Bearer " + f.token
	if gotAuth != wantAuth {
		f.writeResponse(w, http.StatusUnauthorized, telegramEnvelope{
			OK:          false,
			ErrorCode:   http.StatusUnauthorized,
			Description: "unauthorized",
		})
		return
	}

	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.writeResponse(w, http.StatusBadRequest, telegramEnvelope{
			OK:          false,
			ErrorCode:   http.StatusBadRequest,
			Description: "invalid payload",
		})
		return
	}

	if response := f.popResponse(route); response.status != 0 {
		f.writeRawResponse(w, response.status, response.body)
		return
	}

	switch route {
	case "getUpdates":
		f.handleGetUpdates(w, payload)
	case "sendMessage":
		f.writeResponse(w, 0, telegramEnvelope{
			OK:     true,
			Result: map[string]any{"message_id": int64(42)},
		})
	case "editMessageText":
		f.writeResponse(w, 0, telegramEnvelope{
			OK:     true,
			Result: map[string]any{},
		})
	case "answerCallbackQuery":
		f.writeResponse(w, 0, telegramEnvelope{
			OK:     true,
			Result: map[string]any{},
		})
	case "deleteWebhook":
		f.writeResponse(w, 0, telegramEnvelope{
			OK:     true,
			Result: map[string]any{},
		})
	default:
		http.NotFound(w, r)
	}
}

func (f *FakeTelegram) handleGetUpdates(w http.ResponseWriter, payload map[string]any) {
	var offset int64
	if value, ok := payload["offset"].(float64); ok {
		offset = int64(value)
	}
	var limit int
	if value, ok := payload["limit"].(float64); ok {
		limit = int(value)
	}
	timeout := 0
	if value, ok := payload["timeout"].(float64); ok {
		timeout = int(value)
	}
	f.setGetUpdatesMetadata(offset, limit, timeout)

	f.mu.Lock()
	updates := make([]Update, 0, len(f.updates))
	for _, update := range f.updates {
		if update.UpdateID >= offset {
			updates = append(updates, update)
		}
	}
	f.updates = nil
	f.mu.Unlock()

	f.writeResponse(w, 0, telegramEnvelope{
		OK:     true,
		Result: updates,
	})
}

func (f *FakeTelegram) setGetUpdatesMetadata(offset int64, limit int, timeout int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastOffset = offset
	f.lastLimit = limit
	f.lastTimeout = timeout
}

func (f *FakeTelegram) bumpCalls(route string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[route]++
}

func (f *FakeTelegram) popResponse(route string) queuedResponse {
	f.mu.Lock()
	defer f.mu.Unlock()

	route = normalizeRoute(route)
	queue := f.responses[route]
	if len(queue) == 0 {
		return queuedResponse{}
	}
	response := queue[0]
	f.responses[route] = queue[1:]
	return response
}

func (f *FakeTelegram) writeResponse(w http.ResponseWriter, status int, env telegramEnvelope) {
	if status == 0 {
		status = http.StatusOK
	}
	f.writeRawResponse(w, status, mustMarshal(env))
}

func (f *FakeTelegram) writeRawResponse(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	if _, err := w.Write([]byte(body)); err != nil {
		f.t.Fatalf("fake telegram write response: %v", err)
	}
}

func normalizeRoute(route string) string {
	return strings.Trim(route, "/")
}

func mustMarshal(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("fake telegram marshal: %v", err))
	}
	return string(raw)
}
