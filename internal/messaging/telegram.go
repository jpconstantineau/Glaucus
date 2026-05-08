package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TelegramConfig struct {
	ProfileID            string
	AdapterID            string
	BotToken             string
	BaseURL              string
	Mode                 string
	PrivateChatUserScope bool
}

type TelegramAdapter struct {
	config TelegramConfig
	client *http.Client
}

func NewTelegramAdapter(cfg TelegramConfig, client *http.Client) *TelegramAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://api.telegram.org"
	}
	return &TelegramAdapter{config: cfg, client: client}
}

func (a *TelegramAdapter) Platform() string { return PlatformTelegram }

func (a *TelegramAdapter) Start(context.Context) error { return nil }

func (a *TelegramAdapter) Stop(context.Context) error { return nil }

func (a *TelegramAdapter) Health(ctx context.Context) (AdapterHealth, error) {
	if strings.TrimSpace(a.config.BotToken) == "" {
		return AdapterHealth{
			Status:    "misconfigured",
			AuthMode:  "bot_token",
			LastError: "telegram bot token is not configured",
		}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint("getMe"), nil)
	if err != nil {
		return AdapterHealth{}, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return AdapterHealth{
			Status:    "degraded",
			AuthMode:  "bot_token",
			LastError: err.Error(),
		}, nil
	}
	defer response.Body.Close()

	health := AdapterHealth{
		AuthMode:        "bot_token",
		LastConnectedAt: time.Now().UTC(),
		Capabilities:    []string{"inbound", "outbound", "health", "topics", "voice"},
		Metadata: map[string]any{
			"mode": firstNonEmpty(a.config.Mode, "polling"),
		},
	}
	if response.StatusCode >= 400 {
		health.Status = "degraded"
		health.LastError = fmt.Sprintf("telegram getMe returned %d", response.StatusCode)
		return health, nil
	}
	health.Status = "healthy"
	return health, nil
}

func (a *TelegramAdapter) NormalizeInbound(raw []byte) (InboundEvent, error) {
	var update telegramUpdate
	if err := json.Unmarshal(raw, &update); err != nil {
		return InboundEvent{}, fmt.Errorf("decode telegram update: %w", err)
	}
	message := update.Message
	if message.MessageID == 0 {
		message = update.EditedMessage
	}
	if message.MessageID == 0 {
		message = update.ChannelPost
	}
	if message.MessageID == 0 {
		return InboundEvent{}, fmt.Errorf("telegram update did not include a supported message payload")
	}

	text := firstNonEmpty(message.Text, message.Caption)
	content := []ContentPart{{Type: "input_text", Text: text}}
	attachments := make([]Attachment, 0, 2)
	if message.Voice.FileID != "" {
		attachments = append(attachments, Attachment{
			ID:       message.Voice.FileID,
			Name:     "voice.ogg",
			MIMEType: "audio/ogg",
			Metadata: map[string]any{"duration_seconds": message.Voice.Duration},
		})
	}
	if len(message.Photo) > 0 {
		largest := message.Photo[len(message.Photo)-1]
		attachments = append(attachments, Attachment{
			ID:       largest.FileID,
			Name:     "photo",
			MIMEType: "image/jpeg",
		})
	}

	chatID := strconv.FormatInt(message.Chat.ID, 10)
	userScope := ""
	if a.config.PrivateChatUserScope || strings.EqualFold(message.Chat.Type, "private") {
		userScope = strconv.FormatInt(message.From.ID, 10)
	}

	return InboundEvent{
		Platform:  PlatformTelegram,
		ProfileID: a.config.ProfileID,
		AdapterID: a.config.AdapterID,
		Actor: Actor{
			ID:          strconv.FormatInt(message.From.ID, 10),
			DisplayName: strings.TrimSpace(firstNonEmpty(message.From.FirstName+" "+message.From.LastName, message.From.Username)),
			Username:    message.From.Username,
			IsBot:       message.From.IsBot,
		},
		ChatID:      chatID,
		ThreadID:    telegramThreadID(message),
		UserScope:   userScope,
		Content:     content,
		Attachments: attachments,
		Metadata: map[string]any{
			"update_id":   update.UpdateID,
			"message_id":  message.MessageID,
			"chat_type":   message.Chat.Type,
			"has_caption": message.Caption != "",
		},
	}, nil
}

func (a *TelegramAdapter) SendMessage(ctx context.Context, target DeliveryTarget, message OutboundMessage) (DeliveryResult, error) {
	if strings.TrimSpace(a.config.BotToken) == "" {
		return DeliveryResult{}, fmt.Errorf("telegram bot token is not configured")
	}

	payload := map[string]any{
		"chat_id": target.ChatID,
		"text":    message.Text,
	}
	if threadID := strings.TrimSpace(target.ThreadID); threadID != "" && threadID != "default" {
		if parsed, err := strconv.ParseInt(threadID, 10, 64); err == nil {
			payload["message_thread_id"] = parsed
		}
	}
	if markdown := stringValue(message.Metadata, "parse_mode"); markdown != "" {
		payload["parse_mode"] = markdown
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return DeliveryResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return DeliveryResult{}, err
	}
	defer response.Body.Close()

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return DeliveryResult{}, fmt.Errorf("decode telegram send response: %w", err)
	}
	if response.StatusCode >= 400 || !envelope.OK {
		return DeliveryResult{}, fmt.Errorf("telegram sendMessage returned %d", response.StatusCode)
	}
	return DeliveryResult{
		MessageID: strconv.Itoa(envelope.Result.MessageID),
		Status:    "sent",
		Details: map[string]any{
			"chat_id": target.ChatID,
		},
	}, nil
}

func (a *TelegramAdapter) endpoint(method string) string {
	return strings.TrimRight(a.config.BaseURL, "/") + "/bot" + strings.TrimSpace(a.config.BotToken) + "/" + method
}

func telegramThreadID(message telegramMessage) string {
	if message.MessageThreadID != 0 {
		return strconv.FormatInt(message.MessageThreadID, 10)
	}
	return ""
}

type telegramUpdate struct {
	UpdateID      int64           `json:"update_id"`
	Message       telegramMessage `json:"message"`
	EditedMessage telegramMessage `json:"edited_message"`
	ChannelPost   telegramMessage `json:"channel_post"`
}

type telegramMessage struct {
	MessageID       int             `json:"message_id"`
	MessageThreadID int64           `json:"message_thread_id"`
	Text            string          `json:"text"`
	Caption         string          `json:"caption"`
	From            telegramUser    `json:"from"`
	Chat            telegramChat    `json:"chat"`
	Voice           telegramVoice   `json:"voice"`
	Photo           []telegramPhoto `json:"photo"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type telegramVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
}

type telegramPhoto struct {
	FileID string `json:"file_id"`
}
