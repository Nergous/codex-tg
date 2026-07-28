package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{},
	}
}

var errEmptyIPCClientConfig = errors.New("missing ipc client configuration")

func (c *Client) Open(ctx context.Context, req OpenRequest) (OpenResponse, error) {
	if err := c.validate(); err != nil {
		return OpenResponse{}, err
	}
	var out OpenResponse
	if err := c.call(ctx, http.MethodPost, openPath, req, &out); err != nil {
		return OpenResponse{}, err
	}
	return out, nil
}

func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	if err := c.validate(); err != nil {
		return StatusResponse{}, err
	}
	var out StatusResponse
	if err := c.call(ctx, http.MethodGet, statusPath, nil, &out); err != nil {
		return StatusResponse{}, err
	}
	return out, nil
}

func (c *Client) Stop(ctx context.Context) error {
	if err := c.validate(); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, stopPath, nil, &struct{}{})
}

func (c *Client) RegisterProject(ctx context.Context, project ProjectRequest) error {
	if err := c.validate(); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, projectPath, project, &struct{}{})
}

func (c *Client) validate() error {
	if strings.TrimSpace(c.baseURL) == "" || strings.TrimSpace(c.token) == "" {
		return errEmptyIPCClientConfig
	}
	return nil
}

func (c *Client) call(ctx context.Context, method, path string, in any, out any) error {
	var body []byte
	if in != nil {
		var err error
		body, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		detail := strings.TrimSpace(string(message))
		if detail != "" {
			return fmt.Errorf("request failed: %s: %s", resp.Status, detail)
		}
		return fmt.Errorf("request failed: %s", resp.Status)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
