package fcm

import (
	"context"
	"errors"

	"campusassistant-api/internal/config"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// maxTokensPerRequest is FCM's hard limit for a single multicast send.
const maxTokensPerRequest = 500

type Client struct {
	msg *messaging.Client
}

// NewClient builds an FCM client from cfg's credentials. cfg.FirebaseCredentialsJSON
// (raw service-account JSON) takes priority if set; otherwise cfg.FirebaseCredentialsFile
// is read from disk. Callers should treat a non-nil error as "push unavailable" and
// keep running without it rather than failing startup.
func NewClient(ctx context.Context, cfg *config.Config) (*Client, error) {
	var opt option.ClientOption
	switch {
	case cfg.FirebaseCredentialsJSON != "":
		opt = option.WithCredentialsJSON([]byte(cfg.FirebaseCredentialsJSON))
	case cfg.FirebaseCredentialsFile != "":
		opt = option.WithCredentialsFile(cfg.FirebaseCredentialsFile)
	default:
		return nil, errors.New("no Firebase credentials configured")
	}

	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, err
	}

	msgClient, err := app.Messaging(ctx)
	if err != nil {
		return nil, err
	}

	return &Client{msg: msgClient}, nil
}

// SubscribeToTopic subscribes tokens to topic (FCM's IID API — max 1000 tokens
// per call; callers with more should chunk).
func (c *Client) SubscribeToTopic(ctx context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error) {
	return c.msg.SubscribeToTopic(ctx, tokens, topic)
}

// UnsubscribeFromTopic unsubscribes tokens from topic.
func (c *Client) UnsubscribeFromTopic(ctx context.Context, tokens []string, topic string) (*messaging.TopicManagementResponse, error) {
	return c.msg.UnsubscribeFromTopic(ctx, tokens, topic)
}

// SendToTopic pushes a single message to every device subscribed to topic and
// returns FCM's message ID. imageURL is optional — pass "" for a plain text
// notification.
func (c *Client) SendToTopic(ctx context.Context, topic, title, body, imageURL string, data map[string]string) (string, error) {
	return c.msg.Send(ctx, &messaging.Message{
		Topic: topic,
		Notification: &messaging.Notification{
			Title:    title,
			Body:     body,
			ImageURL: imageURL,
		},
		Data: data,
	})
}

// SendResult summarizes a Send call across all token batches.
type SendResult struct {
	SuccessCount int
	FailureCount int
	// InvalidTokens are tokens FCM reported as unregistered/invalid — safe to
	// delete from user_devices so we stop retrying dead installs.
	InvalidTokens []string
}

// Send pushes the same title/body/data to every token, chunking into FCM's
// 500-token-per-request limit and aggregating the results. imageURL is
// optional — pass "" for a plain text notification.
func (c *Client) Send(ctx context.Context, tokens []string, title, body, imageURL string, data map[string]string) (*SendResult, error) {
	result := &SendResult{}

	for start := 0; start < len(tokens); start += maxTokensPerRequest {
		end := start + maxTokensPerRequest
		if end > len(tokens) {
			end = len(tokens)
		}
		batch := tokens[start:end]

		resp, err := c.msg.SendEachForMulticast(ctx, &messaging.MulticastMessage{
			Tokens: batch,
			Notification: &messaging.Notification{
				Title:    title,
				Body:     body,
				ImageURL: imageURL,
			},
			Data: data,
		})
		if err != nil {
			return result, err
		}

		result.SuccessCount += resp.SuccessCount
		result.FailureCount += resp.FailureCount
		for i, r := range resp.Responses {
			if !r.Success && messaging.IsRegistrationTokenNotRegistered(r.Error) {
				result.InvalidTokens = append(result.InvalidTokens, batch[i])
			}
		}
	}

	return result, nil
}
