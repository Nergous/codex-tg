package config

import "testing"

func TestTelegram_validate(t *testing.T) {
	cases := map[string]struct {
		input  *TelegramConfig
		output error
	}{
		"nil config": {
			input:  nil,
			output: ErrEmptyTelegramConfig,
		},
		"empty userID": {
			input: &TelegramConfig{
				AllowedUserID: 0,
			},
			output: ErrInvalidTelegramID,
		},
		"empty chatID": {
			input: &TelegramConfig{
				AllowedChatID: 0,
			},
			output: ErrInvalidTelegramID,
		},
		"negative userID": {
			input: &TelegramConfig{
				AllowedUserID: -1,
			},
			output: ErrInvalidTelegramID,
		},
		"negative chatID": {
			input: &TelegramConfig{
				AllowedChatID: -1,
			},
			output: ErrInvalidTelegramID,
		},
		"correct config": {
			input: &TelegramConfig{
				AllowedUserID: 1,
				AllowedChatID: 1,
			},
			output: nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.input.validate()
			if err != tc.output {
				t.Errorf("expected %v, got %v", tc.output, err)
			}
		})
	}
}
