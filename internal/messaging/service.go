package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	CollectionAdapters    = "platform_adapters"
	CollectionAdapterLogs = "platform_adapter_logs"

	PlatformTelegram = "telegram"
	PlatformDiscord  = "discord"
	PlatformWebhook  = "webhook"
	PlatformEmail    = "email"
)

var phaseCatalog = []PlatformDefinition{
	{Name: PlatformTelegram, Phase: 1, AuthPlaceholders: []string{"bot_token"}},
	{Name: PlatformDiscord, Phase: 1, AuthPlaceholders: []string{"bot_token"}},
	{Name: PlatformWebhook, Phase: 1, AuthPlaceholders: []string{"bearer_token", "hmac", "ip_allowlist"}},
	{Name: PlatformEmail, Phase: 1, AuthPlaceholders: []string{"imap_password", "smtp_password"}},
	{Name: "whatsapp", Phase: 2, AuthPlaceholders: []string{"bridge_auth"}},
	{Name: "signal", Phase: 2, AuthPlaceholders: []string{"bridge_auth"}},
	{Name: "matrix", Phase: 2, AuthPlaceholders: []string{"access_token"}},
	{Name: "mattermost", Phase: 2, AuthPlaceholders: []string{"bot_token"}},
	{Name: "sms", Phase: 2, AuthPlaceholders: []string{"signature"}},
	{Name: "dingtalk", Phase: 3, AuthPlaceholders: []string{"app_secret"}},
	{Name: "feishu", Phase: 3, AuthPlaceholders: []string{"app_secret"}},
	{Name: "wecom", Phase: 3, AuthPlaceholders: []string{"app_secret"}},
	{Name: "weixin", Phase: 3, AuthPlaceholders: []string{"token"}},
	{Name: "bluebubbles", Phase: 3, AuthPlaceholders: []string{"bridge_secret"}},
	{Name: "home_assistant", Phase: 3, AuthPlaceholders: []string{"access_token"}},
	{Name: "yuanbao", Phase: 3, AuthPlaceholders: []string{"bot_token"}},
	{Name: "qq_official_bot", Phase: 3, AuthPlaceholders: []string{"app_secret"}},
}

type PlatformDefinition struct {
	Name             string   `json:"name"`
	Phase            int      `json:"phase"`
	AuthPlaceholders []string `json:"auth_placeholders"`
}

type AdapterRecord struct {
	ID              string         `json:"id"`
	ProfileID       string         `json:"profile_id"`
	Platform        string         `json:"platform"`
	Enabled         bool           `json:"enabled"`
	Status          string         `json:"status"`
	AuthMode        string         `json:"auth_mode,omitempty"`
	Config          map[string]any `json:"config,omitempty"`
	Allowlist       []string       `json:"allowlist,omitempty"`
	Capabilities    []string       `json:"capabilities,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	LastConnectedAt time.Time      `json:"last_connected_at,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
}

type LogRecord struct {
	ID                string         `json:"id"`
	ProfileID         string         `json:"profile_id"`
	AdapterID         string         `json:"adapter_id"`
	Platform          string         `json:"platform"`
	Direction         string         `json:"direction"`
	Status            string         `json:"status"`
	SessionKey        string         `json:"session_key,omitempty"`
	ChatID            string         `json:"chat_id,omitempty"`
	ThreadID          string         `json:"thread_id,omitempty"`
	ExternalMessageID string         `json:"external_message_id,omitempty"`
	Summary           string         `json:"summary,omitempty"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	Payload           map[string]any `json:"payload,omitempty"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
}

type LogInput struct {
	ProfileID         string
	AdapterID         string
	Platform          string
	Direction         string
	Status            string
	SessionKey        string
	ChatID            string
	ThreadID          string
	ExternalMessageID string
	Summary           string
	ErrorMessage      string
	Payload           map[string]any
}

type UpsertAdapterInput struct {
	ProfileID    string
	Platform     string
	Enabled      bool
	Status       string
	AuthMode     string
	Config       map[string]any
	Allowlist    []string
	Capabilities []string
	Metadata     map[string]any
}

type Actor struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Username    string         `json:"username,omitempty"`
	IsBot       bool           `json:"is_bot,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type Attachment struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	MIMEType string         `json:"mime_type,omitempty"`
	URL      string         `json:"url,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type InboundEvent struct {
	Platform    string         `json:"platform"`
	ProfileID   string         `json:"profile_id"`
	AdapterID   string         `json:"adapter_id,omitempty"`
	Actor       Actor          `json:"actor"`
	ChatID      string         `json:"chat_id"`
	ThreadID    string         `json:"thread_id,omitempty"`
	UserScope   string         `json:"user_scope,omitempty"`
	Content     []ContentPart  `json:"content"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DeliveryTarget struct {
	AdapterID  string         `json:"adapter_id,omitempty"`
	Platform   string         `json:"platform,omitempty"`
	ProfileID  string         `json:"profile_id"`
	ChatID     string         `json:"chat_id"`
	ThreadID   string         `json:"thread_id,omitempty"`
	UserScope  string         `json:"user_scope,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	SessionKey string         `json:"session_key,omitempty"`
}

type OutboundMessage struct {
	Text        string         `json:"text"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DeliveryResult struct {
	MessageID string         `json:"message_id,omitempty"`
	Status    string         `json:"status"`
	Details   map[string]any `json:"details,omitempty"`
}

type AdapterHealth struct {
	Status          string         `json:"status"`
	AuthMode        string         `json:"auth_mode,omitempty"`
	LastConnectedAt time.Time      `json:"last_connected_at,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	Capabilities    []string       `json:"capabilities,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type AuthConfig struct {
	Mode      string
	Allowlist []string
}

type Adapter interface {
	Platform() string
	Start(context.Context) error
	Stop(context.Context) error
	Health(context.Context) (AdapterHealth, error)
	SendMessage(context.Context, DeliveryTarget, OutboundMessage) (DeliveryResult, error)
}

type Gateway struct {
	app      core.App
	sessions *sessions.Service
	adapters map[string]Adapter
}

func NewGateway(app core.App, sessionService *sessions.Service) *Gateway {
	return &Gateway{
		app:      app,
		sessions: sessionService,
		adapters: map[string]Adapter{},
	}
}

func (g *Gateway) Name() string {
	return "messaging-gateway"
}

func (g *Gateway) Start(context.Context) error {
	return nil
}

func (g *Gateway) Stop(context.Context) error {
	return nil
}

func (g *Gateway) Register(adapter Adapter) {
	if adapter == nil {
		return
	}
	g.adapters[adapter.Platform()] = adapter
}

func (g *Gateway) Adapter(platform string) (Adapter, bool) {
	adapter, ok := g.adapters[strings.TrimSpace(platform)]
	return adapter, ok
}

func (g *Gateway) PlatformCatalog() []PlatformDefinition {
	items := make([]PlatformDefinition, len(phaseCatalog))
	copy(items, phaseCatalog)
	return items
}

func (g *Gateway) EnsurePhaseOneAdapters(ctx context.Context, profileID string) error {
	for _, definition := range phaseCatalog {
		if definition.Phase != 1 {
			continue
		}
		if _, err := g.UpsertAdapter(ctx, UpsertAdapterInput{
			ProfileID: profileID,
			Platform:  definition.Name,
			Enabled:   false,
			Status:    "not_configured",
			AuthMode:  firstNonEmpty(definition.AuthPlaceholders...),
			Metadata: map[string]any{
				"phase":             definition.Phase,
				"auth_placeholders": definition.AuthPlaceholders,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) SessionKey(event InboundEvent) string {
	thread := strings.TrimSpace(event.ThreadID)
	if thread == "" {
		thread = "default"
	}
	parts := []string{
		strings.TrimSpace(event.Platform),
		strings.TrimSpace(event.ProfileID),
		strings.TrimSpace(event.ChatID),
		thread,
	}
	if scope := strings.TrimSpace(event.UserScope); scope != "" {
		parts = append(parts, scope)
	}
	return strings.Join(parts, ":")
}

func (g *Gateway) Authorize(_ context.Context, cfg AuthConfig, event InboundEvent) error {
	mode := strings.TrimSpace(cfg.Mode)
	switch mode {
	case "", "allow_all", "global_allow_all", "per_platform_allow_all":
		return nil
	case "allowlist", "global_allowlist", "per_platform_allowlist":
		for _, item := range cfg.Allowlist {
			if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(event.Actor.ID)) ||
				strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(event.Actor.Username)) {
				return nil
			}
		}
		return fmt.Errorf("actor %q is not on the allowlist", event.Actor.ID)
	case "invite_code", "dm_pairing":
		if code := strings.TrimSpace(stringValue(event.Metadata, "invite_code")); code != "" {
			return nil
		}
		return errors.New("invite code or pairing metadata is required")
	default:
		return fmt.Errorf("unsupported auth mode %q", cfg.Mode)
	}
}

func (g *Gateway) ResolveSession(ctx context.Context, event InboundEvent) (sessions.Session, error) {
	if g.sessions == nil {
		return sessions.Session{}, errors.New("session service is unavailable")
	}
	key := g.SessionKey(event)
	session, err := g.sessions.FindSessionByKey(ctx, event.ProfileID, key)
	if err == nil {
		return session, nil
	}
	if !strings.Contains(err.Error(), "find session by key") {
		return sessions.Session{}, err
	}

	title := firstNonEmpty(firstText(event.Content), key)
	return g.sessions.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID:  event.ProfileID,
		Source:     "gateway." + event.Platform,
		SessionKey: key,
		Title:      summarizeTitle(title),
		Status:     "active",
		Metadata: map[string]any{
			"platform":     event.Platform,
			"adapter_id":   event.AdapterID,
			"chat_id":      event.ChatID,
			"thread_id":    event.ThreadID,
			"user_scope":   event.UserScope,
			"actor":        event.Actor,
			"attachments":  event.Attachments,
			"raw_metadata": event.Metadata,
		},
	})
}

func (g *Gateway) Ingest(ctx context.Context, event InboundEvent) (sessions.Session, error) {
	session, err := g.ResolveSession(ctx, event)
	if err != nil {
		return sessions.Session{}, err
	}
	_, _ = g.AppendLog(context.Background(), LogInput{
		ProfileID:  event.ProfileID,
		AdapterID:  event.AdapterID,
		Platform:   event.Platform,
		Direction:  "inbound",
		Status:     "received",
		SessionKey: g.SessionKey(event),
		ChatID:     event.ChatID,
		ThreadID:   event.ThreadID,
		Summary:    summarizeTitle(firstText(event.Content)),
		Payload: map[string]any{
			"actor":       event.Actor,
			"content":     event.Content,
			"attachments": event.Attachments,
			"metadata":    event.Metadata,
		},
	})
	return session, nil
}

func (g *Gateway) UpsertAdapter(ctx context.Context, input UpsertAdapterInput) (AdapterRecord, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return AdapterRecord{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.Platform) == "" {
		return AdapterRecord{}, errors.New("platform is required")
	}
	record, err := g.findAdapterRecord(input.ProfileID, input.Platform)
	if err != nil {
		record, err = g.newRecord(CollectionAdapters)
		if err != nil {
			return AdapterRecord{}, err
		}
		record.Set("profile_id", input.ProfileID)
		record.Set("platform", input.Platform)
	}
	record.Set("enabled", input.Enabled)
	record.Set("status", firstNonEmpty(input.Status, "configured"))
	record.Set("auth_mode", input.AuthMode)
	if err := setJSON(record, "config_json", input.Config); err != nil {
		return AdapterRecord{}, err
	}
	if err := setJSON(record, "allowlist_json", input.Allowlist); err != nil {
		return AdapterRecord{}, err
	}
	if err := setJSON(record, "capabilities_json", input.Capabilities); err != nil {
		return AdapterRecord{}, err
	}
	if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
		return AdapterRecord{}, err
	}
	if err := g.app.SaveWithContext(ctx, record); err != nil {
		return AdapterRecord{}, fmt.Errorf("save adapter: %w", err)
	}
	return adapterFromRecord(record)
}

func (g *Gateway) ListAdapters(ctx context.Context, profileID string) ([]AdapterRecord, error) {
	records, err := g.app.FindRecordsByFilter(
		CollectionAdapters,
		"profile_id = {:profile_id}",
		"platform",
		0,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list adapters: %w", err)
	}
	items := make([]AdapterRecord, 0, len(records))
	for _, record := range records {
		item, err := adapterFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (g *Gateway) GetAdapter(ctx context.Context, adapterID string) (AdapterRecord, error) {
	record, err := g.app.FindRecordById(CollectionAdapters, adapterID)
	if err != nil {
		return AdapterRecord{}, fmt.Errorf("find adapter: %w", err)
	}
	_ = ctx
	return adapterFromRecord(record)
}

func (g *Gateway) UpdateHealth(ctx context.Context, adapterID string, health AdapterHealth) (AdapterRecord, error) {
	record, err := g.app.FindRecordById(CollectionAdapters, adapterID)
	if err != nil {
		return AdapterRecord{}, fmt.Errorf("find adapter: %w", err)
	}
	record.Set("status", firstNonEmpty(health.Status, "unknown"))
	record.Set("auth_mode", health.AuthMode)
	record.Set("last_error", health.LastError)
	if !health.LastConnectedAt.IsZero() {
		dt, err := types.ParseDateTime(health.LastConnectedAt)
		if err != nil {
			return AdapterRecord{}, fmt.Errorf("parse last_connected_at: %w", err)
		}
		record.Set("last_connected_at", dt)
	}
	if err := setJSON(record, "capabilities_json", health.Capabilities); err != nil {
		return AdapterRecord{}, err
	}
	if err := setJSON(record, "metadata_json", health.Metadata); err != nil {
		return AdapterRecord{}, err
	}
	if err := g.app.SaveWithContext(ctx, record); err != nil {
		return AdapterRecord{}, fmt.Errorf("update adapter health: %w", err)
	}
	return adapterFromRecord(record)
}

func (g *Gateway) Send(ctx context.Context, target DeliveryTarget, message OutboundMessage) (DeliveryResult, error) {
	platform := strings.TrimSpace(target.Platform)
	if platform == "" && target.AdapterID != "" {
		record, err := g.app.FindRecordById(CollectionAdapters, target.AdapterID)
		if err != nil {
			return DeliveryResult{}, fmt.Errorf("find adapter: %w", err)
		}
		platform = record.GetString("platform")
	}
	adapter, ok := g.adapters[platform]
	if !ok {
		return DeliveryResult{}, fmt.Errorf("no adapter registered for platform %q", platform)
	}
	result, err := adapter.SendMessage(ctx, target, message)
	logStatus := result.Status
	logError := ""
	if err != nil {
		logStatus = "failed"
		logError = err.Error()
	}
	_, _ = g.AppendLog(context.Background(), LogInput{
		ProfileID:         target.ProfileID,
		AdapterID:         target.AdapterID,
		Platform:          platform,
		Direction:         "outbound",
		Status:            firstNonEmpty(logStatus, "sent"),
		SessionKey:        firstNonEmpty(target.SessionKey, g.targetSessionKey(target)),
		ChatID:            target.ChatID,
		ThreadID:          target.ThreadID,
		ExternalMessageID: result.MessageID,
		Summary:           summarizeTitle(message.Text),
		ErrorMessage:      logError,
		Payload: map[string]any{
			"message": message,
			"result":  result,
		},
	})
	return result, err
}

func (g *Gateway) AppendLog(ctx context.Context, input LogInput) (LogRecord, error) {
	record, err := g.newRecord(CollectionAdapterLogs)
	if err != nil {
		return LogRecord{}, err
	}
	record.Set("profile_id", input.ProfileID)
	record.Set("adapter_id", input.AdapterID)
	record.Set("platform", input.Platform)
	record.Set("direction", input.Direction)
	record.Set("status", input.Status)
	record.Set("session_key", input.SessionKey)
	record.Set("chat_id", input.ChatID)
	record.Set("thread_id", input.ThreadID)
	record.Set("external_message_id", input.ExternalMessageID)
	record.Set("summary", input.Summary)
	record.Set("error_message", input.ErrorMessage)
	if err := setJSON(record, "payload_json", input.Payload); err != nil {
		return LogRecord{}, err
	}
	if err := g.app.SaveWithContext(ctx, record); err != nil {
		return LogRecord{}, fmt.Errorf("save adapter log: %w", err)
	}
	return logFromRecord(record)
}

func (g *Gateway) ListLogs(ctx context.Context, profileID, platform string, limit int) ([]LogRecord, error) {
	filter := "profile_id = {:profile_id}"
	params := dbx.Params{"profile_id": profileID}
	if trimmed := strings.TrimSpace(platform); trimmed != "" {
		filter += " && platform = {:platform}"
		params["platform"] = trimmed
	}
	records, err := g.app.FindRecordsByFilter(CollectionAdapterLogs, filter, "", limit, 0, params)
	if err != nil {
		return nil, fmt.Errorf("list adapter logs: %w", err)
	}
	items := make([]LogRecord, 0, len(records))
	for _, record := range records {
		item, err := logFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (g *Gateway) newRecord(collection string) (*core.Record, error) {
	col, err := g.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

func (g *Gateway) findAdapterRecord(profileID, platform string) (*core.Record, error) {
	return g.app.FindFirstRecordByFilter(
		CollectionAdapters,
		"profile_id = {:profile_id} && platform = {:platform}",
		dbx.Params{"profile_id": profileID, "platform": platform},
	)
}

func (g *Gateway) targetSessionKey(target DeliveryTarget) string {
	thread := strings.TrimSpace(target.ThreadID)
	if thread == "" {
		thread = "default"
	}
	parts := []string{target.Platform, target.ProfileID, target.ChatID, thread}
	if scope := strings.TrimSpace(target.UserScope); scope != "" {
		parts = append(parts, scope)
	}
	return strings.Join(parts, ":")
}

func adapterFromRecord(record *core.Record) (AdapterRecord, error) {
	item := AdapterRecord{
		ID:              record.Id,
		ProfileID:       record.GetString("profile_id"),
		Platform:        record.GetString("platform"),
		Enabled:         record.GetBool("enabled"),
		Status:          record.GetString("status"),
		AuthMode:        record.GetString("auth_mode"),
		LastConnectedAt: record.GetDateTime("last_connected_at").Time(),
		LastError:       record.GetString("last_error"),
		CreatedAt:       record.GetDateTime("created").Time(),
		UpdatedAt:       record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "config_json", &item.Config); err != nil {
		return AdapterRecord{}, err
	}
	if err := decodeJSONField(record, "allowlist_json", &item.Allowlist); err != nil {
		return AdapterRecord{}, err
	}
	if err := decodeJSONField(record, "capabilities_json", &item.Capabilities); err != nil {
		return AdapterRecord{}, err
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return AdapterRecord{}, err
	}
	return item, nil
}

func logFromRecord(record *core.Record) (LogRecord, error) {
	item := LogRecord{
		ID:                record.Id,
		ProfileID:         record.GetString("profile_id"),
		AdapterID:         record.GetString("adapter_id"),
		Platform:          record.GetString("platform"),
		Direction:         record.GetString("direction"),
		Status:            record.GetString("status"),
		SessionKey:        record.GetString("session_key"),
		ChatID:            record.GetString("chat_id"),
		ThreadID:          record.GetString("thread_id"),
		ExternalMessageID: record.GetString("external_message_id"),
		Summary:           record.GetString("summary"),
		ErrorMessage:      record.GetString("error_message"),
		CreatedAt:         record.GetDateTime("created").Time(),
	}
	if err := decodeJSONField(record, "payload_json", &item.Payload); err != nil {
		return LogRecord{}, err
	}
	return item, nil
}

func setJSON(record *core.Record, field string, value any) error {
	if value == nil {
		record.Set(field, nil)
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", field, err)
	}
	record.Set(field, string(raw))
	return nil
}

func decodeJSONField(record *core.Record, field string, target any) error {
	raw := record.GetString(field)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}

func firstText(parts []ContentPart) string {
	for _, part := range parts {
		if text := strings.TrimSpace(part.Text); text != "" {
			return text
		}
	}
	return ""
}

func summarizeTitle(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 60 {
		return text
	}
	return strings.TrimSpace(text[:60]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringValue(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key].(string)
	return value
}
