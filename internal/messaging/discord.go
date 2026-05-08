package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type DiscordConfig struct {
	ProfileID      string
	AdapterID      string
	BotToken       string
	BaseURL        string
	ApplicationID  string
	RequireMention bool
	AllowRoleIDs   []string
}

type DiscordAdapter struct {
	config DiscordConfig
	client *http.Client
}

func NewDiscordAdapter(cfg DiscordConfig, client *http.Client) *DiscordAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://discord.com/api/v10"
	}
	return &DiscordAdapter{config: cfg, client: client}
}

func (a *DiscordAdapter) Platform() string { return PlatformDiscord }

func (a *DiscordAdapter) Start(context.Context) error { return nil }

func (a *DiscordAdapter) Stop(context.Context) error { return nil }

func (a *DiscordAdapter) Health(ctx context.Context) (AdapterHealth, error) {
	if strings.TrimSpace(a.config.BotToken) == "" {
		return AdapterHealth{
			Status:    "misconfigured",
			AuthMode:  "bot_token",
			LastError: "discord bot token is not configured",
		}, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.config.BaseURL, "/")+"/users/@me", nil)
	if err != nil {
		return AdapterHealth{}, err
	}
	request.Header.Set("Authorization", "Bot "+a.config.BotToken)
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
		Capabilities:    []string{"inbound", "outbound", "health", "threads", "guilds", "dms"},
		Metadata: map[string]any{
			"require_mention": a.config.RequireMention,
			"role_allowlist":  a.config.AllowRoleIDs,
		},
	}
	if response.StatusCode >= 400 {
		health.Status = "degraded"
		health.LastError = fmt.Sprintf("discord /users/@me returned %d", response.StatusCode)
		return health, nil
	}
	health.Status = "healthy"
	return health, nil
}

func (a *DiscordAdapter) NormalizeInbound(raw []byte) (InboundEvent, error) {
	var message discordMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return InboundEvent{}, fmt.Errorf("decode discord message: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.ChannelID) == "" {
		return InboundEvent{}, fmt.Errorf("discord payload did not include message and channel identifiers")
	}

	userScope := ""
	if strings.TrimSpace(message.GuildID) == "" {
		userScope = strings.TrimSpace(message.Author.ID)
	}

	attachments := make([]Attachment, 0, len(message.Attachments))
	for _, item := range message.Attachments {
		attachments = append(attachments, Attachment{
			ID:       item.ID,
			Name:     item.Filename,
			MIMEType: item.ContentType,
			URL:      item.URL,
		})
	}

	return InboundEvent{
		Platform:  PlatformDiscord,
		ProfileID: a.config.ProfileID,
		AdapterID: a.config.AdapterID,
		Actor: Actor{
			ID:          message.Author.ID,
			DisplayName: firstNonEmpty(message.Member.Nick, message.Author.GlobalName, message.Author.Username),
			Username:    message.Author.Username,
			IsBot:       message.Author.Bot,
			Metadata: map[string]any{
				"role_ids": message.Member.Roles,
			},
		},
		ChatID:    message.ChannelID,
		ThreadID:  firstNonEmpty(message.ThreadID, message.MessageReference.MessageID),
		UserScope: userScope,
		Content: []ContentPart{{
			Type: "input_text",
			Text: message.Content,
		}},
		Attachments: attachments,
		Metadata: map[string]any{
			"message_id":       message.ID,
			"guild_id":         message.GuildID,
			"mentioned":        a.isMentioned(message),
			"require_mention":  a.config.RequireMention,
			"message_type":     message.Type,
			"message_role_ids": message.Member.Roles,
		},
	}, nil
}

func (a *DiscordAdapter) SendMessage(ctx context.Context, target DeliveryTarget, message OutboundMessage) (DeliveryResult, error) {
	if strings.TrimSpace(a.config.BotToken) == "" {
		return DeliveryResult{}, fmt.Errorf("discord bot token is not configured")
	}

	body, err := json.Marshal(map[string]any{
		"content": message.Text,
	})
	if err != nil {
		return DeliveryResult{}, err
	}

	endpoint := strings.TrimRight(a.config.BaseURL, "/") + "/channels/" + target.ChatID + "/messages"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{}, err
	}
	request.Header.Set("Authorization", "Bot "+a.config.BotToken)
	request.Header.Set("Content-Type", "application/json")
	if threadID := strings.TrimSpace(target.ThreadID); threadID != "" && threadID != "default" {
		query := request.URL.Query()
		query.Set("thread_id", threadID)
		request.URL.RawQuery = query.Encode()
	}
	response, err := a.client.Do(request)
	if err != nil {
		return DeliveryResult{}, err
	}
	defer response.Body.Close()

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return DeliveryResult{}, fmt.Errorf("decode discord send response: %w", err)
	}
	if response.StatusCode >= 400 {
		return DeliveryResult{}, fmt.Errorf("discord message send returned %d", response.StatusCode)
	}
	return DeliveryResult{
		MessageID: payload.ID,
		Status:    "sent",
		Details: map[string]any{
			"channel_id": target.ChatID,
		},
	}, nil
}

func (a *DiscordAdapter) isMentioned(message discordMessage) bool {
	if !a.config.RequireMention {
		return true
	}
	for _, mention := range message.Mentions {
		if mention.ID == a.config.ApplicationID || mention.Username == a.config.ApplicationID {
			return true
		}
	}
	return false
}

type discordMessage struct {
	ID               string                 `json:"id"`
	ChannelID        string                 `json:"channel_id"`
	GuildID          string                 `json:"guild_id"`
	Content          string                 `json:"content"`
	ThreadID         string                 `json:"thread_id"`
	Type             int                    `json:"type"`
	Author           discordUser            `json:"author"`
	Member           discordMember          `json:"member"`
	Attachments      []discordAttachment    `json:"attachments"`
	Mentions         []discordUser          `json:"mentions"`
	MessageReference discordMessageRef      `json:"message_reference"`
	Extra            map[string]interface{} `json:"-"`
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

type discordMember struct {
	Nick  string   `json:"nick"`
	Roles []string `json:"roles"`
}

type discordAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

type discordMessageRef struct {
	MessageID string `json:"message_id"`
}
