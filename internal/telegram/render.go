package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Nergous/codex-tg/internal/codex"
)

const (
	defaultProgressThrottle  = 2 * time.Second
	defaultHeartbeatInterval = 60 * time.Second
	defaultHeartbeatCheck    = time.Second
	defaultMaxStoredBytes    = 128 * 1024
	defaultHeadBytes         = 8 * 1024
	defaultTailBytes         = 112 * 1024
	defaultFinalChunkBytes   = 3900
)

var defaultRenderTruncationMarker = "\n\n[output truncated]\n\n"

type MessageOptions struct {
	ParseMode string
}

type RendererOptions struct {
	Messenger        Messenger
	Now              func() time.Time
	ProgressThrottle time.Duration
	HeartbeatDelay   time.Duration
}

type RenderState struct {
	ChatID          int64
	ThreadID        string
	TurnID          string
	Project         string
	StatusMessageID int64
	StartedAt       time.Time
	LastEventAt     time.Time
	LastEditAt      time.Time
	LastHeartbeatAt time.Time

	Activity     string
	ChangedFiles int
	AssistantRaw []byte
	Done         bool
}

type renderState struct {
	RenderState
}

type Renderer struct {
	mu sync.Mutex

	messenger Messenger
	now       func() time.Time

	progressThrottle time.Duration
	heartbeatDelay   time.Duration

	heartbeatTicker *time.Ticker
	heartbeatStop   chan struct{}
	heartbeatOnce   sync.Once
	heartbeatDone   sync.WaitGroup

	threads      map[string]string       // threadID -> project path
	chatByThread map[string]int64        // threadID -> chatID
	statesByTurn map[string]*renderState // turnID -> render state
}

func NewRenderer(opts RendererOptions) *Renderer {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	progressThrottle := opts.ProgressThrottle
	if progressThrottle == 0 {
		progressThrottle = defaultProgressThrottle
	}

	heartbeatDelay := opts.HeartbeatDelay
	if heartbeatDelay == 0 {
		heartbeatDelay = defaultHeartbeatInterval
	}

	return &Renderer{
		messenger:        opts.Messenger,
		now:              now,
		progressThrottle: progressThrottle,
		heartbeatDelay:   heartbeatDelay,
		threads:          make(map[string]string),
		chatByThread:     make(map[string]int64),
		statesByTurn:     make(map[string]*renderState),
	}
}

func (r *Renderer) SetThread(chatID int64, threadID, project string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threads[threadID] = project
	r.chatByThread[threadID] = chatID
}

func (r *Renderer) Shutdown(ctx context.Context) {
	_ = ctx
	r.mu.Lock()
	for _, state := range r.statesByTurn {
		state.Done = true
	}
	stop := r.heartbeatStop
	r.heartbeatStop = nil
	r.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	r.heartbeatDone.Wait()
}

func (r *Renderer) OnEvent(ctx context.Context, event codex.Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if event.ThreadID == "" || event.TurnID == "" {
		return nil
	}

	r.startHeartbeatLoop()

	now := r.now()
	state := r.ensureTurnState(event.ThreadID, event.TurnID, now)
	if state == nil {
		return nil
	}

	var changed int
	text := event.Text
	if event.Raw != nil {
		parsedChanged := parseChangedFiles(event.Raw)
		if parsedChanged >= 0 {
			changed = parsedChanged
		}
		if parsedText := parseEventText(event.Raw); parsedText != "" {
			text = parsedText
		}
	}

	r.mu.Lock()
	if state.Done {
		r.mu.Unlock()
		return nil
	}

	state.LastEventAt = now
	state.Activity = firstNonEmpty(parseActivity(event.Raw), state.Activity)
	if changed >= 0 {
		state.ChangedFiles = changed
	}

	visible := isVisibleRenderEvent(event.Method)
	switch event.Method {
	case "item/agentMessage/delta", "item/completed":
		state.AssistantRaw = appendAssistantText(state.AssistantRaw, text)
	case "turn/completed", "turn/failed", "turn/interrupted", "turn/faulted":
		if text != "" {
			state.AssistantRaw = appendAssistantText(state.AssistantRaw, text)
		}
		state.Done = true
	case "item/started":
		if text != "" && state.Activity == "" {
			state.Activity = text
		}
	}

	r.mu.Unlock()

	if state.Done {
		return r.finalize(ctx, state, event.Method)
	}
	if visible {
		return r.sendProgress(ctx, state, now, false)
	}
	return nil
}

func (r *Renderer) ensureTurnState(threadID, turnID string, now time.Time) *renderState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.statesByTurn[turnID]
	if state == nil {
		state = &renderState{
			RenderState: RenderState{
				ChatID:    r.chatByThread[threadID],
				ThreadID:  threadID,
				TurnID:    turnID,
				Project:   r.threads[threadID],
				StartedAt: now,
			},
		}
		r.statesByTurn[turnID] = state
		return state
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = now
		state.LastEventAt = now
	}
	return state
}

func (r *Renderer) finalize(ctx context.Context, state *renderState, method string) error {
	if state == nil || state.ChatID == 0 {
		r.mu.Lock()
		if state != nil {
			delete(r.statesByTurn, state.TurnID)
		}
		r.mu.Unlock()
		return nil
	}

	response := strings.TrimSpace(string(state.AssistantRaw))
	if response == "" {
		switch method {
		case "turn/completed":
			response = "turn completed"
		case "turn/failed":
			response = "turn failed"
		case "turn/interrupted":
			response = "turn interrupted"
		case "turn/faulted":
			response = "turn faulted"
		default:
			response = "turn completed"
		}
		if state.Activity != "" {
			response = response + ": " + state.Activity
		}
	}

	if err := sendWithMarkdownFallback(ctx, r.messenger, state.ChatID, response, nil, MessageOptions{}, defaultFinalChunkBytes); err != nil {
		return err
	}

	r.mu.Lock()
	delete(r.statesByTurn, state.TurnID)
	r.mu.Unlock()
	return nil
}

func (r *Renderer) sendProgress(ctx context.Context, state *renderState, now time.Time, force bool) error {
	if state == nil || state.ChatID == 0 {
		return nil
	}
	text := renderStatusText(state.Project, state.Activity, state.ChangedFiles, now.Sub(state.StartedAt))

	r.mu.Lock()
	if !force {
		if !state.LastEditAt.IsZero() && now.Sub(state.LastEditAt) < r.progressThrottle {
			r.mu.Unlock()
			return nil
		}
	}

	existingID := state.StatusMessageID
	r.mu.Unlock()

	if existingID == 0 {
		statusID, err := r.messenger.Send(ctx, state.ChatID, text, nil, MessageOptions{})
		if err != nil {
			return err
		}
		r.mu.Lock()
		state.StatusMessageID = statusID
		state.LastEditAt = now
		state.LastHeartbeatAt = now
		r.mu.Unlock()
		return nil
	}

	if err := r.messenger.Edit(ctx, state.ChatID, existingID, text, nil, MessageOptions{}); err != nil {
		return err
	}
	r.mu.Lock()
	state.LastEditAt = now
	state.LastHeartbeatAt = now
	r.mu.Unlock()
	return nil
}

func (r *Renderer) startHeartbeatLoop() {
	r.heartbeatOnce.Do(func() {
		stop := make(chan struct{})
		r.mu.Lock()
		r.heartbeatStop = stop
		r.mu.Unlock()

		r.heartbeatDone.Add(1)
		go func() {
			ticker := time.NewTicker(defaultHeartbeatCheck)
			defer ticker.Stop()
			defer r.heartbeatDone.Done()
			for {
				select {
				case now := <-ticker.C:
					r.pulseHeartbeat(now)
				case <-stop:
					return
				}
			}
		}()
	})
}

func (r *Renderer) pulseHeartbeat(now time.Time) {
	ready := make([]*renderState, 0)
	r.mu.Lock()
	for _, state := range r.statesByTurn {
		if state.Done || state.ChatID == 0 {
			continue
		}
		if now.Sub(state.LastEventAt) < r.heartbeatDelay {
			continue
		}
		if !state.LastHeartbeatAt.IsZero() && now.Sub(state.LastHeartbeatAt) < r.heartbeatDelay {
			continue
		}
		state.LastHeartbeatAt = now
		snapshot := *state
		ready = append(ready, &snapshot)
	}
	r.mu.Unlock()

	for _, state := range ready {
		_ = r.sendProgress(context.Background(), state, now, true)
	}
}

func isVisibleRenderEvent(method string) bool {
	switch method {
	case "item/started", "item/agentMessage/delta", "item/completed", "item/failed":
		return true
	default:
		return false
	}
}

func renderStatusText(project, activity string, changedFiles int, elapsed time.Duration) string {
	if activity == "" {
		activity = "running"
	}
	return fmt.Sprintf(
		"project: %s\nactivity: %s\nchanged files: %d\nelapsed: %s",
		project,
		activity,
		changedFiles,
		roundDuration(elapsed),
	)
}

func roundDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	if d < time.Second {
		return "0s"
	}
	return d.Truncate(time.Second).String()
}

func parseActivity(raw json.RawMessage) string {
	var body map[string]any
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	if value, ok := body["activity"].(string); ok {
		return strings.TrimSpace(value)
	}
	if value, ok := body["kind"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func parseEventText(raw json.RawMessage) string {
	var body map[string]any
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	if value, ok := body["text"].(string); ok {
		return value
	}
	if item, ok := body["item"].(map[string]any); ok {
		if value, ok := item["text"].(string); ok {
			return value
		}
	}
	return ""
}

func parseChangedFiles(raw json.RawMessage) int {
	var body map[string]any
	if len(raw) == 0 {
		return -1
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return -1
	}

	if n := readIntField(body, "changedFiles"); n >= 0 {
		return n
	}
	if n := readIntField(body, "changedFileCount"); n >= 0 {
		return n
	}
	if item, ok := body["item"].(map[string]any); ok {
		if n := readIntField(item, "changedFiles"); n >= 0 {
			return n
		}
		if n := readIntField(item, "changedFileCount"); n >= 0 {
			return n
		}
	}
	return -1
}

func readIntField(data map[string]any, key string) int {
	raw, ok := data[key]
	if !ok {
		return -1
	}
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return -1
		}
		return int(parsed)
	case string:
		var parsed int
		_, err := fmt.Sscanf(value, "%d", &parsed)
		if err != nil {
			return -1
		}
		return parsed
	default:
		return -1
	}
}

func appendAssistantText(existing []byte, delta string) []byte {
	if delta == "" {
		return existing
	}
	composed := make([]byte, 0, len(existing)+len(delta))
	composed = append(composed, existing...)
	composed = append(composed, []byte(delta)...)
	if len(composed) <= defaultMaxStoredBytes {
		return composed
	}

	prefix := clampUTF8(composed[:defaultHeadBytes], defaultHeadBytes)
	tailStart := len(composed) - defaultTailBytes
	if tailStart < 0 {
		tailStart = 0
	}
	tail := clampUTF8(composed[tailStart:], defaultTailBytes)

	out := make([]byte, 0, len(prefix)+len(defaultRenderTruncationMarker)+len(tail))
	out = append(out, prefix...)
	out = append(out, []byte(defaultRenderTruncationMarker)...)
	return append(out, tail...)
}

func clampUTF8(input []byte, max int) []byte {
	if len(input) <= max {
		return append([]byte(nil), input...)
	}
	clamped := append([]byte(nil), input[:max]...)
	for len(clamped) > 0 && !utf8.Valid(clamped) {
		clamped = clamped[:len(clamped)-1]
	}
	return clamped
}

func splitUTF8(text string, max int) []string {
	if max <= 0 {
		return []string{text}
	}
	raw := []byte(text)
	if len(raw) <= max {
		return []string{text}
	}

	var chunks []string
	for len(raw) > 0 {
		size := max
		if len(raw) < size {
			size = len(raw)
		}
		for size > 0 && !utf8.Valid(raw[:size]) {
			size--
		}
		chunks = append(chunks, string(raw[:size]))
		raw = raw[size:]
	}
	return chunks
}

func sendWithMarkdownFallback(ctx context.Context, messenger Messenger, chatID int64, text string, keyboard *InlineKeyboard, opts MessageOptions, maxBytes int) error {
	chunks := splitUTF8(text, maxBytes)
	for _, chunk := range chunks {
		markdown := opts
		markdown.ParseMode = "MarkdownV2"
		if _, err := messenger.Send(ctx, chatID, chunk, keyboard, markdown); err != nil {
			if !isMarkdownParseError(err) {
				return err
			}
			if _, fallbackErr := messenger.Send(ctx, chatID, chunk, keyboard, MessageOptions{}); fallbackErr != nil {
				return fallbackErr
			}
			continue
		}
	}
	return nil
}

func isMarkdownParseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "parse") || strings.Contains(msg, "markdown") || strings.Contains(msg, "entities")
}
