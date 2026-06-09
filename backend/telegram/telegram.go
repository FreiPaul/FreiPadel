package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const base_url = "https://api.telegram.org/bot"

type TelegramSender struct {
	bot_token string
	client    *http.Client
}

type sendMessagePayload struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

func NewSender(token string) *TelegramSender {
	newClient := http.Client{Timeout: 25 * time.Second}
	return &TelegramSender{bot_token: token, client: &newClient}
}

func (s *TelegramSender) SendMsg(chatid uint32, msg string) error {

	if chatid == 0 || msg == "" {
		return nil //abort without error
	}

	payload := sendMessagePayload{ChatID: fmt.Sprintf("%d", chatid), Text: msg}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	telegram_url := fmt.Sprintf("%s%s/sendMessage", base_url, s.bot_token)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, telegram_url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessage returned status %d: %s", resp.StatusCode, body)
	}
	return nil

}
