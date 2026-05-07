package sessions

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateResumeAndListSessionData(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	ctx := context.Background()

	session, err := service.CreateSession(ctx, CreateSessionInput{
		ProfileID: "profile_default",
		Source:    "web",
		Title:     "First session",
		Status:    "active",
		ModelSnapshot: map[string]any{
			"provider": "openrouter",
			"model":    "openai/gpt-4.1-mini",
		},
		ToolsetSnapshot: map[string]any{
			"name": "safe-default",
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if session.ID == "" {
		t.Fatal("expected session id")
	}

	userMessage, err := service.CreateMessage(ctx, CreateMessageInput{
		ProfileID: session.ProfileID,
		SessionID: session.ID,
		Role:      "user",
		Content: MessageContent{
			{Type: "input_text", Text: "Hello from the browser"},
		},
		VisibleText: "Hello from the browser",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	if userMessage.Ordinal != 1 {
		t.Fatalf("expected first message ordinal to be 1, got %d", userMessage.Ordinal)
	}

	run, err := service.CreateRun(ctx, CreateRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		Status:        "queued",
		Request: map[string]any{
			"message_id": userMessage.ID,
		},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	assistantMessage, err := service.CreateMessage(ctx, CreateMessageInput{
		ProfileID:   session.ProfileID,
		SessionID:   session.ID,
		RunID:       run.ID,
		Role:        "assistant",
		Content:     MessageContent{{Type: "output_text", Text: "Hi there"}},
		VisibleText: "Hi there",
		Usage: map[string]any{
			"output_tokens": 2,
		},
	})
	if err != nil {
		t.Fatalf("create assistant message: %v", err)
	}

	if assistantMessage.Ordinal != 2 {
		t.Fatalf("expected second message ordinal to be 2, got %d", assistantMessage.Ordinal)
	}

	resumedSession, err := service.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("resume session: %v", err)
	}

	if resumedSession.ModelSnapshot["provider"] != "openrouter" {
		t.Fatalf("expected provider snapshot to round-trip, got %#v", resumedSession.ModelSnapshot)
	}
	if resumedSession.LastMessageAt.IsZero() {
		t.Fatal("expected last_message_at to be updated")
	}

	sessions, err := service.ListSessions(ctx, session.ProfileID, 20)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	messages, err := service.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].VisibleText != "Hello from the browser" || messages[1].RunID != run.ID {
		t.Fatalf("unexpected message lineage: %#v", messages)
	}

	runs, err := service.ListRuns(ctx, session.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Request["message_id"] != userMessage.ID {
		t.Fatalf("expected run request to reference user message, got %#v", runs[0].Request)
	}
}

func newTestApp(t *testing.T) core.App {
	t.Helper()

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

	return app
}
