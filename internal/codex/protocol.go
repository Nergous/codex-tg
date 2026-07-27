package codex

import "encoding/json"

type request struct {
	Method string `json:"method"`
	ID     int64  `json:"id"`
	Params any    `json:"params,omitempty"`
}

type response struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Event is emitted for server-side notifications and server-initiated requests.
// Keep unknown notification fields in Raw to tolerate protocol evolution.
type Event struct {
	Method    string          `json:"-"`
	ThreadID  string          `json:"-"`
	TurnID    string          `json:"-"`
	Text      string          `json:"-"`
	RequestID json.RawMessage `json:"-"`
	Raw       json.RawMessage `json:"-"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type initializeParams struct {
	ClientInfo ClientInfo `json:"clientInfo"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type threadStartParams struct {
	// StartThread maps to thread/start with these params.
	CWD            string `json:"cwd"`
	Sandbox        string `json:"sandbox"`
	ApprovalPolicy string `json:"approvalPolicy"`
}

type threadStartResult struct {
	// StartThread reads the nested thread.id field.
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type threadResumeParams struct {
	// ResumeThread maps to thread/resume with this param.
	ThreadID string `json:"threadId"`
}

type threadResumeResult struct {
	// ResumeThread verifies the nested thread.id value.
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartParams struct {
	// StartTurn maps to turn/start with threadId + one text input item.
	ThreadID string      `json:"threadId"`
	Input    []turnInput `json:"input"`
}

type turnInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type turnStartResult struct {
	// StartTurn reads the nested turn.id field.
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type commandExecParams struct {
	// Exec maps to command/exec.
	CWD     string `json:"cwd"`
	Command string `json:"command"`
}

// CommandResult is the public result of command/exec.
type CommandResult struct {
	ExitCode int    `json:"-"`
	Stdout   string `json:"-"`
	Stderr   string `json:"-"`
}

type commandExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}
