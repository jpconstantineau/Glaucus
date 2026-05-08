package messaging

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

type WebhookConfig struct {
	ProfileID       string
	AdapterID       string
	OutboundURL     string
	BearerToken     string
	HMACSecret      string
	AllowedCIDRs    []string
	StaticUserScope string
	DefaultChatID   string
	DefaultThreadID string
}

type WebhookAdapter struct {
	config WebhookConfig
	client *http.Client
}

func NewWebhookAdapter(cfg WebhookConfig, client *http.Client) *WebhookAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &WebhookAdapter{config: cfg, client: client}
}

func (a *WebhookAdapter) Platform() string { return PlatformWebhook }

func (a *WebhookAdapter) Start(context.Context) error { return nil }

func (a *WebhookAdapter) Stop(context.Context) error { return nil }

func (a *WebhookAdapter) Health(context.Context) (AdapterHealth, error) {
	status := "healthy"
	lastError := ""
	if strings.TrimSpace(a.config.OutboundURL) == "" {
		status = "misconfigured"
		lastError = "webhook outbound URL is not configured"
	}
	authMode := "bearer_token"
	switch {
	case strings.TrimSpace(a.config.HMACSecret) != "":
		authMode = "hmac"
	case len(a.config.AllowedCIDRs) > 0 && strings.TrimSpace(a.config.BearerToken) == "":
		authMode = "ip_allowlist"
	}
	return AdapterHealth{
		Status:       status,
		AuthMode:     authMode,
		LastError:    lastError,
		Capabilities: []string{"inbound", "outbound", "health", "audit"},
	}, nil
}

func (a *WebhookAdapter) NormalizeRequest(r *http.Request, body []byte) (InboundEvent, error) {
	if err := a.validateRequest(r, body); err != nil {
		return InboundEvent{}, err
	}

	var payload struct {
		Actor struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Username    string `json:"username"`
		} `json:"actor"`
		ChatID      string         `json:"chat_id"`
		ThreadID    string         `json:"thread_id"`
		UserScope   string         `json:"user_scope"`
		Text        string         `json:"text"`
		Content     []ContentPart  `json:"content"`
		Attachments []Attachment   `json:"attachments"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return InboundEvent{}, fmt.Errorf("decode webhook payload: %w", err)
	}
	content := payload.Content
	if len(content) == 0 {
		content = []ContentPart{{Type: "input_text", Text: payload.Text}}
	}

	return InboundEvent{
		Platform:  PlatformWebhook,
		ProfileID: a.config.ProfileID,
		AdapterID: a.config.AdapterID,
		Actor: Actor{
			ID:          firstNonEmpty(payload.Actor.ID, "webhook"),
			DisplayName: payload.Actor.DisplayName,
			Username:    payload.Actor.Username,
		},
		ChatID:      firstNonEmpty(payload.ChatID, a.config.DefaultChatID, "webhook"),
		ThreadID:    firstNonEmpty(payload.ThreadID, a.config.DefaultThreadID),
		UserScope:   firstNonEmpty(payload.UserScope, a.config.StaticUserScope),
		Content:     content,
		Attachments: payload.Attachments,
		Metadata:    payload.Metadata,
	}, nil
}

func (a *WebhookAdapter) SendMessage(ctx context.Context, target DeliveryTarget, message OutboundMessage) (DeliveryResult, error) {
	if strings.TrimSpace(a.config.OutboundURL) == "" {
		return DeliveryResult{}, fmt.Errorf("webhook outbound URL is not configured")
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":    target.ChatID,
		"thread_id":  target.ThreadID,
		"user_scope": target.UserScope,
		"text":       message.Text,
		"metadata":   message.Metadata,
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.OutboundURL, bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return DeliveryResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return DeliveryResult{}, fmt.Errorf("webhook outbound delivery returned %d", response.StatusCode)
	}
	return DeliveryResult{
		Status: "sent",
		Details: map[string]any{
			"endpoint": a.config.OutboundURL,
		},
	}, nil
}

func (a *WebhookAdapter) validateRequest(r *http.Request, body []byte) error {
	if token := strings.TrimSpace(a.config.BearerToken); token != "" {
		if got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); got != token {
			return fmt.Errorf("invalid bearer token")
		}
	}
	if secret := strings.TrimSpace(a.config.HMACSecret); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		actual := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("X-Glaucus-Signature"), "sha256="))
		if !hmac.Equal([]byte(expected), []byte(actual)) {
			return fmt.Errorf("invalid hmac signature")
		}
	}
	if len(a.config.AllowedCIDRs) > 0 {
		if err := allowCIDRs(a.config.AllowedCIDRs, r.RemoteAddr); err != nil {
			return err
		}
	}
	return nil
}

func allowCIDRs(cidrs []string, remoteAddr string) error {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("remote address %q is invalid", remoteAddr)
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err == nil && network.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("remote address %s is not allowed", ip.String())
}
