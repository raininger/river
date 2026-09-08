package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Bot struct {
	token  string
	chatID string
	hc     *http.Client
}

func New(token, chatID string) *Bot {
	return &Bot{
		token:  token,
		chatID: chatID,
		hc:     &http.Client{Timeout: 20 * time.Second},
	}
}

func (b *Bot) Send(ctx context.Context, text string) error {
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  b.chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("解析 telegram 响应失败: %w", err)
	}
	if !r.OK {
		return fmt.Errorf("telegram 发送失败: %s", r.Description)
	}
	return nil
}
