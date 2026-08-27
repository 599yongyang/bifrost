package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

type ChannelNotification struct {
	Title     string
	Text      string
	Markdown  string
	Event     string
	Payload   map[string]any
	Details   map[string]any
	Severity  string
	Source    string
	Timestamp time.Time
	Test      bool
}

func (m *Manager) SendNotification(ctx context.Context, channel *tables.TableAlertChannel, notification ChannelNotification) error {
	if err := m.ValidateChannel(channel); err != nil {
		return err
	}
	now := notification.Timestamp.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var endpoint string
	var payload any
	switch channel.Type {
	case tables.AlertChannelSlack:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{"blocks": []any{
			map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": notification.Title}},
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "```" + notification.Text + "```"}},
		}}
	case tables.AlertChannelMicrosoftTeams:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{"type": "message", "attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []any{
					map[string]any{"type": "TextBlock", "weight": "Bolder", "text": notification.Title},
					map[string]any{"type": "TextBlock", "wrap": true, "text": notification.Text},
				},
			},
		}}}
	case tables.AlertChannelWeCom:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": notification.Markdown},
		}
	case tables.AlertChannelPagerDuty:
		endpoint = "https://events.pagerduty.com/v2/enqueue"
		payload = map[string]any{
			"routing_key":  stringConfig(channel.Config, "routing_key", "integration_key"),
			"event_action": "trigger",
			"dedup_key":    notification.Event,
			"payload": map[string]any{
				"summary":        notification.Text,
				"source":         firstNonEmpty(notification.Source, "Bifrost"),
				"severity":       firstNonEmpty(notification.Severity, "info"),
				"custom_details": notification.Details,
			},
		}
	case tables.AlertChannelWebhook:
		endpoint = stringConfig(channel.Config, "url", "webhook_url")
		webhookPayload := notification.Payload
		if webhookPayload == nil {
			webhookPayload = map[string]any{
				"event":     notification.Event,
				"timestamp": now,
				"title":     notification.Title,
				"message":   notification.Text,
				"details":   notification.Details,
			}
		}
		payload = webhookPayload
	default:
		return fmt.Errorf("unsupported alert channel type: %s", channel.Type)
	}
	if err := m.validateURL(endpoint); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if channel.Type == tables.AlertChannelMicrosoftTeams && len(body) > 28*1024 {
		return fmt.Errorf("microsoft teams payload exceeds 28 KB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Type == tables.AlertChannelWebhook {
		for key, value := range stringMapConfig(channel.Config, "headers") {
			if !blockedHeader(key) {
				req.Header.Set(key, value)
			}
		}
	}
	client := m.client
	if m.network.AllowPrivateNetwork {
		client = m.privateClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delivery failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		return fmt.Errorf("read delivery response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delivery failed with HTTP %d", resp.StatusCode)
	}
	if channel.Type == tables.AlertChannelWeCom {
		var result struct {
			ErrorCode int    `json:"errcode"`
			ErrorText string `json:"errmsg"`
		}
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return fmt.Errorf("invalid WeCom webhook response: %w", err)
		}
		if result.ErrorCode != 0 {
			return fmt.Errorf("WeCom webhook rejected the message (errcode %d: %s)", result.ErrorCode, result.ErrorText)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
