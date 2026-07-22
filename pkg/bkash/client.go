// Package bkash is a server-side client for bKash's Tokenized Checkout API
// (v1.2.0-beta). It talks to bKash directly — sandbox or production is
// chosen once, at startup, from server config (Config.BkashProduction),
// never by the caller — the client app never gets a say in which
// environment a payment runs against.
package bkash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"campusassistant-api/internal/config"
)

const (
	sandboxBaseURL    = "https://tokenized.sandbox.bka.sh/v1.2.0-beta"
	productionBaseURL = "https://tokenized.pay.bka.sh/v1.2.0-beta"

	requestTimeout = 15 * time.Second
	// Refresh the cached token this long before it actually expires, so a
	// request never races a token that's about to be rejected.
	tokenExpirySafetyMargin = 60 * time.Second
)

type Client struct {
	baseURL      string
	username     string
	password     string
	appKey       string
	appSecret    string
	httpClient   *http.Client
	isProduction bool

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// IsProduction reports which bKash environment this client was built for.
func (c *Client) IsProduction() bool {
	return c.isProduction
}

func NewClient(cfg *config.Config) *Client {
	c := &Client{httpClient: &http.Client{Timeout: requestTimeout}, isProduction: cfg.BkashProduction}
	if cfg.BkashProduction {
		c.baseURL = productionBaseURL
		c.username = cfg.BkashProdUsername
		c.password = cfg.BkashProdPassword
		c.appKey = cfg.BkashProdAppKey
		c.appSecret = cfg.BkashProdAppSecret
	} else {
		c.baseURL = sandboxBaseURL
		c.username = cfg.BkashSandboxUsername
		c.password = cfg.BkashSandboxPassword
		c.appKey = cfg.BkashSandboxAppKey
		c.appSecret = cfg.BkashSandboxAppSecret
	}
	return c
}

type grantTokenResponse struct {
	StatusCode    string `json:"statusCode"`
	StatusMessage string `json:"statusMessage"`
	IDToken       string `json:"id_token"`
	TokenType     string `json:"token_type"`
	ExpiresIn     int    `json:"expires_in"`
	RefreshToken  string `json:"refresh_token"`
}

// CreatePaymentResult mirrors the fields the caller actually needs from
// bKash's create-payment response.
type CreatePaymentResult struct {
	PaymentID            string `json:"paymentID"`
	BkashURL             string `json:"bkashURL"`
	TransactionStatus    string `json:"transactionStatus"`
	StatusCode           string `json:"statusCode"`
	StatusMessage        string `json:"statusMessage"`
	SuccessCallbackURL   string `json:"successCallbackURL"`
	FailureCallbackURL   string `json:"failureCallbackURL"`
	CancelledCallbackURL string `json:"cancelledCallbackURL"`
}

// ExecutePaymentResult mirrors the fields the caller needs from bKash's
// execute-payment response.
type ExecutePaymentResult struct {
	PaymentID         string `json:"paymentID"`
	TrxID             string `json:"trxID"`
	TransactionStatus string `json:"transactionStatus"`
	Amount            string `json:"amount"`
	Currency          string `json:"currency"`
	StatusCode        string `json:"statusCode"`
	StatusMessage     string `json:"statusMessage"`
}

// grantToken returns a cached id_token if it's still valid, otherwise grants
// a fresh one. Guarded by mu so concurrent requests share one grant call
// instead of each racing bKash for their own token.
func (c *Client) grantToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	body, err := json.Marshal(map[string]string{
		"app_key":    c.appKey,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("bkash: encode grant token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tokenized/checkout/token/grant", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("bkash: build grant token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("username", c.username)
	req.Header.Set("password", c.password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bkash: grant token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("bkash: read grant token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bkash: grant token returned %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed grantTokenResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("bkash: decode grant token response: %w (body: %s)", err, string(respBody))
	}
	if parsed.IDToken == "" {
		return "", fmt.Errorf("bkash: grant token response missing id_token: %s", string(respBody))
	}

	c.cachedToken = parsed.IDToken
	expiresIn := time.Duration(parsed.ExpiresIn) * time.Second
	if expiresIn <= tokenExpirySafetyMargin {
		expiresIn = tokenExpirySafetyMargin
	}
	c.tokenExpiry = time.Now().Add(expiresIn - tokenExpirySafetyMargin)

	return c.cachedToken, nil
}

// CreatePayment starts a bKash tokenized checkout session for amountBDT
// (whole taka, matching SubscriptionPlan.Price). callbackURL is where bKash
// redirects the user's browser/WebView after payment.
func (c *Client) CreatePayment(ctx context.Context, amountBDT int, invoiceNumber, payerReference, callbackURL string) (*CreatePaymentResult, error) {
	idToken, err := c.grantToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"mode":                  "0011",
		"payerReference":        payerReference,
		"callbackURL":           callbackURL,
		"amount":                fmt.Sprintf("%d", amountBDT),
		"currency":              "BDT",
		"intent":                "sale",
		"merchantInvoiceNumber": invoiceNumber,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("bkash: encode create payment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tokenized/checkout/create", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bkash: build create payment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", idToken)
	req.Header.Set("X-App-Key", c.appKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bkash: create payment request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bkash: read create payment response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bkash: create payment returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result CreatePaymentResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("bkash: decode create payment response: %w (body: %s)", err, string(respBody))
	}
	if result.PaymentID == "" || result.BkashURL == "" {
		return nil, fmt.Errorf("bkash: create payment response missing paymentID/bkashURL: %s", string(respBody))
	}

	return &result, nil
}

// ExecutePayment finalizes a payment session with bKash. This is the only
// authoritative source of truth for whether a payment actually completed —
// callers must never trust a client-supplied "success" status without
// calling this.
func (c *Client) ExecutePayment(ctx context.Context, paymentID string) (*ExecutePaymentResult, error) {
	idToken, err := c.grantToken(ctx)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]string{"paymentID": paymentID})
	if err != nil {
		return nil, fmt.Errorf("bkash: encode execute payment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tokenized/checkout/execute", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bkash: build execute payment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", idToken)
	req.Header.Set("X-App-Key", c.appKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bkash: execute payment request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bkash: read execute payment response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bkash: execute payment returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ExecutePaymentResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("bkash: decode execute payment response: %w (body: %s)", err, string(respBody))
	}

	return &result, nil
}
