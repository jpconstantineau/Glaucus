package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
)

type stubExecutor struct {
	result ExecutionResult
	err    error
}

func (s stubExecutor) ExecuteJob(context.Context, Job) (ExecutionResult, error) {
	return s.result, s.err
}

func TestSchedulerReconcilesActiveStateAndDispatchesDueJobs(t *testing.T) {
	app := newTestApp(t)
	jobService := NewService(app)
	sessionService := sessions.NewService(app)

	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	jobService.now = func() time.Time { return now }

	session, err := sessionService.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "web",
		Title:     "Queued run",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sessionService.CreateRun(context.Background(), sessions.CreateRunInput{
		ProfileID:     "default",
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		Status:        "running",
	}); err != nil {
		t.Fatalf("create active run: %v", err)
	}

	job, err := jobService.CreateJob(context.Background(), CreateJobInput{
		ProfileID:     "default",
		Name:          "Due job",
		Prompt:        "Summarize changes",
		ScheduleKind:  "once",
		ScheduleValue: "2026-05-07T11:55:00Z",
		Timezone:      "UTC",
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create due job: %v", err)
	}

	scheduler := NewScheduler("default", false, time.Minute, jobService, sessionService, stubExecutor{
		result: ExecutionResult{
			JobID:      job.ID,
			SessionID:  "session_cron",
			RunID:      "run_cron",
			OutputText: "done",
			Status:     "completed",
		},
	}, nil)
	scheduler.now = func() time.Time { return now }

	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer func() {
		_ = scheduler.Stop(context.Background())
	}()

	status := scheduler.Status()
	if status.RecoveredRuns == 0 {
		t.Fatalf("expected active runs to be reconciled, got %+v", status)
	}

	if err := scheduler.dispatchJob(context.Background(), job, now); err != nil {
		t.Fatalf("dispatch due job: %v", err)
	}

	runs, err := jobService.ListRuns(context.Background(), job.ID, 20)
	if err != nil {
		t.Fatalf("list job history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_cron" || runs[0].Status != JobStatusCompleted {
		t.Fatalf("unexpected job history: %+v", runs)
	}
}

func TestSchedulerCapturesWorkerFailure(t *testing.T) {
	app := newTestApp(t)
	jobService := NewService(app)
	sessionService := sessions.NewService(app)
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	jobService.now = func() time.Time { return now }

	job, err := jobService.CreateJob(context.Background(), CreateJobInput{
		ProfileID:     "default",
		Name:          "Failing job",
		Prompt:        "Do work",
		ScheduleKind:  "once",
		ScheduleValue: "2026-05-07T11:59:00Z",
		Timezone:      "UTC",
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	scheduler := NewScheduler("default", false, time.Minute, jobService, sessionService, stubExecutor{
		err: errors.New("boom"),
	}, nil)
	scheduler.now = func() time.Time { return now }

	if err := scheduler.dispatchJob(context.Background(), job, now); err == nil {
		t.Fatal("expected dispatch to fail")
	}

	status := scheduler.Status()
	if status.DispatchedJobs != 1 {
		t.Fatalf("expected dispatch count to increment, got %+v", status)
	}

	history, err := jobService.ListRuns(context.Background(), job.ID, 20)
	if err != nil {
		t.Fatalf("list job history: %v", err)
	}
	if len(history) != 1 || history[0].Status != JobStatusFailed {
		t.Fatalf("expected failed job history, got %+v", history)
	}
}
