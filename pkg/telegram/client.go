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
	if !c.configured() {
		log.Printf("[telegram] (not configured) would send: %s", text)
		return nil
	}
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id": c.chatID,
		"text":    text,
	})
}

// SendPhotos posts images by public URL — Telegram fetches them itself, so
// the URLs must be reachable from the internet. One URL goes out as a
// single photo; 2–10 as an album. Extra URLs beyond 10 are dropped.
func (c *Client) SendPhotos(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	if !c.configured() {
		log.Printf("[telegram] (not configured) would send %d photo(s)", len(urls))
		return nil
	}
	if len(urls) == 1 {
		return c.call(ctx, "sendPhoto", map[string]any{
			"chat_id": c.chatID,
			"photo":   urls[0],
		})
	}
	if len(urls) > 10 {
		urls = urls[:10]
	}
	media := make([]map[string]string, len(urls))
	for i, u := range urls {
		media[i] = map[string]string{"type": "photo", "media": u}
	}
	return c.call(ctx, "sendMediaGroup", map[string]any{
		"chat_id": c.chatID,
		"media":   media,
	})
}

func (c *Client) configured() bool {
	return c.botToken != "" && c.chatID != ""
}

// call POSTs a JSON payload to a Bot API method.
func (c *Client) call(ctx context.Context, method string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: encode %s request: %w", method, err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.botToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: %s returned %d: %s", method, resp.StatusCode, string(respBody))
	}
	return nil
}
