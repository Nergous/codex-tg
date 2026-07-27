package models

type Approval struct {
	Nonce     string
	RequestID string
	ThreadID  string
	ChatID    int64
	Kind      string
	ExpiresAt int64
	Resolved  bool
	Decision  string
}
