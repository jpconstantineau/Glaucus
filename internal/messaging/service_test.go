package messaging

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
)

func TestGatewaySessionKeyAuthorizeAndResolveSession(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	sessionService := sessions.NewService(app)
	gateway := NewGateway(app, sessionService)
	event := InboundEvent{
		Platform:  PlatformTelegram,
		ProfileID: "default",
		ChatID:    "chat-1",
		ThreadID:  "thread-2",
		UserScope: "user-3",
		Actor: Actor{
			ID:       "actor-1",
			Username: "pierre",
		},
		Content: []ContentPart{{Type: "input_text", Text: "hello from telegram"}},
	}

	if key := gateway.SessionKey(event); key != "telegram:default:chat-1:thread-2:user-3" {
		t.Fatalf("unexpected session key %q", key)
	}
	if err := gateway.Authorize(context.Background(), AuthConfig{Mode: "allowlist", Allowlist: []string{"pierre"}}, event); err != nil {
		t.Fatalf("authorize allowlist: %v", err)
	}
	if err := gateway.Authorize(context.Background(), AuthConfig{Mode: "invite_code"}, event); err == nil {
		t.Fatal("expected invite-code auth to require metadata")
	}

	session, err := gateway.ResolveSession(context.Background(), event)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if session.SessionKey != gateway.SessionKey(event) {
		t.Fatalf("expected session key to round-trip, got %q", session.SessionKey)
	}

	same, err := gateway.ResolveSession(context.Background(), event)
	if err != nil {
		t.Fatalf("resolve existing session: %v", err)
	}
	if same.ID != session.ID {
		t.Fatalf("expected same session to be reused, got %q want %q", same.ID, session.ID)
	}
}

func TestGatewayUpsertAndLogAdapterState(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	gateway := NewGateway(app, sessions.NewService(app))
	adapter, err := gateway.UpsertAdapter(context.Background(), UpsertAdapterInput{
		ProfileID:    "default",
		Platform:     PlatformWebhook,
		Enabled:      true,
		Status:       "configured",
		AuthMode:     "hmac",
		Allowlist:    []string{"10.0.0.1"},
		Capabilities: []string{"inbound", "outbound", "health"},
		Metadata:     map[string]any{"phase": 1},
	})
	if err != nil {
		t.Fatalf("upsert adapter: %v", err)
	}
	if adapter.AuthMode != "hmac" {
		t.Fatalf("expected auth mode to persist, got %q", adapter.AuthMode)
	}

	if _, err := gateway.UpdateHealth(context.Background(), adapter.ID, AdapterHealth{
		Status:       "healthy",
		AuthMode:     "hmac",
		Capabilities: []string{"inbound", "outbound", "health"},
	}); err != nil {
		t.Fatalf("update health: %v", err)
	}

	if _, err := gateway.AppendLog(context.Background(), LogInput{
		ProfileID:  "default",
		AdapterID:  adapter.ID,
		Platform:   PlatformWebhook,
		Direction:  "outbound",
		Status:     "sent",
		SessionKey: "webhook:default:chat:default:user",
		Summary:    "test delivery",
	}); err != nil {
		t.Fatalf("append log: %v", err)
	}

	items, err := gateway.ListAdapters(context.Background(), "default")
	if err != nil {
		t.Fatalf("list adapters: %v", err)
	}
	if len(items) != 1 || items[0].Platform != PlatformWebhook {
		t.Fatalf("unexpected adapter list: %+v", items)
	}

	logs, err := gateway.ListLogs(context.Background(), "default", PlatformWebhook, 10)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Status != "sent" {
		t.Fatalf("unexpected logs: %+v", logs)
	}
}
