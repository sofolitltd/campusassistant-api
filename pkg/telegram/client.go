// Package telegram sends plain-text admin alerts to a Telegram chat via the
// Bot API. It mirrors how pkg/mailer and pkg/fcm degrade: a missing
// TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID disables sending (logged instead)
// rather than preventing the server from booting.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/config"
)

const requestTimeout = 10 * time.Second

// Client posts messages to one configured Telegram chat. A Client with an
// empty token or chat ID is a no-op that only logs — see New.
type Client struct {
	botToken   string
	chatID     string
	httpClient *http.Client
}

// New returns a Client wired to cfg's Telegram settings. If either
// TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID is unset, SendMessage logs instead
// of sending, so local development and deployments that don't need this
// integration keep working unchanged.
func New(cfg *config.Config) *Client {
	return &Client{
		botToken:   strings.TrimSpace(cfg.TelegramBotToken),
		chatID:     strings.TrimSpace(cfg.TelegramChatID),
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// SendMessage posts text to the configured chat. Errors are the caller's to
// handle (or ignore) — this is a best-effort admin notification, never a
// condition that should fail the request that triggered it.
func (c *Client) SendMessage(ctx context.Context, text string) error {
	if c.botToken == "" || c.chatID == "" {
		log.Printf("[telegram] (not configured) would send: %s", text)
		return nil
	}

	body, err := json.Marshal(map[string]string{
		"chat_id": c.chatID,
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("telegram: encode send message request: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build send message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send message request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: send message returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
