package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DiscordWebhook struct {
	webhookURL string
	httpClient *http.Client
}

func NewDiscordWebhook(webhookURL string, timeout time.Duration) *DiscordWebhook {
	return &DiscordWebhook{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (d *DiscordWebhook) Send(ctx context.Context, content string) error {
	payload, err := json.Marshal(map[string]string{
		"content": content,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal discord payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create discord request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := d.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to send discord notification: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", response.StatusCode)
	}

	return nil
}
