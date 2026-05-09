package batch

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateBatchJobAndTrajectoryExport(t *testing.T) {
	app := newTestApp(t)
	sessionService := sessions.NewService(app)
	eventService := runtime.NewEventService(app)
	service := NewService(app, sessionService, eventService)
	service.now = func() time.Time {
		return time.Date(2026, 5, 8, 10, 30, 0, 0, time.UTC)
	}

	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	job, attempts, err := service.CreateJob(context.Background(), CreateJobInput{
		ProfileID:        "default",
		Name:             "Slice 10 verification",
		ProviderID:       "test-provider",
		ModelID:          "test-model",
		Toolset:          "safe",
		WorkingDirectory: activeProfile.Root,
		CreatedBy:        "test",
		Items: []Item{
			{Prompt: "Summarize the first prompt", Metadata: map[string]any{"case": "one"}},
			{Prompt: "Summarize the second prompt", Metadata: map[string]any{"case": "two"}},
		},
		Metadata: map[string]any{"source": "unit-test"},
	})
	if err != nil {
		t.Fatalf("create batch job: %v", err)
	}
	if job.ID == "" || len(attempts) != 2 {
		t.Fatalf("expected durable job with attempts, got job=%+v attempts=%d", job, len(attempts))
	}

	session, err := sessionService.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "batch",
		Title:     "Attempt 1",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	run, err := sessionService.CreateRun(context.Background(), sessions.CreateRunInput{
		ProfileID:     "default",
		SessionID:     session.ID,
		TriggerSource: "batch",
		Status:        runtime.RunStatusCompleted,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := sessionService.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   "default",
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: attempts[0].Prompt}},
		VisibleText: attempts[0].Prompt,
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	if _, err := sessionService.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   "default",
		SessionID:   session.ID,
		RunID:       run.ID,
		Role:        "assistant",
		Content:     sessions.MessageContent{{Type: "output_text", Text: "First output"}},
		VisibleText: "First output",
		Usage:       map[string]any{"total_tokens": 17},
	}); err != nil {
		t.Fatalf("create assistant message: %v", err)
	}
	if _, err := eventService.Append(context.Background(), runtime.AppendEventInput{
		ProfileID:  "default",
		RunID:      run.ID,
		SessionID:  session.ID,
		Type:       "run.completed",
		Payload:    map[string]any{"status": "completed"},
		IsTerminal: true,
	}); err != nil {
		t.Fatalf("append run event: %v", err)
	}

	if _, err := service.UpdateAttempt(context.Background(), attempts[0].ID, UpdateAttemptInput{
		Status:     AttemptStatusCompleted,
		SessionID:  session.ID,
		RunID:      run.ID,
		OutputText: "First output",
		Usage:      map[string]any{"total_tokens": 17},
		StartedAt:  service.now(),
		EndedAt:    service.now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("update first attempt: %v", err)
	}
	if _, err := service.UpdateAttempt(context.Background(), attempts[1].ID, UpdateAttemptInput{
		Status:       AttemptStatusFailed,
		ErrorMessage: "provider timeout",
		StartedAt:    service.now().Add(2 * time.Minute),
		EndedAt:      service.now().Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("update second attempt: %v", err)
	}

	job, err = service.RecomputeJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("recompute job: %v", err)
	}
	if job.Status != JobStatusPartial || job.CompletedCount != 1 || job.FailedCount != 1 {
		t.Fatalf("unexpected job counters after recompute: %+v", job)
	}

	rows, err := service.BuildTrajectoryRows(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("build trajectory rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 trajectory rows, got %d", len(rows))
	}
	if rows[0].SchemaVersion != SchemaVersion || rows[0].Prompt != "Summarize the first prompt" {
		t.Fatalf("unexpected first trajectory row: %+v", rows[0])
	}
	if len(rows[0].Turns) != 2 || len(rows[0].Events) != 1 {
		t.Fatalf("expected first row to include turns and events, got %+v", rows[0])
	}
	if rows[1].ErrorMessage != "provider timeout" || rows[1].Status != AttemptStatusFailed {
		t.Fatalf("unexpected failed trajectory row: %+v", rows[1])
	}

	bundle, err := service.WriteTrajectoryExport(context.Background(), job.ID, activeProfile.Root)
	if err != nil {
		t.Fatalf("write trajectory export: %v", err)
	}
	if _, err := os.Stat(bundle.ManifestPath); err != nil {
		t.Fatalf("expected manifest path, got err=%v", err)
	}
	if _, err := os.Stat(bundle.TrajectoryPath); err != nil {
		t.Fatalf("expected trajectory path, got err=%v", err)
	}

	manifestBody, err := os.ReadFile(bundle.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest ExportManifest
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.SchemaVersion != SchemaVersion || manifest.AttemptCount != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	file, err := os.Open(bundle.TrajectoryPath)
	if err != nil {
		t.Fatalf("open trajectory export: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan trajectory export: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 trajectory rows in export, got %d", count)
	}

	updatedJob, err := service.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	expectedExportPath := filepath.ToSlash(filepath.Join("exports", "batches", job.ID, "trajectory.jsonl"))
	if updatedJob.ExportPath != expectedExportPath {
		t.Fatalf("expected export path %q, got %q", expectedExportPath, updatedJob.ExportPath)
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
