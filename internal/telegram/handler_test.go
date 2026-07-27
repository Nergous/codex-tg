package telegram

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/models"
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

	coordinator := &fakeCoordinator{
		projects: []models.Project{
			{Name: "alpha", Path: "/repo/alpha"},
			{Name: "beta", Path: "/repo/beta"},
		},
	}
	messenger := &fakeMessenger{}
	now := time.Now()
	handler := NewHandler(HandlerOptions{
		Coordinator:   coordinator,
		Messenger:     messenger,
		AllowedUserID: allowedUserID,
		AllowedChatID: allowedChatID,
		BotName:       "bot",
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
	if len(messenger.sends) == 0 || !strings.Contains(messenger.sends[0].text, "unknown command") {
		t.Fatalf("unexpected response = %#v", messenger.sends)
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
