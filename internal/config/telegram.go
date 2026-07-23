package config

type TelegramConfig struct {
	AllowedUserID int64 `json:"allowed_user_id"`
	AllowedChatID int64 `json:"allowed_chat_id"`
}

func (t *TelegramConfig) validate() error {
	if t == nil {
		return ErrEmptyTelegramConfig
	}

	if t.AllowedChatID < 1 || t.AllowedUserID < 1 {
		return ErrInvalidTelegramID
	}

	return nil
}
