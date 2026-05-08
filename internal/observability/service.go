package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type Snapshot struct {
	ProfileID        string `json:"profile_id"`
	TotalRuns        int    `json:"total_runs"`
	QueuedRuns       int    `json:"queued_runs"`
	RunningRuns      int    `json:"running_runs"`
	CompletedRuns    int    `json:"completed_runs"`
	FailedRuns       int    `json:"failed_runs"`
	CancelledRuns    int    `json:"cancelled_runs"`
	TotalJobRuns     int    `json:"total_job_runs"`
	QueuedJobRuns    int    `json:"queued_job_runs"`
	RunningJobRuns   int    `json:"running_job_runs"`
	CompletedJobRuns int    `json:"completed_job_runs"`
	FailedJobRuns    int    `json:"failed_job_runs"`
	TotalMessages    int    `json:"total_messages"`
	TotalTokens      int    `json:"total_tokens"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
}

type BuildInfo struct {
	AppName string
	Version string
	Commit  string
	BuiltAt string
}

type Service struct {
	app   core.App
	build BuildInfo
}

func NewService(app core.App, build BuildInfo) *Service {
	return &Service{app: app, build: build}
}

func (s *Service) Snapshot(ctx context.Context, profileID string) (Snapshot, error) {
	snapshot := Snapshot{ProfileID: profileID}
	runRecords, err := s.app.FindRecordsByFilter("agent_runs", "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load runs: %w", err)
	}
	for _, record := range runRecords {
		snapshot.TotalRuns++
		switch record.GetString("status") {
		case "queued":
			snapshot.QueuedRuns++
		case "running":
			snapshot.RunningRuns++
		case "completed":
			snapshot.CompletedRuns++
		case "failed":
			snapshot.FailedRuns++
		case "cancelled":
			snapshot.CancelledRuns++
		}
	}

	jobRunRecords, err := s.app.FindRecordsByFilter("cron_job_runs", "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load job runs: %w", err)
	}
	for _, record := range jobRunRecords {
		snapshot.TotalJobRuns++
		switch record.GetString("status") {
		case "queued":
			snapshot.QueuedJobRuns++
		case "running":
			snapshot.RunningJobRuns++
		case "completed":
			snapshot.CompletedJobRuns++
		case "failed":
			snapshot.FailedJobRuns++
		}
	}

	messageRecords, err := s.app.FindRecordsByFilter("agent_messages", "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load messages: %w", err)
	}
	for _, record := range messageRecords {
		snapshot.TotalMessages++
		snapshot.InputTokens += tokenCount(record.GetString("usage_json"), "input_tokens", "prompt_tokens")
		snapshot.OutputTokens += tokenCount(record.GetString("usage_json"), "output_tokens", "completion_tokens")
		snapshot.TotalTokens += tokenCount(record.GetString("usage_json"), "total_tokens")
	}
	if snapshot.TotalTokens == 0 {
		snapshot.TotalTokens = snapshot.InputTokens + snapshot.OutputTokens
	}

	_ = ctx
	return snapshot, nil
}

func (s *Service) Prometheus(snapshot Snapshot) string {
	lines := []string{
		fmt.Sprintf("glaucus_build_info{app=%q,version=%q,commit=%q,built_at=%q} 1", s.build.AppName, s.build.Version, s.build.Commit, s.build.BuiltAt),
		fmt.Sprintf("glaucus_runs_total{profile=%q} %d", snapshot.ProfileID, snapshot.TotalRuns),
		fmt.Sprintf("glaucus_runs_completed_total{profile=%q} %d", snapshot.ProfileID, snapshot.CompletedRuns),
		fmt.Sprintf("glaucus_runs_failed_total{profile=%q} %d", snapshot.ProfileID, snapshot.FailedRuns),
		fmt.Sprintf("glaucus_runs_cancelled_total{profile=%q} %d", snapshot.ProfileID, snapshot.CancelledRuns),
		fmt.Sprintf("glaucus_job_runs_total{profile=%q} %d", snapshot.ProfileID, snapshot.TotalJobRuns),
		fmt.Sprintf("glaucus_messages_total{profile=%q} %d", snapshot.ProfileID, snapshot.TotalMessages),
		fmt.Sprintf("glaucus_usage_input_tokens_total{profile=%q} %d", snapshot.ProfileID, snapshot.InputTokens),
		fmt.Sprintf("glaucus_usage_output_tokens_total{profile=%q} %d", snapshot.ProfileID, snapshot.OutputTokens),
		fmt.Sprintf("glaucus_usage_tokens_total{profile=%q} %d", snapshot.ProfileID, snapshot.TotalTokens),
	}
	return strings.Join(lines, "\n") + "\n"
}

func tokenCount(raw string, keys ...string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0
	}
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			switch typed := value.(type) {
			case float64:
				return int(typed)
			case int:
				return typed
			}
		}
	}
	return 0
}
