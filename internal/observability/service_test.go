package observability

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
)

func TestSnapshotAggregatesRunsAndUsage(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	service := sessions.NewService(app)
	session, err := service.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "test",
		Title:     "snapshot",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	run, err := service.CreateRun(context.Background(), sessions.CreateRunInput{
		ProfileID:     "default",
		SessionID:     session.ID,
		TriggerSource: "test",
		Status:        "completed",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := service.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   "default",
		SessionID:   session.ID,
		RunID:       run.ID,
		Role:        "assistant",
		Content:     sessions.MessageContent{{Type: "output_text", Text: "hello"}},
		VisibleText: "hello",
		Usage: map[string]any{
			"input_tokens":  3,
			"output_tokens": 5,
			"total_tokens":  8,
		},
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	obs := NewService(app, BuildInfo{AppName: "Glaucus", Version: "dev", Commit: "local", BuiltAt: "now"})
	snapshot, err := obs.Snapshot(context.Background(), "default")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.CompletedRuns != 1 || snapshot.TotalTokens != 8 || snapshot.InputTokens != 3 || snapshot.OutputTokens != 5 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if text := obs.Prometheus(snapshot); text == "" || text[0:18] != "glaucus_build_info" {
		t.Fatalf("unexpected prometheus output: %q", text)
	}
}
