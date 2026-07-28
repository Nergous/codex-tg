package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/approval"
	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

type fakeCoordinator struct {
	mu          sync.Mutex
	projects    []models.Project
	recentCalls []recentCall
	execCalls   []execCall

	listCalled       int
	openCalls        []openCall
	submitCalls      []submitCall
	resumeCalls      []string
	statusCalls      []string
	cancelCalls      []string
	execResponses    map[string]func(string) (string, error)
	recentSessionErr error
	queuedMessages   []models.QueuedMessage
}

type execCall struct {
	threadID string
	command  string
}

type openCall struct {
	path      string
	createNew bool
}
type submitCall struct {
	threadID string
	prompt   string
}
type recentCall struct {
	project string
	limit   int
}

type fakeApprovalService struct {
	mu         sync.Mutex
	requests   []fakeApprovalRequest
	resolves   []fakeApprovalResolve
	nextNonce  string
	requestErr error
	resolveErr error
}

type fakeApprovalRequest struct {
	chatID    int64
	threadID  string
	kind      string
	summary   string
	requestID string
}

type fakeApprovalResolve struct {
	chatID   int64
	nonce    string
	decision approval.Decision
}

func (f *fakeApprovalService) Request(_ context.Context, req approval.Request) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requestErr != nil {
		return "", f.requestErr
	}

	nonce := f.nextNonce
	if nonce == "" {
		nonce = "nonce-1"
	}
	f.nextNonce = ""
	f.requests = append(f.requests, fakeApprovalRequest{
		chatID:    req.ChatID,
		threadID:  req.ThreadID,
		kind:      req.Kind,
		summary:   req.Summary,
		requestID: string(req.RPCID),
	})
	return nonce, nil
}

func (f *fakeApprovalService) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeApprovalService) lastRequest() fakeApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return fakeApprovalRequest{}
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeApprovalService) lastResolve() fakeApprovalResolve {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resolves) == 0 {
		return fakeApprovalResolve{}
	}
	return f.resolves[len(f.resolves)-1]
}

func (f *fakeApprovalService) ResolveWith(_ context.Context, chatID int64, nonce string, decision approval.Decision, respond func() error) error {
	f.mu.Lock()
	f.resolves = append(f.resolves, fakeApprovalResolve{
		chatID:   chatID,
		nonce:    nonce,
		decision: decision,
	})
	err := f.resolveErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return respond()
}

type fakeApprovalResponder struct {
	mu      sync.Mutex
	calls   []approvalResponse
	respErr error
}

type approvalResponse struct {
	requestID string
	result    any
}

func (f *fakeApprovalResponder) Respond(_ context.Context, requestID json.RawMessage, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, approvalResponse{
		requestID: string(requestID),
		result:    result,
	})
	return f.respErr
}

func (f *fakeApprovalResponder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeApprovalResponder) lastCall() approvalResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return approvalResponse{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeCoordinator) ListProjects(context.Context) ([]models.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalled++
	return append([]models.Project(nil), f.projects...), nil
}

func (f *fakeCoordinator) OpenProject(_ context.Context, projectPath string, createNew bool) (models.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, openCall{path: projectPath, createNew: createNew})
	if projectPath == "error" {
		return models.Session{}, errors.New("open failed")
	}
	return models.Session{ProjectPath: projectPath, ThreadID: "thr-" + strings.ReplaceAll(projectPath, ":", "-")}, nil
}

func (f *fakeCoordinator) ResumeThread(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls = append(f.resumeCalls, threadID)
	if threadID == "error" {
		return errors.New("cannot resume")
	}
	return nil
}

func (f *fakeCoordinator) Submit(_ context.Context, threadID string, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitCalls = append(f.submitCalls, submitCall{threadID: threadID, prompt: prompt})
	return nil
}

func (f *fakeCoordinator) Status(_ context.Context, threadID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, threadID)
	if threadID == "error" {
		return "", errors.New("status failed")
	}
	return "status for " + threadID, nil
}

func (f *fakeCoordinator) Cancel(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls = append(f.cancelCalls, threadID)
	if threadID == "error" {
		return errors.New("cancel failed")
	}
	return nil
}

func (f *fakeCoordinator) RecentSessions(_ context.Context, projectPath string, limit int) ([]models.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recentSessionErr != nil {
		return nil, f.recentSessionErr
	}
	f.recentCalls = append(f.recentCalls, recentCall{project: projectPath, limit: limit})
	sessions := make([]models.Session, 0, limit)
	for i := 0; i < limit+5; i++ {
		sessions = append(sessions, models.Session{ProjectPath: projectPath, ThreadID: "thr-" + string(rune('a'+i))})
	}
	return sessions, nil
}

func (f *fakeCoordinator) QueuedMessages(context.Context, string) ([]models.QueuedMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]models.QueuedMessage(nil), f.queuedMessages...), nil
}

func (f *fakeCoordinator) Exec(_ context.Context, threadID, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCalls = append(f.execCalls, execCall{threadID: threadID, command: command})

	if f.execResponses != nil {
		if fn, ok := f.execResponses[command]; ok {
			return fn(threadID)
		}
	}
	return "", nil
}

func (f *fakeCoordinator) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalled = 0
	f.openCalls = nil
	f.submitCalls = nil
	f.resumeCalls = nil
	f.statusCalls = nil
	f.cancelCalls = nil
	f.recentCalls = nil
	f.execCalls = nil
}

func TestQueueCommandListsQueuedPrompts(t *testing.T) {
	handler, coordinator, messenger, _ := newFixture(t, 100, 200)
	coordinator.queuedMessages = []models.QueuedMessage{{Text: "first prompt"}, {Text: "second prompt"}}

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/queue")); err != nil {
		t.Fatal(err)
	}
	got := messenger.lastSendText()
	if !strings.Contains(got, "1. first prompt") || !strings.Contains(got, "2. second prompt") {
		t.Fatalf("queue response = %q", got)
	}
}

type fakeMessenger struct {
	mu      sync.Mutex
	sends   []sendCall
	answers int
}

type sendCall struct {
	chatID   int64
	text     string
	keyboard *InlineKeyboard
	opts     MessageOptions
}

func (f *fakeMessenger) Send(_ context.Context, chatID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, sendCall{chatID: chatID, text: text, keyboard: keyboard, opts: opts})
	return int64(len(f.sends)), nil
}

func (f *fakeMessenger) Edit(context.Context, int64, int64, string, *InlineKeyboard, MessageOptions) error {
	return nil
}

func (f *fakeMessenger) AnswerCallback(_ context.Context, _ string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers++
	return nil
}

func (f *fakeMessenger) lastSend() sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends[len(f.sends)-1]
}

func (f *fakeMessenger) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *fakeMessenger) lastSendText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends[len(f.sends)-1].text
}

func newFixture(t *testing.T, allowedUserID, allowedChatID int64) (*Handler, *fakeCoordinator, *fakeMessenger, *time.Time) {
	t.Helper()
	return newFixtureWithApprovals(t, allowedUserID, allowedChatID, nil, nil)
}

func newFixtureWithApprovals(
	t *testing.T,
	allowedUserID, allowedChatID int64,
	approvalService ApprovalService,
	approvalResponder ApprovalResponder,
) (*Handler, *fakeCoordinator, *fakeMessenger, *time.Time) {
	t.Helper()

	coordinator := &fakeCoordinator{
		projects: []models.Project{
			{Name: "alpha", Path: "/repo/alpha"},
			{Name: "beta", Path: "/repo/beta"},
		},
	}
	messenger := &fakeMessenger{}
	now := time.Now()
	handler := NewHandler(HandlerOptions{
		Coordinator:       coordinator,
		Messenger:         messenger,
		AllowedUserID:     allowedUserID,
		AllowedChatID:     allowedChatID,
		BotName:           "bot",
		ApprovalService:   approvalService,
		ApprovalResponder: approvalResponder,
		Now: func() time.Time {
			return now
		},
	})
	return handler, coordinator, messenger, &now
}

func messageUpdate(fromID, chatID int64, chatType, text string) Update {
	return Update{
		UpdateID: 1,
		Message: &Message{
			MessageID: 1,
			Chat:      Chat{ID: chatID, Type: chatType},
			From:      User{ID: fromID},
			Text:      text,
		},
	}
}

func callbackUpdate(chatID, fromID int64, data string) Update {
	return Update{
		UpdateID: 1,
		CallbackQuery: &CallbackQuery{
			ID:   "cb-1",
			From: User{ID: fromID},
			Data: data,
			Message: &Message{
				Chat: Chat{ID: chatID, Type: "private"},
			},
		},
	}
}

func approvalRequestEvent(threadID, turnID string, requestID json.RawMessage, summary string) codex.Event {
	return codex.Event{
		Method:    "approval/request",
		ThreadID:  threadID,
		TurnID:    turnID,
		RequestID: requestID,
		Raw: mustMarshal(map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"kind":     "command",
			"summary":  summary,
		}),
	}
}

func approvalRequestEventWithKind(threadID, turnID string, requestID json.RawMessage, kind, summary, text string) codex.Event {
	payload := map[string]any{
		"kind":    kind,
		"summary": summary,
	}
	if text != "" {
		payload["text"] = text
	}
	return codex.Event{
		Method:    "approval/request",
		ThreadID:  threadID,
		TurnID:    turnID,
		RequestID: requestID,
		Raw:       mustMarshal(payload),
	}
}

func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestHandlerSilentlyIgnoresUnauthorizedAndGroupUpdates(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)

	cases := []Update{
		messageUpdate(101, 200, "private", "change files"),
		messageUpdate(100, 201, "private", "change files"),
		messageUpdate(100, 200, "group", "change files"),
		callbackUpdate(200, 101, "unused"),
	}

	for _, update := range cases {
		if err := handler.Handle(context.Background(), update); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
	}

	coord.mu.Lock()
	got := len(coord.submitCalls) + len(coord.openCalls) + len(coord.resumeCalls) + len(coord.statusCalls) + len(coord.cancelCalls)
	coord.mu.Unlock()
	if got != 0 {
		t.Fatalf("unexpected coordinator calls = %d", got)
	}

	messenger.mu.Lock()
	sent := len(messenger.sends)
	messenger.mu.Unlock()
	if sent != 0 {
		t.Fatalf("unexpected messenger sends = %d", sent)
	}
}

func TestHandlePromptUsesDefaultProject(t *testing.T) {
	t.Parallel()
	handler, coord, _, _ := newFixture(t, 100, 200)

	update := messageUpdate(100, 200, "private", "change files")
	if err := handler.Handle(context.Background(), update); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.openCalls) != 1 {
		t.Fatalf("open calls = %d, want 1", len(coord.openCalls))
	}
	if coord.openCalls[0].path != "/repo/alpha" {
		t.Fatalf("open path = %q, want /repo/alpha", coord.openCalls[0].path)
	}
	if len(coord.submitCalls) != 1 {
		t.Fatalf("submit calls = %d, want 1", len(coord.submitCalls))
	}
}

func TestParseCommandAllowsOnlyPrefixAndBotname(t *testing.T) {
	t.Parallel()
	handler, coord, _, _ := newFixture(t, 100, 200)

	updates := []Update{
		messageUpdate(100, 200, "private", "/status@bot"),
		messageUpdate(100, 200, "private", "/status@other"),
	}
	for i, update := range updates {
		_ = i
		if err := handler.Handle(context.Background(), update); err != nil {
			t.Fatalf("Handle(%d) error = %v", i, err)
		}
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.statusCalls) != 1 {
		t.Fatalf("status calls = %d, want 1", len(coord.statusCalls))
	}
}

func TestUnknownCommandSendsHelp(t *testing.T) {
	t.Parallel()
	handler, _, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/foo")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	if len(messenger.sends) == 0 || !strings.Contains(messenger.sends[0].text, "available commands") {
		t.Fatalf("unexpected response = %#v", messenger.sends)
	}
}

func TestHelpCommandSendsCommandsList(t *testing.T) {
	t.Parallel()
	handler, _, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/help")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	if len(messenger.sends) == 0 {
		t.Fatalf("no response for /help")
	}
	if !strings.Contains(messenger.sends[0].text, "/status") || !strings.Contains(messenger.sends[0].text, "/unlock <token>") {
		t.Fatalf("help payload = %#v", messenger.sends[0].text)
	}
}

func TestHandleIgnoresCommandInMiddleOfText(t *testing.T) {
	t.Parallel()
	handler, coord, _, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "hi /status there")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	opened := len(coord.openCalls)
	submits := len(coord.submitCalls)
	status := len(coord.statusCalls)
	coord.mu.Unlock()

	if status != 0 {
		t.Fatalf("status calls = %d, want 0", status)
	}
	if opened != 1 {
		t.Fatalf("open calls = %d, want 1", opened)
	}
	if submits != 1 {
		t.Fatalf("submit calls = %d, want 1", submits)
	}
}

func TestProjectKeyboardUsesOpaqueCallbacks(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/projects")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	send := messenger.lastSend()
	if send.keyboard == nil || len(send.keyboard.InlineKeyboard) != 2 {
		t.Fatalf("keyboard = %#v", send.keyboard)
	}
	token := send.keyboard.InlineKeyboard[0][0].CallbackData

	if strings.Contains(token, "alpha") || strings.Contains(token, "beta") || strings.Contains(token, "/repo/alpha") {
		t.Fatalf("callback token leaks project name/path: %q", token)
	}
	r := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !r.MatchString(token) {
		t.Fatalf("callback token malformed = %q", token)
	}

	if err := handler.Handle(context.Background(), callbackUpdate(200, 100, token)); err != nil {
		t.Fatalf("callback handle error = %v", err)
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.openCalls) != 1 {
		t.Fatalf("open calls = %d, want 1", len(coord.openCalls))
	}
	if coord.openCalls[0].path != "/repo/alpha" {
		t.Fatalf("callback opened %q, want /repo/alpha", coord.openCalls[0].path)
	}
}

func TestNewRequiresConfirmation(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/new")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	send := messenger.lastSend()
	if send.keyboard == nil || len(send.keyboard.InlineKeyboard) != 1 || len(send.keyboard.InlineKeyboard[0]) != 2 {
		t.Fatalf("confirmation keyboard missing: %#v", send.keyboard)
	}
	yes := send.keyboard.InlineKeyboard[0][0].CallbackData

	if err := handler.Handle(context.Background(), callbackUpdate(200, 100, yes)); err != nil {
		t.Fatalf("callback handle error = %v", err)
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.openCalls) != 2 {
		t.Fatalf("open calls = %d, want 2, got %#v", len(coord.openCalls), coord.openCalls)
	}
	last := coord.openCalls[len(coord.openCalls)-1]
	if !last.createNew {
		t.Fatalf("last open call createNew=false, want true")
	}
}

func TestLockPreventsPromptAndRequiresValidUnlock(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, now := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/lock")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	messenger.mu.Lock()
	lockText := messenger.sends[len(messenger.sends)-1].text
	messenger.mu.Unlock()
	matches := regexp.MustCompile(`/unlock ([A-Za-z0-9_-]+)`).FindStringSubmatch(lockText)
	if len(matches) != 2 {
		t.Fatalf("lock response lacks token: %q", lockText)
	}
	token := matches[1]

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "change files")); err != nil {
		t.Fatalf("locked prompt error = %v", err)
	}

	coord.mu.Lock()
	before := len(coord.submitCalls)
	coord.mu.Unlock()
	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/unlock wrong")); err != nil {
		t.Fatalf("wrong unlock error = %v", err)
	}
	coord.mu.Lock()
	if len(coord.submitCalls) != before {
		coord.mu.Unlock()
		t.Fatalf("submit changed despite wrong token")
	}
	coord.mu.Unlock()

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/unlock "+token)); err != nil {
		t.Fatalf("unlock error = %v", err)
	}

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "change files")); err != nil {
		t.Fatalf("prompt after unlock error = %v", err)
	}

	coord.mu.Lock()
	after := len(coord.submitCalls)
	coord.mu.Unlock()
	if after == before {
		t.Fatalf("submit did not happen after unlock")
	}

	*now = now.Add(3 * time.Minute)
	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/lock")); err != nil {
		t.Fatalf("lock error = %v", err)
	}
	messenger.mu.Lock()
	lockText = messenger.sends[len(messenger.sends)-1].text
	messenger.mu.Unlock()
	token = regexp.MustCompile(`/unlock ([A-Za-z0-9_-]+)`).FindStringSubmatch(lockText)[1]
	*now = now.Add(defaultLockTTL + time.Minute)
	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/unlock "+token)); err != nil {
		t.Fatalf("expired unlock should not error: %v", err)
	}
}

func TestResumeListsAtMostTenSessions(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/resume")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	send := messenger.lastSend()

	if send.keyboard == nil {
		t.Fatalf("expected keyboard for sessions")
	}
	if got := len(send.keyboard.InlineKeyboard); got > 10 {
		t.Fatalf("session rows = %d, want <= 10", got)
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.recentCalls) == 0 {
		t.Fatalf("RecentSessions not called")
	}
	if got := coord.recentCalls[0].limit; got != 10 {
		t.Fatalf("RecentSessions limit = %d, want 10", got)
	}
}

func TestResumeAllowsPersistedSessionAfterHandlerRestart(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/resume thr-persisted")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	resumeCalls := append([]string(nil), coord.resumeCalls...)
	coord.mu.Unlock()
	if len(resumeCalls) != 1 || resumeCalls[0] != "thr-persisted" {
		t.Fatalf("ResumeThread() calls = %v, want [thr-persisted]", resumeCalls)
	}
	if got := messenger.lastSendText(); got != "resumed" {
		t.Fatalf("response = %q, want resumed", got)
	}
	if got := handler.threadChat("thr-persisted"); got != 200 {
		t.Fatalf("renderer chat = %d, want 200", got)
	}

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "continue")); err != nil {
		t.Fatalf("prompt after resume error = %v", err)
	}
	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.submitCalls) != 1 || coord.submitCalls[0].threadID != "thr-persisted" {
		t.Fatalf("prompt thread = %#v, want thr-persisted", coord.submitCalls)
	}
}

func TestHandlerDiffDefaultUsesSummary(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)
	coord.execResponses = map[string]func(string) (string, error){
		"git diff --numstat": func(string) (string, error) {
			return "1\t2\tREADME.md\n3\t4\tcmd/main.go", nil
		},
		"git status --short": func(string) (string, error) {
			return "M README.md\n?? new.txt", nil
		},
	}

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/diff")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	if len(coord.execCalls) != 2 {
		t.Fatalf("exec calls = %d, want 2", len(coord.execCalls))
	}
	if coord.execCalls[0].command != "git diff --numstat" {
		t.Fatalf("first command = %q, want git diff --numstat", coord.execCalls[0].command)
	}
	if coord.execCalls[1].command != "git status --short" {
		t.Fatalf("second command = %q, want git status --short", coord.execCalls[1].command)
	}

	if messenger.sendCount() == 0 {
		t.Fatal("expected diff response")
	}
	msg := messenger.lastSend().text
	if !strings.Contains(msg, "README.md (+1 -2)") {
		t.Fatalf("missing numstat summary: %q", msg)
	}
	if !strings.Contains(msg, "cmd/main.go (+3 -4)") {
		t.Fatalf("missing numstat summary: %q", msg)
	}
	if !strings.Contains(msg, "status:") {
		t.Fatalf("missing status section: %q", msg)
	}
	coord.mu.Unlock()
}

func TestHandlerDiffFullPatchExecutesFullDiff(t *testing.T) {
	t.Parallel()
	handler, coord, messenger, _ := newFixture(t, 100, 200)
	coord.execResponses = map[string]func(string) (string, error){
		"git diff --": func(string) (string, error) {
			return "diff --patch", nil
		},
	}

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/diff full")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	if len(coord.execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(coord.execCalls))
	}
	if coord.execCalls[0].command != "git diff --" {
		t.Fatalf("command = %q, want git diff --", coord.execCalls[0].command)
	}

	if messenger.sendCount() == 0 {
		t.Fatal("expected diff response")
	}
	if got := messenger.lastSend().text; got != "diff --patch" {
		t.Fatalf("message = %q, want %q", got, "diff --patch")
	}
	coord.mu.Unlock()
	msg := messenger.lastSend()
	if messenger.sendCount() == 0 {
		t.Fatal("expected diff response")
	}
	if got := msg.text; got != "diff --patch" {
		t.Fatalf("message = %q, want %q", got, "diff --patch")
	}
}

func TestOnEventApprovalRequestSendsInlineChoices(t *testing.T) {
	t.Parallel()
	service := &fakeApprovalService{nextNonce: "nonce-approve"}
	responder := &fakeApprovalResponder{}
	handler, _, messenger, _ := newFixtureWithApprovals(t, 100, 200, service, responder)

	handler.bindRenderer(200, "thr-1")

	if err := handler.OnEvent(context.Background(), approvalRequestEvent("thr-1", "turn-1", json.RawMessage(`"srv-1"`), "show project status")); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	if got := service.requestCount(); got != 1 {
		t.Fatalf("approval service requests = %d, want 1", got)
	}
	lastRequest := service.lastRequest()
	if lastRequest.chatID != 200 {
		t.Fatalf("request chat = %d, want 200", lastRequest.chatID)
	}
	if lastRequest.requestID != "\"srv-1\"" {
		t.Fatalf("requestID = %q, want %q", lastRequest.requestID, "\"srv-1\"")
	}

	send := messenger.lastSend()
	if send.keyboard == nil {
		t.Fatal("expected approval keyboard")
	}
	if len(send.keyboard.InlineKeyboard) != 1 || len(send.keyboard.InlineKeyboard[0]) != 3 {
		t.Fatalf("approval keyboard = %#v", send.keyboard.InlineKeyboard)
	}
	if send.keyboard.InlineKeyboard[0][0].CallbackData == "" || send.keyboard.InlineKeyboard[0][1].CallbackData == "" || send.keyboard.InlineKeyboard[0][2].CallbackData == "" {
		t.Fatal("expected callback data for every approval action")
	}
}

func TestOnEventHighRiskApprovalOffersNoApprove(t *testing.T) {
	t.Parallel()
	service := &fakeApprovalService{nextNonce: "nonce-high-risk"}
	responder := &fakeApprovalResponder{}
	handler, _, messenger, _ := newFixtureWithApprovals(t, 100, 200, service, responder)

	handler.bindRenderer(200, "thr-1")

	if err := handler.OnEvent(context.Background(), approvalRequestEventWithKind("thr-1", "turn-1", json.RawMessage(`"srv-hr"`), "command", "git push origin main", "")); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	if got := service.requestCount(); got != 1 {
		t.Fatalf("approval service requests = %d, want 1", got)
	}

	send := messenger.lastSend()
	if send.keyboard == nil {
		t.Fatal("expected approval keyboard")
	}
	if len(send.keyboard.InlineKeyboard) != 1 || len(send.keyboard.InlineKeyboard[0]) != 2 {
		t.Fatalf("approval keyboard = %#v", send.keyboard.InlineKeyboard)
	}
	if send.keyboard.InlineKeyboard[0][0].Text != "Deny" || send.keyboard.InlineKeyboard[0][1].Text != "Cancel" {
		t.Fatalf("unexpected high risk approval buttons: %#v", send.keyboard.InlineKeyboard[0])
	}
}

func TestOnEventWindowsPathApprovalOffersNoApprove(t *testing.T) {
	t.Parallel()
	service := &fakeApprovalService{nextNonce: "nonce-windows-path"}
	responder := &fakeApprovalResponder{}
	handler, _, messenger, _ := newFixtureWithApprovals(t, 100, 200, service, responder)

	handler.bindRenderer(200, "thr-1")

	if err := handler.OnEvent(context.Background(), approvalRequestEventWithKind("thr-1", "turn-1", json.RawMessage(`"srv-path"`), "command", `C:\Windows\System32\whoami.exe`, "")); err != nil {
		t.Fatal(err)
	}

	send := messenger.lastSend()
	if send.keyboard == nil || len(send.keyboard.InlineKeyboard) != 1 || len(send.keyboard.InlineKeyboard[0]) != 2 {
		t.Fatalf("approval keyboard = %#v", send.keyboard)
	}
	if send.keyboard.InlineKeyboard[0][0].Text != "Deny" || send.keyboard.InlineKeyboard[0][1].Text != "Cancel" {
		t.Fatalf("unexpected Windows path approval buttons: %#v", send.keyboard.InlineKeyboard[0])
	}
}

func TestOnEventRejectsUnsupportedApprovalKind(t *testing.T) {
	t.Parallel()
	service := &fakeApprovalService{nextNonce: "nonce-unsupported"}
	responder := &fakeApprovalResponder{}
	handler, _, messenger, _ := newFixtureWithApprovals(t, 100, 200, service, responder)

	handler.bindRenderer(200, "thr-1")

	if err := handler.OnEvent(context.Background(), approvalRequestEventWithKind("thr-1", "turn-1", json.RawMessage(`"srv-unsupported"`), "mystery", "do something", "")); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	if got := service.requestCount(); got != 0 {
		t.Fatalf("approval service requests = %d, want 0", got)
	}
	if responder.callCount() != 1 {
		t.Fatalf("responder calls = %d, want 1", responder.callCount())
	}
	response, ok := responder.lastCall().result.(map[string]any)
	if !ok || response["decision"] != "decline" {
		t.Fatalf("response=%#v want decline", responder.lastCall().result)
	}

	send := messenger.lastSend()
	if send.text == "" || !strings.Contains(send.text, "unsupported approval request") {
		t.Fatalf("unexpected response = %#v", send.text)
	}
}

func TestApprovalCallbackResolvesAndResponds(t *testing.T) {
	t.Parallel()
	service := &fakeApprovalService{nextNonce: "nonce-deny"}
	responder := &fakeApprovalResponder{}
	handler, _, messenger, _ := newFixtureWithApprovals(t, 100, 200, service, responder)
	handler.bindRenderer(200, "thr-1")

	if err := handler.OnEvent(context.Background(), approvalRequestEvent("thr-1", "turn-1", json.RawMessage(`"srv-2"`), "cat /etc/passwd")); err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	send := messenger.lastSend()
	if len(send.keyboard.InlineKeyboard) == 0 || len(send.keyboard.InlineKeyboard[0]) < 2 {
		t.Fatal("missing deny callback")
	}
	denyToken := send.keyboard.InlineKeyboard[0][0].CallbackData
	if err := handler.Handle(context.Background(), callbackUpdate(200, 100, denyToken)); err != nil {
		t.Fatalf("callback error = %v", err)
	}

	resolved := service.lastResolve()
	if resolved.chatID != 200 {
		t.Fatalf("resolved chat = %d, want 200", resolved.chatID)
	}
	if resolved.nonce != "nonce-deny" {
		t.Fatalf("resolved nonce = %q, want %q", resolved.nonce, "nonce-deny")
	}
	if resolved.decision != approval.Deny {
		t.Fatalf("resolved decision = %q, want %q", resolved.decision, approval.Deny)
	}
	if responder.callCount() != 1 {
		t.Fatalf("responder calls = %d, want 1", responder.callCount())
	}
	lastCall := responder.lastCall()
	if lastCall.requestID != "\"srv-2\"" {
		t.Fatalf("responder requestID = %q, want %q", lastCall.requestID, "\"srv-2\"")
	}
	gotResult, ok := lastCall.result.(map[string]any)
	if !ok {
		t.Fatalf("responder result type = %T, want map", lastCall.result)
	}
	if gotResult["decision"] != "decline" {
		t.Fatalf("decision = %v, want decline", gotResult["decision"])
	}
}

func TestCallbackRejectedWhenTokenBoundToDifferentChat(t *testing.T) {
	t.Parallel()
	handler, coord, _, _ := newFixture(t, 100, 200)

	if err := handler.Handle(context.Background(), messageUpdate(100, 200, "private", "/lock")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	marker := handler.issueCallback()
	handler.withCallback(marker, callbackPayload{
		action:    "new_confirm",
		expiresAt: time.Now().Add(10 * time.Minute),
		createdAt: time.Now(),
		chatID:    999,
	})

	if err := handler.Handle(context.Background(), callbackUpdate(200, 100, marker)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	coord.mu.Lock()
	defer coord.mu.Unlock()
	if len(coord.openCalls) != 0 || len(coord.submitCalls) != 0 {
		t.Fatalf("unexpected coordinator calls for mismatched callback: open=%d submit=%d", len(coord.openCalls), len(coord.submitCalls))
	}
}

func TestLockPersistsAcrossHandlerRestart(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coord := &fakeCoordinator{projects: []models.Project{{Name: "demo", Path: `C:\repo`}}}
	messenger := &fakeMessenger{}
	first := NewHandler(HandlerOptions{
		Coordinator:   coord,
		Messenger:     messenger,
		AllowedUserID: 100,
		AllowedChatID: 200,
		LockStore:     store,
	})
	if err := first.Handle(ctx, messageUpdate(100, 200, "private", "/lock")); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(messenger.lastSend().text)
	if len(fields) != 3 {
		t.Fatalf("lock message=%q", messenger.lastSend().text)
	}

	second := NewHandler(HandlerOptions{
		Coordinator:   coord,
		Messenger:     messenger,
		AllowedUserID: 100,
		AllowedChatID: 200,
		LockStore:     store,
	})
	if err := second.Handle(ctx, messageUpdate(100, 200, "private", "change files")); err != nil {
		t.Fatal(err)
	}
	if len(coord.submitCalls) != 0 {
		t.Fatalf("locked handler submitted prompt: %#v", coord.submitCalls)
	}
	if err := second.Handle(ctx, messageUpdate(100, 200, "private", "/unlock "+fields[2])); err != nil {
		t.Fatal(err)
	}
	if err := second.Handle(ctx, messageUpdate(100, 200, "private", "change files")); err != nil {
		t.Fatal(err)
	}
	if len(coord.submitCalls) != 1 {
		t.Fatalf("unlocked handler submit calls=%#v", coord.submitCalls)
	}
}
