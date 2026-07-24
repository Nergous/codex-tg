package models

type QueuedMessage struct {
	ID        int64
	ThreadID  string
	ChatID    int64
	Text      string
	CreatedAt int64
}
