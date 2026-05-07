package jobs

import (
	"context"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreatePauseResumeAndTrackJobHistory(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	service.now = func() time.Time {
		return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	}

	job, err := service.CreateJob(context.Background(), CreateJobInput{
		ProfileID:     "default",
		Name:          "Daily summary",
		Prompt:        "Summarize the session",
		ScheduleKind:  "interval",
		ScheduleValue: "30m",
		Timezone:      "UTC",
		Enabled:       true,
		CWD:           "profiles/default",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.ID == "" || job.NextRunAt.IsZero() {
		t.Fatalf("expected durable job with next run, got %+v", job)
	}

	paused, err := service.PauseJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("pause job: %v", err)
	}
	if paused.Enabled {
		t.Fatal("expected paused job to be disabled")
	}

	resumed, err := service.ResumeJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("resume job: %v", err)
	}
	if !resumed.Enabled {
		t.Fatal("expected resumed job to be enabled")
	}

	history, err := service.RecordRun(context.Background(), RecordRunInput{
		ProfileID:    job.ProfileID,
		JobID:        job.ID,
		Status:       JobStatusQueued,
		ScheduledFor: service.now(),
	})
	if err != nil {
		t.Fatalf("record job run: %v", err)
	}

	updatedHistory, err := service.UpdateRun(context.Background(), history.ID, UpdateRunInput{
		RunID:         "run_123",
		Status:        JobStatusCompleted,
		StartedAt:     service.now(),
		EndedAt:       service.now().Add(2 * time.Minute),
		OutputExcerpt: "Completed successfully",
	})
	if err != nil {
		t.Fatalf("update job run: %v", err)
	}
	if updatedHistory.RunID != "run_123" || updatedHistory.Status != JobStatusCompleted {
		t.Fatalf("unexpected updated history: %+v", updatedHistory)
	}

	listedJobs, err := service.ListJobs(context.Background(), job.ProfileID, 20)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(listedJobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(listedJobs))
	}

	listedRuns, err := service.ListRuns(context.Background(), job.ID, 20)
	if err != nil {
		t.Fatalf("list job runs: %v", err)
	}
	if len(listedRuns) != 1 || listedRuns[0].RunID != "run_123" {
		t.Fatalf("unexpected job runs: %+v", listedRuns)
	}
}

func TestListDueJobsUsesNextRunAt(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	service.now = func() time.Time {
		return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	}

	job, err := service.CreateJob(context.Background(), CreateJobInput{
		ProfileID:     "default",
		Name:          "Once",
		Prompt:        "Run once",
		ScheduleKind:  "once",
		ScheduleValue: "2026-05-07T12:10:00Z",
		Timezone:      "UTC",
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create once job: %v", err)
	}

	due, err := service.ListDueJobs(context.Background(), "default", time.Date(2026, 5, 7, 12, 9, 0, 0, time.UTC), 20)
	if err != nil {
		t.Fatalf("list due jobs early: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("expected no due jobs before next_run_at, got %d", len(due))
	}

	due, err = service.ListDueJobs(context.Background(), "default", time.Date(2026, 5, 7, 12, 11, 0, 0, time.UTC), 20)
	if err != nil {
		t.Fatalf("list due jobs late: %v", err)
	}
	if len(due) != 1 || due[0].ID != job.ID {
		t.Fatalf("expected created job to be due, got %+v", due)
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
