package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	getUpdatesTimeout = 35 * time.Second
	callTimeout       = 10 * time.Second
)

var (
	ErrUnauthorized = errors.New("telegram unauthorized")
	ErrAPI          = errors.New("telegram API error")
)

type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return ErrAPI.Error()
	}
	if e.Code == 0 {
		if strings.TrimSpace(e.Description) == "" {
			return ErrAPI.Error()
		}
		return e.Description
	}
	return fmt.Sprintf("%s (%d)", e.Description, e.Code)
}

type responseEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	sleep       func(time.Duration)
	retryDelay  func() time.Duration
	defaultGet  func() time.Duration
	defaultCall func() time.Duration
}

func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		token:       token,
		httpClient:  httpClient,
		sleep:       time.Sleep,
		retryDelay:  randomRetryDelay,
		defaultGet:  func() time.Duration { return getUpdatesTimeout },
		defaultCall: func() time.Duration { return callTimeout },
	}
}

func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	request := struct {
		Offset         int64    `json:"offset"`
		Limit          int      `json:"limit"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{
		Offset:         offset,
		Limit:          100,
		Timeout:        int(c.defaultGet().Seconds()),
		AllowedUpdates: []string{"message", "callback_query"},
	}

	var out []Update
	if err := c.call(ctx, "getUpdates", request, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type sendRequest struct {
	ChatID      int64           `json:"chat_id"`
	Text        string          `json:"text"`
	ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
	ParseMode   string          `json:"parse_mode,omitempty"`
}

func (c *Client) Send(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) (int64, error) {
	request := sendRequest{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   opts.ParseMode,
	}

	var out struct {
		MessageID int64 `json:"message_id"`
	}
	if err := c.call(ctx, "sendMessage", request, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

func (c *Client) Edit(ctx context.Context, chatID, messageID int64, text string, keyboard *InlineKeyboard, opts MessageOptions) error {
	request := struct {
		ChatID      int64           `json:"chat_id"`
		MessageID   int64           `json:"message_id"`
		Text        string          `json:"text"`
		ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
		ParseMode   string          `json:"parse_mode,omitempty"`
	}{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
		ParseMode:   opts.ParseMode,
	}
	return c.call(ctx, "editMessageText", request, nil)
}

func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	request := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text"`
	}{
		CallbackQueryID: callbackID,
		Text:            text,
	}
	return c.call(ctx, "answerCallbackQuery", request, nil)
}

func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", map[string]bool{}, nil)
}

func (c *Client) SetReaction(ctx context.Context, chatID, messageID int64, emoji string) error {
	request := struct {
		ChatID    int64 `json:"chat_id"`
		MessageID int64 `json:"message_id"`
		Reaction  []struct {
			Type  string `json:"type"`
			Emoji string `json:"emoji"`
		} `json:"reaction"`
	}{ChatID: chatID, MessageID: messageID}
	request.Reaction = append(request.Reaction, struct {
		Type  string `json:"type"`
		Emoji string `json:"emoji"`
	}{Type: "emoji", Emoji: emoji})
	return c.call(ctx, "setMessageReaction", request, nil)
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	request := struct {
		ChatID int64  `json:"chat_id"`
		Action string `json:"action"`
	}{ChatID: chatID, Action: action}
	return c.call(ctx, "sendChatAction", request, nil)
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var out User
	if err := c.call(ctx, "getMe", map[string]bool{}, &out); err != nil {
		return User{}, err
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, route string, payload any, out any) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		body, err := c.callOnce(ctx, route, payload)
		if err == nil {
			if out == nil {
				return nil
			}
			return json.Unmarshal(body, out)
		}

		retryAfter, retryable := shouldRetry(err)
		if !retryable {
			return err
		}
		if retryAfter <= 0 {
			retryAfter = c.retryDelay()
		}
		if err := sleepWithContext(ctx, c.sleep, retryAfter); err != nil {
			return err
		}
	}
}

func (c *Client) callOnce(ctx context.Context, route string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrAPI, err)
	}

	timeout := c.defaultCall()
	if route == "getUpdates" {
		timeout = c.defaultGet()
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/"+route, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrAPI, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, classifyNetworkError(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrAPI, err)
	}

	var env responseEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: malformed telegram response: %w", ErrAPI, err)
	}

	if err := classifyResponse(resp.StatusCode, env); err != nil {
		return nil, err
	}
	return env.Result, nil
}

func classifyResponse(status int, env responseEnvelope) error {
	switch status {
	case http.StatusTooManyRequests:
		return &APIError{
			Code:        status,
			Description: env.Description,
			RetryAfter:  time.Duration(env.Parameters.RetryAfter) * time.Second,
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf(
			"%w: %w",
			ErrUnauthorized,
			&APIError{
				Code:        status,
				Description: firstNonEmpty(env.Description, statusText(status)),
			},
		)
	}

	if status >= 400 && status < 600 {
		return &APIError{
			Code:        status,
			Description: firstNonEmpty(env.Description, statusText(status)),
		}
	}

	if !env.OK {
		return &APIError{
			Code:        chooseErrorCode(status, env.ErrorCode),
			Description: firstNonEmpty(env.Description, statusText(status)),
		}
	}
	return nil
}

func chooseErrorCode(httpStatus, responseCode int) int {
	if responseCode != 0 {
		return responseCode
	}
	return httpStatus
}

func classifyNetworkError(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &APIError{
				Code:        http.StatusGatewayTimeout,
				Description: "network timeout",
			}
		}
		return &APIError{
			Code:        0,
			Description: netErr.Error(),
		}
	}
	return &APIError{
		Code:        0,
		Description: err.Error(),
	}
}

func shouldRetry(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if errors.Is(err, ErrUnauthorized) {
			return 0, false
		}
		if apiErr.Code == 0 {
			return 0, true
		}
		switch apiErr.Code {
		case http.StatusTooManyRequests:
			return apiErr.RetryAfter, true
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			return 0, false
		}
		if apiErr.Code >= 500 && apiErr.Code < 600 {
			return 0, true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return 0, true
	}
	return 0, false
}

func sleepWithContext(ctx context.Context, sleep func(time.Duration), d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if sleep == nil {
		sleep = time.Sleep
	}

	done := make(chan struct{})
	go func() {
		sleep(d)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func randomRetryDelay() time.Duration {
	return time.Duration(1+rand.Intn(30)) * time.Second
}

func statusText(status int) string {
	return strings.TrimPrefix(http.StatusText(status), "status code ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
