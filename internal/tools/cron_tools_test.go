package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCronJobToolCreateListAndHistory(t *testing.T) {
	manager := &fakeCronJobManager{}
	tool := CronJobTool{manager: manager}
	root := t.TempDir()

	created := tool.Execute(context.Background(), ToolRequest{
		ProfileID:        "default",
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"action": "create",
			"name":   "Morning digest",
			"prompt": "Summarize overnight activity",
			"schedule": map[string]any{
				"kind":  "interval",
				"value": "1h",
			},
			"timezone": "UTC",
		},
	})
	if created.Status != StatusSuccess {
		t.Fatalf("expected create success, got %s (%s)", created.Status, created.DisplayText)
	}
	payload := created.Payload.(map[string]any)
	job := payload["job"].(map[string]any)

	listed := tool.Execute(context.Background(), ToolRequest{
		ProfileID:        "default",
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments:        map[string]any{"action": "list"},
	})
	if listed.Status != StatusSuccess {
		t.Fatalf("expected list success, got %s (%s)", listed.Status, listed.DisplayText)
	}
	if !strings.Contains(listed.DisplayText, "Listed 1 cron jobs") {
		t.Fatalf("unexpected list output: %s", listed.DisplayText)
	}

	runNow := tool.Execute(context.Background(), ToolRequest{
		ProfileID:        "default",
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"action": "run_now",
			"job_id": job["id"],
		},
	})
	if runNow.Status != StatusSuccess {
		t.Fatalf("expected run_now success, got %s (%s)", runNow.Status, runNow.DisplayText)
	}

	history := tool.Execute(context.Background(), ToolRequest{
		ProfileID:        "default",
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"action": "history",
			"job_id": job["id"],
		},
	})
	if history.Status != StatusSuccess {
		t.Fatalf("expected history success, got %s (%s)", history.Status, history.DisplayText)
	}
}

type fakeCronJobManager struct {
	jobs []map[string]any
	runs []map[string]any
}

func (a *fakeCronJobManager) ListJobs(ctx context.Context, profileID string, limit int) (any, error) {
	_ = ctx
	_ = profileID
	_ = limit
	return a.jobs, nil
}

func (a *fakeCronJobManager) GetJob(ctx context.Context, jobID string) (any, error) {
	_ = ctx
	for _, job := range a.jobs {
		if job["id"] == jobID {
			return job, nil
		}
	}
	return nil, nil
}

func (a *fakeCronJobManager) CreateJob(ctx context.Context, input CronJobCreateInput) (any, error) {
	_ = ctx
	job := map[string]any{
		"id":       "job_1",
		"name":     input.Name,
		"prompt":   input.Prompt,
		"timezone": input.Timezone,
	}
	a.jobs = append(a.jobs, job)
	return job, nil
}

func (a *fakeCronJobManager) UpdateJob(ctx context.Context, jobID string, input CronJobUpdateInput) (any, error) {
	_ = ctx
	_ = input
	return a.GetJob(context.Background(), jobID)
}

func (a *fakeCronJobManager) PauseJob(ctx context.Context, jobID string) (any, error) {
	return a.GetJob(ctx, jobID)
}

func (a *fakeCronJobManager) ResumeJob(ctx context.Context, jobID string) (any, error) {
	return a.GetJob(ctx, jobID)
}

func (a *fakeCronJobManager) DeleteJob(ctx context.Context, jobID string) error {
	_ = ctx
	_ = jobID
	return nil
}

func (a *fakeCronJobManager) QueueManualRun(ctx context.Context, profileID string, jobID string) (any, any, error) {
	_ = ctx
	_ = profileID
	job, _ := a.GetJob(context.Background(), jobID)
	run := map[string]any{"id": "jobrun_1", "job_id": jobID}
	a.runs = append(a.runs, run)
	return job, run, nil
}

func (a *fakeCronJobManager) ListJobRuns(ctx context.Context, jobID string, limit int) (any, error) {
	_ = ctx
	_ = jobID
	_ = limit
	return a.runs, nil
}
