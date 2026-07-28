package telegram

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Nergous/codex-tg/internal/approval"
	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

const (
	defaultLockTTL        = 2 * time.Minute
	maxRecentSessions     = 10
	callbackTokenLifetime = 10 * time.Minute
)

const commandHelpText = `unknown command.
available commands:
/status, /projects, /project <name>, /new, /resume [thread], /sessions [thread],
/diff [full], /cancel, /lock, /unlock <token>, /queue`

var (
	errNoProject    = errors.New("no project configured")
	errInvalidNonce = errors.New("invalid nonce")
)

type Messenger interface {
	Send(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) (int64, error)
	Edit(ctx context.Context, chatID, messageID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

type ApprovalService interface {
	Request(ctx context.Context, req approval.Request) (string, error)
	ResolveWith(ctx context.Context, chatID int64, nonce string, decision approval.Decision, respond func() error) error
}

type ApprovalResponder interface {
	Respond(ctx context.Context, requestID json.RawMessage, result any) error
}

type LockStore interface {
	SetBotLock(ctx context.Context, chatID int64, nonce string, expiresAt int64) error
	IsBotLocked(ctx context.Context, chatID int64) (bool, error)
	UnlockBot(ctx context.Context, chatID int64, nonce string, now int64) error
}

type Coordinator interface {
	ListProjects(ctx context.Context) ([]models.Project, error)
	OpenProject(ctx context.Context, projectPath string, createNew bool) (models.Session, error)
	ResumeThread(ctx context.Context, threadID string) error
	Submit(ctx context.Context, threadID string, prompt string) error
	Status(ctx context.Context, threadID string) (string, error)
	Cancel(ctx context.Context, threadID string) error
	RecentSessions(ctx context.Context, projectPath string, limit int) ([]models.Session, error)
	Exec(ctx context.Context, threadID, command string) (string, error)
}

type Handler struct {
	coordinator Coordinator
	messenger   Messenger
	renderer    *Renderer

	allowedUserID int64
	allowedChatID int64
	botName       string
	now           func() time.Time

	approvalService   ApprovalService
	approvalResponder ApprovalResponder
	lockStore         LockStore

	mu sync.Mutex

	states        map[int64]*chatState
	threadChats   map[string]int64
	callbacks     map[string]callbackPayload
	unlockSecrets map[string]unlockState
}

type HandlerOptions struct {
	Coordinator       Coordinator
	Messenger         Messenger
	AllowedUserID     int64
	AllowedChatID     int64
	BotName           string
	Now               func() time.Time
	ApprovalService   ApprovalService
	ApprovalResponder ApprovalResponder
	LockStore         LockStore
}

type chatState struct {
	project string
	thread  string
	locked  bool
}

type callbackPayload struct {
	action            string
	project           string
	threadID          string
	expiresAt         time.Time
	createdAt         time.Time
	chatID            int64
	approvalNonce     string
	approvalRequestID json.RawMessage
}

type unlockState struct {
	expires time.Time
	used    bool
}

func NewHandler(opts HandlerOptions) *Handler {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		coordinator:       opts.Coordinator,
		messenger:         opts.Messenger,
		allowedUserID:     opts.AllowedUserID,
		allowedChatID:     opts.AllowedChatID,
		botName:           opts.BotName,
		now:               now,
		approvalService:   opts.ApprovalService,
		approvalResponder: opts.ApprovalResponder,
		lockStore:         opts.LockStore,
		renderer: NewRenderer(RendererOptions{
			Messenger: opts.Messenger,
			Now:       now,
		}),
		states:        map[int64]*chatState{},
		threadChats:   map[string]int64{},
		callbacks:     map[string]callbackPayload{},
		unlockSecrets: map[string]unlockState{},
	}
}

func (h *Handler) Handle(ctx context.Context, update Update) error {
	chatID, ok := h.chatID(update)
	if !ok {
		return nil
	}
	if !h.isAuthorized(update, chatID) {
		return nil
	}

	if update.Message != nil {
		return h.handleMessage(ctx, chatID, update.Message)
	}
	if update.CallbackQuery != nil {
		locked, err := h.isLocked(ctx, chatID)
		if err != nil {
			return h.notify(ctx, chatID, "lock state unavailable")
		}
		if locked && !h.isCallbackAllowed(update.CallbackQuery) {
			return h.replyLocked(ctx, chatID)
		}
		return h.handleCallback(ctx, chatID, update.CallbackQuery)
	}
	return nil
}

func (h *Handler) handleMessage(ctx context.Context, chatID int64, message *Message) error {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return nil
	}

	locked, err := h.isLocked(ctx, chatID)
	if err != nil {
		return h.notify(ctx, chatID, "lock state unavailable")
	}
	if locked {
		command, _, isCommand, forMe := parseCommand(text, h.botName)
		if isCommand {
			if forMe && (command == "status" || command == "unlock") {
				return h.processCommand(ctx, chatID, command, strings.Fields(text)[1:])
			}
			return h.replyLocked(ctx, chatID)
		}
		return h.replyLocked(ctx, chatID)
	}

	command, args, hasCommand, forMe := parseCommand(text, h.botName)
	if hasCommand {
		if !forMe {
			return nil
		}
		return h.processCommand(ctx, chatID, command, args)
	}
	return h.handlePrompt(ctx, chatID, text)
}

func (h *Handler) handlePrompt(ctx context.Context, chatID int64, text string) error {
	threadID, err := h.ensureThread(ctx, chatID)
	if err != nil {
		if errors.Is(err, errNoProject) {
			return h.notify(ctx, chatID, "no projects configured")
		}
		return err
	}
	h.bindRenderer(chatID, threadID)
	return h.coordinator.Submit(ctx, threadID, text)
}

func (h *Handler) processCommand(ctx context.Context, chatID int64, command string, args []string) error {
	switch command {
	case "status":
		threadID, err := h.ensureThread(ctx, chatID)
		if err != nil {
			return h.notify(ctx, chatID, "select project first")
		}
		text, err := h.coordinator.Status(ctx, threadID)
		if err != nil {
			return h.notify(ctx, chatID, "status unavailable")
		}
		return h.notify(ctx, chatID, text)
	case "projects":
		return h.sendProjects(ctx, chatID)
	case "project":
		return h.selectProjectByName(ctx, chatID, args)
	case "new":
		return h.newSession(ctx, chatID, args)
	case "resume", "sessions":
		return h.resumeOrList(ctx, chatID, args)
	case "diff":
		return h.diff(ctx, chatID, args)
	case "cancel":
		threadID, err := h.ensureThread(ctx, chatID)
		if err != nil {
			return h.notify(ctx, chatID, "no active turn")
		}
		if err := h.coordinator.Cancel(ctx, threadID); err != nil {
			return h.notify(ctx, chatID, "cannot cancel turn")
		}
		return h.notify(ctx, chatID, "turn canceled")
	case "queue":
		return h.notify(ctx, chatID, "queue is empty")
	case "lock":
		return h.lock(ctx, chatID)
	case "unlock":
		if len(args) == 0 {
			return h.notify(ctx, chatID, "use /unlock <token>")
		}
		return h.unlock(ctx, chatID, strings.TrimSpace(args[0]))
	case "help":
		return h.notify(ctx, chatID, commandHelpText)
	default:
		return h.notify(ctx, chatID, commandHelpText)
	}
}

func (h *Handler) handleCallback(ctx context.Context, chatID int64, callback *CallbackQuery) error {
	_ = h.answerCallback(ctx, callback.ID, "")
	payload, ok := h.popCallback(callback.Data)
	if !ok {
		return h.notify(ctx, chatID, "stale action")
	}

	switch payload.action {
	case "approval_approve":
		return h.handleApprovalCallback(ctx, payload, approval.ApproveOnce)
	case "approval_deny":
		return h.handleApprovalCallback(ctx, payload, approval.Deny)
	case "approval_cancel":
		return h.handleApprovalCallback(ctx, payload, approval.CancelTask)
	case "select_project":
		session, err := h.coordinator.OpenProject(ctx, payload.project, false)
		if err != nil {
			return h.notify(ctx, chatID, "cannot open project")
		}
		h.withState(chatID, func(state *chatState) {
			state.project = payload.project
			state.thread = session.ThreadID
		})
		h.bindRenderer(chatID, session.ThreadID)
		return h.notify(ctx, chatID, "project selected")
	case "new_confirm":
		return h.newSession(ctx, chatID, []string{"confirm"})
	case "new_cancel":
		return h.notify(ctx, chatID, "new session canceled")
	case "resume":
		if err := h.coordinator.ResumeThread(ctx, payload.threadID); err != nil {
			return h.notify(ctx, chatID, "cannot resume")
		}
		h.bindRenderer(chatID, payload.threadID)
		return h.notify(ctx, chatID, "resumed")
	default:
		return h.notify(ctx, chatID, "unknown callback")
	}
}

func (h *Handler) sendProjects(ctx context.Context, chatID int64) error {
	projects, err := h.coordinator.ListProjects(ctx)
	if err != nil {
		return h.notify(ctx, chatID, "cannot load projects")
	}
	if len(projects) == 0 {
		return h.notify(ctx, chatID, "no projects configured")
	}

	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	rows := make([][]InlineKeyboardButton, 0, len(projects))
	for _, project := range projects {
		token := h.issueCallback()
		if token == "" {
			return errNoProject
		}
		h.withCallback(token, callbackPayload{
			action:    "select_project",
			project:   project.Path,
			expiresAt: h.now().Add(callbackTokenLifetime),
			createdAt: h.now(),
		})
		rows = append(rows, []InlineKeyboardButton{{Text: project.Name, CallbackData: token}})
	}
	return h.send(ctx, chatID, "choose project", &InlineKeyboard{InlineKeyboard: rows})
}

func (h *Handler) selectProjectByName(ctx context.Context, chatID int64, args []string) error {
	if len(args) == 0 {
		return h.notify(ctx, chatID, "usage: /project <name>")
	}

	projects, err := h.coordinator.ListProjects(ctx)
	if err != nil {
		return h.notify(ctx, chatID, "cannot load projects")
	}

	name := args[0]
	for _, project := range projects {
		if project.Name == name {
			session, err := h.coordinator.OpenProject(ctx, project.Path, false)
			if err != nil {
				return h.notify(ctx, chatID, "cannot open project")
			}
			h.withState(chatID, func(state *chatState) {
				state.project = project.Path
				state.thread = session.ThreadID
			})
			h.bindRenderer(chatID, session.ThreadID)
			return h.notify(ctx, chatID, "project selected: "+project.Name)
		}
	}
	return h.notify(ctx, chatID, "unknown project")
}

func (h *Handler) newSession(ctx context.Context, chatID int64, args []string) error {
	if len(args) > 0 && args[0] == "confirm" {
		state := h.state(chatID)
		if state.project == "" {
			if err := h.openDefaultProject(ctx, chatID); err != nil {
				return h.notify(ctx, chatID, err.Error())
			}
			state = h.state(chatID)
		}
		session, err := h.coordinator.OpenProject(ctx, state.project, true)
		if err != nil {
			return h.notify(ctx, chatID, "cannot start new session")
		}
		h.withState(chatID, func(s *chatState) {
			s.thread = session.ThreadID
		})
		h.bindRenderer(chatID, session.ThreadID)
		return h.notify(ctx, chatID, "new session started")
	}

	okToken := h.issueCallback()
	cancelToken := h.issueCallback()
	if okToken == "" || cancelToken == "" {
		return h.notify(ctx, chatID, "cannot prepare confirmation")
	}
	h.withCallback(okToken, callbackPayload{action: "new_confirm", expiresAt: h.now().Add(callbackTokenLifetime), createdAt: h.now()})
	h.withCallback(cancelToken, callbackPayload{action: "new_cancel", expiresAt: h.now().Add(callbackTokenLifetime), createdAt: h.now()})

	return h.send(ctx, chatID, "confirm new session", &InlineKeyboard{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Yes", CallbackData: okToken},
				{Text: "No", CallbackData: cancelToken},
			},
		},
	})
}

func (h *Handler) resumeOrList(ctx context.Context, chatID int64, args []string) error {
	if len(args) > 0 {
		threadID := strings.TrimSpace(args[0])
		if threadID == "" {
			return h.notify(ctx, chatID, "usage: /resume <thread_id>")
		}
		if err := h.coordinator.ResumeThread(ctx, threadID); err != nil {
			return h.notify(ctx, chatID, "cannot resume")
		}
		return h.notify(ctx, chatID, "resumed")
	}

	state := h.state(chatID)
	if state.project == "" {
		if err := h.openDefaultProject(ctx, chatID); err != nil {
			return h.notify(ctx, chatID, "no project selected")
		}
		state = h.state(chatID)
	}
	h.bindRenderer(chatID, state.thread)

	sessions, err := h.coordinator.RecentSessions(ctx, state.project, maxRecentSessions)
	if err != nil {
		return h.notify(ctx, chatID, "cannot list sessions")
	}
	if len(sessions) == 0 {
		return h.notify(ctx, chatID, "no recent sessions")
	}
	if len(sessions) > maxRecentSessions {
		sessions = sessions[:maxRecentSessions]
	}

	rows := make([][]InlineKeyboardButton, 0, len(sessions))
	for _, session := range sessions {
		callback := h.issueCallback()
		if callback == "" {
			return h.notify(ctx, chatID, "cannot list sessions")
		}
		h.withCallback(callback, callbackPayload{
			action:    "resume",
			threadID:  session.ThreadID,
			expiresAt: h.now().Add(callbackTokenLifetime),
			createdAt: h.now(),
		})
		rows = append(rows, []InlineKeyboardButton{{Text: session.ThreadID, CallbackData: callback}})
	}
	return h.send(ctx, chatID, "recent sessions", &InlineKeyboard{InlineKeyboard: rows})
}

func (h *Handler) diff(ctx context.Context, chatID int64, args []string) error {
	threadID, err := h.ensureThread(ctx, chatID)
	if err != nil {
		return h.notify(ctx, chatID, "no active thread")
	}

	requestFull := len(args) > 0 && args[0] == "full"
	h.bindRenderer(chatID, threadID)

	var output string
	if requestFull {
		output, err = h.coordinator.Exec(ctx, threadID, "git diff --")
		if err != nil {
			return h.notify(ctx, chatID, "cannot run diff")
		}
	} else {
		var numstat, status string
		numstat, err = h.coordinator.Exec(ctx, threadID, "git diff --numstat")
		if err == nil {
			status, err = h.coordinator.Exec(ctx, threadID, "git status --short")
		}
		if err != nil {
			return h.notify(ctx, chatID, "cannot run diff")
		}
		output = renderDiffSummary(numstat, status)
	}

	for _, chunk := range splitUTF8(output, defaultFinalChunkBytes) {
		if _, err := h.messenger.Send(ctx, chatID, chunk, nil, MessageOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func renderDiffSummary(numstatOutput, statusOutput string) string {
	numstatOutput = strings.TrimSpace(numstatOutput)
	statusOutput = strings.TrimSpace(statusOutput)

	lines := make([]string, 0)
	for _, line := range strings.Split(numstatOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			lines = append(lines, fmt.Sprintf("%s (+%s -%s)", parts[2], parts[0], parts[1]))
		}
	}

	if statusOutput != "" {
		lines = append(lines, "", "status:")
		for _, line := range strings.Split(statusOutput, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return "no changes"
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) lock(ctx context.Context, chatID int64) error {
	token, err := randomToken()
	if err != nil {
		return h.notify(ctx, chatID, "cannot lock")
	}
	expires := h.now().Add(defaultLockTTL)
	if h.lockStore != nil {
		if err := h.lockStore.SetBotLock(ctx, chatID, token, expires.Unix()); err != nil {
			return h.notify(ctx, chatID, "cannot lock")
		}
	} else {
		h.withUnlock(chatID, token, unlockState{expires: expires, used: false})
	}
	h.withState(chatID, func(state *chatState) { state.locked = true })
	return h.notify(ctx, chatID, fmt.Sprintf("locked. /unlock %s", token))
}

func (h *Handler) unlock(ctx context.Context, chatID int64, token string) error {
	if h.lockStore != nil {
		if err := h.lockStore.UnlockBot(ctx, chatID, token, h.now().Unix()); err != nil {
			return h.notify(ctx, chatID, "invalid or expired token")
		}
		h.withState(chatID, func(state *chatState) { state.locked = false })
		return h.notify(ctx, chatID, "unlocked")
	}
	nonce, err := h.unlockState(token)
	if err != nil {
		return h.notify(ctx, chatID, "invalid or expired token")
	}
	if nonce.used {
		return h.notify(ctx, chatID, "invalid or expired token")
	}
	if nonce.expires.Before(h.now()) {
		return h.notify(ctx, chatID, "invalid or expired token")
	}

	h.withState(chatID, func(state *chatState) {
		state.locked = false
	})
	h.markUnlockUsed(token)
	return h.notify(ctx, chatID, "unlocked")
}

func (h *Handler) isLocked(ctx context.Context, chatID int64) (bool, error) {
	if h.lockStore != nil {
		return h.lockStore.IsBotLocked(ctx, chatID)
	}
	return h.state(chatID).locked, nil
}

func (h *Handler) openDefaultProject(ctx context.Context, chatID int64) error {
	projects, err := h.coordinator.ListProjects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return errNoProject
	}

	session, err := h.coordinator.OpenProject(ctx, projects[0].Path, false)
	if err != nil {
		return err
	}
	h.withState(chatID, func(state *chatState) {
		state.project = projects[0].Path
		state.thread = session.ThreadID
	})
	h.bindRenderer(chatID, session.ThreadID)
	return nil
}

func (h *Handler) ensureThread(ctx context.Context, chatID int64) (string, error) {
	state := h.state(chatID)
	if state.thread != "" {
		return state.thread, nil
	}
	if err := h.openDefaultProject(ctx, chatID); err != nil {
		return "", err
	}
	return h.state(chatID).thread, nil
}

func (h *Handler) OnEvent(ctx context.Context, event codex.Event) error {
	if h.renderer == nil {
		return nil
	}
	if h.isApprovalMethod(event.Method) {
		return h.handleApprovalEvent(ctx, event)
	}
	return h.renderer.OnEvent(ctx, event)
}

func (h *Handler) isApprovalMethod(method string) bool {
	return method == "approval/request" || strings.HasSuffix(method, "/requestApproval")
}

func (h *Handler) handleApprovalEvent(ctx context.Context, event codex.Event) error {
	if h.approvalService == nil || h.approvalResponder == nil {
		return nil
	}
	if len(event.RequestID) == 0 {
		return nil
	}

	chatID := h.threadChat(event.ThreadID)
	if chatID == 0 {
		return nil
	}

	var payload struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	}
	_ = json.Unmarshal(event.Raw, &payload)

	summary := firstNonEmpty(strings.TrimSpace(payload.Summary), strings.TrimSpace(payload.Text), strings.TrimSpace(event.Text))
	if summary == "" {
		summary = "approval requested"
	}

	kind := firstNonEmpty(payload.Kind, strings.TrimPrefix(event.Method, "item/"), "approval")
	normalizedKind := normalizeApprovalKind(kind)
	if !isSupportedApprovalKind(normalizedKind) {
		if err := h.approvalResponder.Respond(ctx, event.RequestID, map[string]any{"decision": "decline"}); err != nil {
			return h.notify(ctx, chatID, "unsupported approval request; automatic denial failed")
		}
		return h.notify(ctx, chatID, "unsupported approval request")
	}
	reqID := append(json.RawMessage(nil), event.RequestID...)
	risky := isHighRiskApproval(normalizedKind, summary, payload.Text)

	nonce, err := h.approvalService.Request(ctx, approval.Request{
		ChatID:   chatID,
		ThreadID: event.ThreadID,
		RPCID:    reqID,
		Kind:     kind,
		Summary:  summary,
	})
	if err != nil {
		return h.notify(ctx, chatID, "cannot create approval request")
	}

	approveToken := h.issueCallback()
	denyToken := h.issueCallback()
	cancelToken := h.issueCallback()
	if denyToken == "" || cancelToken == "" || (!risky && approveToken == "") {
		return h.notify(ctx, chatID, "cannot prepare approval")
	}

	now := h.now()
	expiresAt := now.Add(callbackTokenLifetime)
	if !risky {
		h.withCallback(approveToken, callbackPayload{
			action:            "approval_approve",
			threadID:          event.ThreadID,
			chatID:            chatID,
			approvalNonce:     nonce,
			approvalRequestID: reqID,
			expiresAt:         expiresAt,
			createdAt:         now,
		})
	}
	h.withCallback(denyToken, callbackPayload{
		action:            "approval_deny",
		threadID:          event.ThreadID,
		chatID:            chatID,
		approvalNonce:     nonce,
		approvalRequestID: reqID,
		expiresAt:         expiresAt,
		createdAt:         now,
	})
	h.withCallback(cancelToken, callbackPayload{
		action:            "approval_cancel",
		threadID:          event.ThreadID,
		chatID:            chatID,
		approvalNonce:     nonce,
		approvalRequestID: reqID,
		expiresAt:         expiresAt,
		createdAt:         now,
	})

	controls := [][]InlineKeyboardButton{{{Text: "Deny", CallbackData: denyToken}, {Text: "Cancel", CallbackData: cancelToken}}}
	if !risky {
		controls[0] = append([]InlineKeyboardButton{{Text: "Approve", CallbackData: approveToken}}, controls[0]...)
	}

	return h.send(ctx, chatID, "approval requested: "+summary, &InlineKeyboard{
		InlineKeyboard: controls,
	})
}

func (h *Handler) handleApprovalCallback(ctx context.Context, payload callbackPayload, decision approval.Decision) error {
	if payload.chatID == 0 {
		return nil
	}

	if h.approvalService == nil || h.approvalResponder == nil {
		return h.notify(ctx, payload.chatID, "approval unavailable")
	}
	result := map[string]any{
		"decision": approvalDecisionForResponse(decision),
	}
	if err := h.approvalService.ResolveWith(ctx, payload.chatID, payload.approvalNonce, decision, func() error {
		return h.approvalResponder.Respond(ctx, payload.approvalRequestID, result)
	}); err != nil {
		switch {
		case errors.Is(err, state.ErrAlreadyResolved):
			return h.notify(ctx, payload.chatID, "approval already handled")
		case errors.Is(err, state.ErrUnauthorized):
			return h.notify(ctx, payload.chatID, "approval unauthorized")
		case errors.Is(err, state.ErrExpired):
			return h.notify(ctx, payload.chatID, "approval expired")
		default:
			return h.notify(ctx, payload.chatID, "cannot resolve approval")
		}
	}
	return h.notify(ctx, payload.chatID, "approval responded")
}

func approvalDecisionForResponse(decision approval.Decision) string {
	switch decision {
	case approval.CancelTask:
		return "cancel"
	case approval.Deny:
		return "decline"
	case approval.ApproveOnce:
		return "accept"
	default:
		return "decline"
	}
}

func (h *Handler) threadChat(threadID string) int64 {
	if threadID == "" {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.threadChats[threadID]
}

func (h *Handler) isAuthorized(update Update, chatID int64) bool {
	if update.Message != nil {
		return update.Message.Chat.Type == "private" &&
			update.Message.From.ID == h.allowedUserID &&
			update.Message.Chat.ID == h.allowedChatID &&
			chatID == h.allowedChatID
	}
	if update.CallbackQuery != nil {
		return update.CallbackQuery.Message != nil &&
			update.CallbackQuery.Message.Chat.Type == "private" &&
			update.CallbackQuery.From.ID == h.allowedUserID &&
			update.CallbackQuery.Message.Chat.ID == h.allowedChatID &&
			chatID == h.allowedChatID
	}
	return false
}

func (h *Handler) chatID(update Update) (int64, bool) {
	if update.Message != nil {
		return update.Message.Chat.ID, true
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		return update.CallbackQuery.Message.Chat.ID, true
	}
	return 0, false
}

func (h *Handler) replyLocked(ctx context.Context, chatID int64) error {
	return h.notify(ctx, chatID, "bot is locked. use /unlock")
}

func (h *Handler) notify(ctx context.Context, chatID int64, text string) error {
	_, err := h.messenger.Send(ctx, chatID, text, nil, MessageOptions{})
	return err
}

func (h *Handler) send(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboard) error {
	_, err := h.messenger.Send(ctx, chatID, text, keyboard, MessageOptions{})
	return err
}

func (h *Handler) answerCallback(ctx context.Context, callbackID, text string) error {
	return h.messenger.AnswerCallback(ctx, callbackID, text)
}

func (h *Handler) state(chatID int64) *chatState {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[chatID]
	if state == nil {
		state = &chatState{}
		h.states[chatID] = state
	}
	return state
}

func (h *Handler) withState(chatID int64, fn func(*chatState)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[chatID]
	if state == nil {
		state = &chatState{}
		h.states[chatID] = state
	}
	fn(state)
}

func (h *Handler) withCallback(token string, payload callbackPayload) {
	if token == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callbacks[token] = payload
}

func (h *Handler) popCallback(token string) (callbackPayload, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	payload, ok := h.callbacks[token]
	if !ok {
		return callbackPayload{}, false
	}
	if payload.expiresAt.Before(h.now()) {
		delete(h.callbacks, token)
		return callbackPayload{}, false
	}
	delete(h.callbacks, token)
	return payload, true
}

func (h *Handler) withUnlock(chatID int64, token string, state unlockState) {
	_ = chatID
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unlockSecrets[token] = state
}

func (h *Handler) unlockState(token string) (unlockState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.unlockSecrets[token]
	if !ok {
		return unlockState{}, errInvalidNonce
	}
	if state.expires.Before(h.now()) {
		delete(h.unlockSecrets, token)
		return unlockState{}, errInvalidNonce
	}
	return state, nil
}

func (h *Handler) markUnlockUsed(token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.unlockSecrets[token]
	state.used = true
	h.unlockSecrets[token] = state
}

func (h *Handler) issueCallback() string {
	value := randomTokenMust()
	return value
}

func (h *Handler) bindRenderer(chatID int64, threadID string) {
	if threadID == "" {
		return
	}
	h.setThreadChat(threadID, chatID)
	state := h.state(chatID)
	if h.renderer != nil {
		h.renderer.SetThread(chatID, threadID, state.project)
	}
}

func (h *Handler) setThreadChat(threadID string, chatID int64) {
	if threadID == "" || chatID == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.threadChats[threadID] = chatID
}

func (h *Handler) isCallbackAllowed(callback *CallbackQuery) bool {
	if callback == nil || callback.Message == nil {
		return false
	}
	if callback.Message.Chat.Type != "private" || callback.Data == "" {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	payload, ok := h.callbacks[callback.Data]
	if !ok {
		return false
	}
	if payload.expiresAt.Before(h.now()) {
		delete(h.callbacks, callback.Data)
		return false
	}
	if payload.chatID != 0 && payload.chatID != callback.Message.Chat.ID {
		return false
	}
	return true
}

func normalizeApprovalKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(kind, "item/")))
	kind = strings.ReplaceAll(kind, "-", "")
	kind = strings.ReplaceAll(kind, "_", "")
	return strings.ReplaceAll(kind, "/", "")
}

func isSupportedApprovalKind(kind string) bool {
	switch kind {
	case "approval", "requestapproval", "command", "commandexecution", "filechange", "permission", "mcpelicitation", "approvalrequest":
		return true
	default:
		return false
	}
}

func isHighRiskApproval(kind, summary, requestText string) bool {
	haystack := strings.ToLower(strings.TrimSpace(kind + " " + summary + " " + requestText))
	if haystack == "" {
		return false
	}

	riskyWords := []string{
		"commit", "push", "publish", "deploy", "migration", "migrate",
		"rm -rf", "rm -r ", "rm -f", "rmdir", "del ", "delete ", "mv ", "cp ", "chmod ", "chown ",
		"credential", "token", "password", "access key", "secret", "api key", "private key",
		"sudo", "dd ", "mkfs",
		"git push", "git commit", "git rebase", "git reset", "git clean", "git stash drop", "drop ", "database",
		"../", "..\\",
		"mysql://", "postgres://", "postgresql://", "sqlite://", "redis://", "mongodb://",
		"scp ", "rsync ", "ftp://", "ssh ", "curl ", "wget ", "bash -c", "powershell", "cmd /c", "format ",
		"/etc/", "/var/", "rm -rf /",
	}

	for _, word := range riskyWords {
		if strings.Contains(haystack, word) {
			return true
		}
	}

	return strings.Contains(haystack, "C:\\") ||
		strings.Contains(haystack, "D:\\") ||
		strings.Contains(haystack, "E:\\") ||
		strings.Contains(haystack, "F:\\")
}

func parseCommand(text, botName string) (string, []string, bool, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return "", nil, false, false
	}

	raw := parts[0]
	if !strings.HasPrefix(raw, "/") {
		return "", nil, false, false
	}
	raw = strings.TrimPrefix(raw, "/")
	forMe := true
	if at := strings.Index(raw, "@"); at >= 0 {
		name := raw[at+1:]
		raw = raw[:at]
		if botName == "" || name != botName {
			forMe = false
		}
	}
	if raw == "" {
		return "", nil, false, false
	}
	return strings.ToLower(raw), parts[1:], true, forMe
}

func randomToken() (string, error) {
	raw := make([]byte, 12)
	_, err := rand.Read(raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomTokenMust() string {
	raw, _ := randomToken()
	return raw
}
