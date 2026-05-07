package search

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
)

func TestSearchSessionsUsesFTSIndex(t *testing.T) {
	app := newTestApp(t)
	sessionService := sessions.NewService(app)
	searchService := NewService(app, sessionService)

	session, err := sessionService.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "web",
		Title:     "Roadmap planning",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sessionService.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   "default",
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: "please summarize the roadmap slice work"}},
		VisibleText: "please summarize the roadmap slice work",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	results, err := searchService.SearchSessions(context.Background(), "default", "roadmap", 10)
	if err != nil {
		t.Fatalf("search sessions: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
	if results[0].SessionID != session.ID {
		t.Fatalf("expected matching session id, got %+v", results[0])
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
