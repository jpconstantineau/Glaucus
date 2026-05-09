package batch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	CollectionJobs     = "batch_jobs"
	CollectionAttempts = "batch_attempts"

	SchemaVersion = "batch.v1"

	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusPartial   = "partial"
	JobStatusFailed    = "failed"

	AttemptStatusPending   = "pending"
	AttemptStatusRunning   = "running"
	AttemptStatusCompleted = "completed"
	AttemptStatusFailed    = "failed"
)

type Item struct {
	ID       string         `json:"id"`
	Prompt   string         `json:"prompt"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Job struct {
	ID               string         `json:"id"`
	ProfileID        string         `json:"profile_id"`
	Name             string         `json:"name"`
	SchemaVersion    string         `json:"schema_version"`
	Status           string         `json:"status"`
	ProviderID       string         `json:"provider_id"`
	ModelID          string         `json:"model_id"`
	Toolset          string         `json:"toolset"`
	WorkingDirectory string         `json:"working_directory"`
	ItemCount        int            `json:"item_count"`
	CompletedCount   int            `json:"completed_count"`
	FailedCount      int            `json:"failed_count"`
	CreatedBy        string         `json:"created_by"`
	ExportPath       string         `json:"export_path"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	StartedAt        time.Time      `json:"started_at"`
	EndedAt          time.Time      `json:"ended_at"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type Attempt struct {
	ID           string         `json:"id"`
	ProfileID    string         `json:"profile_id"`
	JobID        string         `json:"job_id"`
	ItemID       string         `json:"item_id"`
	ItemIndex    int            `json:"item_index"`
	Status       string         `json:"status"`
	Prompt       string         `json:"prompt"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	SessionID    string         `json:"session_id"`
	RunID        string         `json:"run_id"`
	OutputText   string         `json:"output_text"`
	Usage        map[string]any `json:"usage,omitempty"`
	ErrorMessage string         `json:"error_message"`
	StartedAt    time.Time      `json:"started_at"`
	EndedAt      time.Time      `json:"ended_at"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type TrajectoryTurn struct {
	Role        string         `json:"role"`
	Ordinal     int            `json:"ordinal"`
	Text        string         `json:"text"`
	Content     any            `json:"content,omitempty"`
	ToolCalls   map[string]any `json:"tool_calls,omitempty"`
	ToolResults map[string]any `json:"tool_results,omitempty"`
}

type TrajectoryRow struct {
	SchemaVersion string             `json:"schema_version"`
	ProfileID     string             `json:"profile_id"`
	JobID         string             `json:"job_id"`
	AttemptID     string             `json:"attempt_id"`
	ItemID        string             `json:"item_id"`
	ItemIndex     int                `json:"item_index"`
	Status        string             `json:"status"`
	ProviderID    string             `json:"provider_id"`
	ModelID       string             `json:"model_id"`
	Toolset       string             `json:"toolset"`
	SessionID     string             `json:"session_id"`
	RunID         string             `json:"run_id"`
	Prompt        string             `json:"prompt"`
	ItemMetadata  map[string]any     `json:"item_metadata,omitempty"`
	JobMetadata   map[string]any     `json:"job_metadata,omitempty"`
	Turns         []TrajectoryTurn   `json:"turns,omitempty"`
	Events        []runtime.RunEvent `json:"events,omitempty"`
	Usage         map[string]any     `json:"usage,omitempty"`
	OutputText    string             `json:"output_text,omitempty"`
	ErrorMessage  string             `json:"error_message,omitempty"`
	GeneratedAt   time.Time          `json:"generated_at"`
}

type CreateJobInput struct {
	ProfileID        string
	Name             string
	ProviderID       string
	ModelID          string
	Toolset          string
	WorkingDirectory string
	CreatedBy        string
	Items            []Item
	Metadata         map[string]any
}

type UpdateJobInput struct {
	Status         string
	CompletedCount int
	FailedCount    int
	ExportPath     string
	StartedAt      time.Time
	EndedAt        time.Time
}

type UpdateAttemptInput struct {
	Status       string
	SessionID    string
	RunID        string
	OutputText   string
	Usage        map[string]any
	ErrorMessage string
	StartedAt    time.Time
	EndedAt      time.Time
}

type ExportBundle struct {
	ManifestPath   string `json:"manifest_path"`
	TrajectoryPath string `json:"trajectory_path"`
}

type ExportManifest struct {
	SchemaVersion string         `json:"schema_version"`
	Job           Job            `json:"job"`
	AttemptCount  int            `json:"attempt_count"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type Service struct {
	app      core.App
	sessions *sessions.Service
	events   *runtime.EventService
	now      func() time.Time
}

func NewService(app core.App, sessionService *sessions.Service, eventService *runtime.EventService) *Service {
	return &Service{
		app:      app,
		sessions: sessionService,
		events:   eventService,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateJob(ctx context.Context, input CreateJobInput) (Job, []Attempt, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Job{}, nil, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return Job{}, nil, errors.New("name is required")
	}
	if len(input.Items) == 0 {
		return Job{}, nil, errors.New("at least one batch item is required")
	}

	var (
		job      Job
		attempts []Attempt
	)
	err := s.app.RunInTransaction(func(txApp core.App) error {
		tx := NewService(txApp, s.sessions, s.events)
		tx.now = s.now

		record, err := tx.newRecord(CollectionJobs)
		if err != nil {
			return err
		}
		record.Set("profile_id", input.ProfileID)
		record.Set("name", strings.TrimSpace(input.Name))
		record.Set("schema_version", SchemaVersion)
		record.Set("status", JobStatusQueued)
		record.Set("provider_id", strings.TrimSpace(input.ProviderID))
		record.Set("model_id", strings.TrimSpace(input.ModelID))
		record.Set("toolset", strings.TrimSpace(input.Toolset))
		record.Set("working_directory", strings.TrimSpace(input.WorkingDirectory))
		record.Set("item_count", len(input.Items))
		record.Set("completed_count", 0)
		record.Set("failed_count", 0)
		record.Set("created_by", strings.TrimSpace(input.CreatedBy))
		if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
			return err
		}
		if err := tx.app.SaveWithContext(ctx, record); err != nil {
			return fmt.Errorf("save batch job: %w", err)
		}

		job, err = jobFromRecord(record)
		if err != nil {
			return err
		}

		attempts = make([]Attempt, 0, len(input.Items))
		for index, item := range input.Items {
			if strings.TrimSpace(item.Prompt) == "" {
				return fmt.Errorf("batch item %d prompt is required", index+1)
			}
			attemptRecord, err := tx.newRecord(CollectionAttempts)
			if err != nil {
				return err
			}
			attemptRecord.Set("profile_id", input.ProfileID)
			attemptRecord.Set("job_id", job.ID)
			attemptRecord.Set("item_id", normalizeItemID(item.ID, index))
			attemptRecord.Set("item_index", index+1)
			attemptRecord.Set("status", AttemptStatusPending)
			attemptRecord.Set("prompt", item.Prompt)
			if err := setJSON(attemptRecord, "metadata_json", item.Metadata); err != nil {
				return err
			}
			if err := tx.app.SaveWithContext(ctx, attemptRecord); err != nil {
				return fmt.Errorf("save batch attempt: %w", err)
			}
			attempt, err := attemptFromRecord(attemptRecord)
			if err != nil {
				return err
			}
			attempts = append(attempts, attempt)
		}

		return nil
	})
	if err != nil {
		return Job{}, nil, err
	}
	return job, attempts, nil
}

func (s *Service) GetJob(ctx context.Context, jobID string) (Job, error) {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("find batch job: %w", err)
	}
	_ = ctx
	return jobFromRecord(record)
}

func (s *Service) ListJobs(ctx context.Context, profileID string, limit int) ([]Job, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(CollectionJobs, "profile_id = {:profile_id}", "-created", limit, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return nil, fmt.Errorf("list batch jobs: %w", err)
	}
	items := make([]Job, 0, len(records))
	for _, record := range records {
		item, err := jobFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) UpdateJob(ctx context.Context, jobID string, input UpdateJobInput) (Job, error) {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("find batch job: %w", err)
	}
	if strings.TrimSpace(input.Status) != "" {
		record.Set("status", strings.TrimSpace(input.Status))
	}
	if input.CompletedCount >= 0 {
		record.Set("completed_count", input.CompletedCount)
	}
	if input.FailedCount >= 0 {
		record.Set("failed_count", input.FailedCount)
	}
	if strings.TrimSpace(input.ExportPath) != "" {
		record.Set("export_path", strings.TrimSpace(input.ExportPath))
	}
	if err := setTime(record, "started_at", input.StartedAt); err != nil {
		return Job{}, err
	}
	if err := setTime(record, "ended_at", input.EndedAt); err != nil {
		return Job{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Job{}, fmt.Errorf("update batch job: %w", err)
	}
	return jobFromRecord(record)
}

func (s *Service) GetAttempt(ctx context.Context, attemptID string) (Attempt, error) {
	record, err := s.app.FindRecordById(CollectionAttempts, attemptID)
	if err != nil {
		return Attempt{}, fmt.Errorf("find batch attempt: %w", err)
	}
	_ = ctx
	return attemptFromRecord(record)
}

func (s *Service) ListAttempts(ctx context.Context, jobID string) ([]Attempt, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("job id is required")
	}
	records, err := s.app.FindRecordsByFilter(CollectionAttempts, "job_id = {:job_id}", "item_index", 0, 0, dbx.Params{"job_id": jobID})
	if err != nil {
		return nil, fmt.Errorf("list batch attempts: %w", err)
	}
	items := make([]Attempt, 0, len(records))
	for _, record := range records {
		item, err := attemptFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) ListRunnableAttempts(ctx context.Context, jobID string) ([]Attempt, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("job id is required")
	}
	records, err := s.app.FindRecordsByFilter(CollectionAttempts, "job_id = {:job_id} && (status = 'pending' || status = 'failed')", "item_index", 0, 0, dbx.Params{"job_id": jobID})
	if err != nil {
		return nil, fmt.Errorf("list runnable attempts: %w", err)
	}
	items := make([]Attempt, 0, len(records))
	for _, record := range records {
		item, err := attemptFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) UpdateAttempt(ctx context.Context, attemptID string, input UpdateAttemptInput) (Attempt, error) {
	record, err := s.app.FindRecordById(CollectionAttempts, attemptID)
	if err != nil {
		return Attempt{}, fmt.Errorf("find batch attempt: %w", err)
	}
	if strings.TrimSpace(input.Status) != "" {
		record.Set("status", strings.TrimSpace(input.Status))
	}
	if strings.TrimSpace(input.SessionID) != "" {
		record.Set("session_id", strings.TrimSpace(input.SessionID))
	}
	if strings.TrimSpace(input.RunID) != "" {
		record.Set("run_id", strings.TrimSpace(input.RunID))
	}
	if input.OutputText != "" {
		record.Set("output_text", input.OutputText)
	}
	if input.ErrorMessage != "" {
		record.Set("error_message", input.ErrorMessage)
	}
	if err := setJSON(record, "usage_json", input.Usage); err != nil {
		return Attempt{}, err
	}
	if err := setTime(record, "started_at", input.StartedAt); err != nil {
		return Attempt{}, err
	}
	if err := setTime(record, "ended_at", input.EndedAt); err != nil {
		return Attempt{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Attempt{}, fmt.Errorf("update batch attempt: %w", err)
	}
	return attemptFromRecord(record)
}

func (s *Service) RecomputeJob(ctx context.Context, jobID string) (Job, error) {
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	attempts, err := s.ListAttempts(ctx, jobID)
	if err != nil {
		return Job{}, err
	}

	completed := 0
	failed := 0
	status := JobStatusQueued
	running := false
	for _, attempt := range attempts {
		switch attempt.Status {
		case AttemptStatusCompleted:
			completed++
		case AttemptStatusFailed:
			failed++
		case AttemptStatusRunning:
			running = true
		}
	}
	switch {
	case running:
		status = JobStatusRunning
	case completed == len(attempts):
		status = JobStatusCompleted
	case completed > 0 && failed > 0:
		status = JobStatusPartial
	case failed == len(attempts):
		status = JobStatusFailed
	case completed > 0:
		status = JobStatusPartial
	case failed > 0:
		status = JobStatusFailed
	default:
		status = JobStatusQueued
	}

	return s.UpdateJob(ctx, jobID, UpdateJobInput{
		Status:         status,
		CompletedCount: completed,
		FailedCount:    failed,
		StartedAt:      job.StartedAt,
		EndedAt:        job.EndedAt,
	})
}

func (s *Service) BuildTrajectoryRows(ctx context.Context, jobID string) ([]TrajectoryRow, error) {
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	attempts, err := s.ListAttempts(ctx, jobID)
	if err != nil {
		return nil, err
	}

	rows := make([]TrajectoryRow, 0, len(attempts))
	for _, attempt := range attempts {
		row := TrajectoryRow{
			SchemaVersion: SchemaVersion,
			ProfileID:     job.ProfileID,
			JobID:         job.ID,
			AttemptID:     attempt.ID,
			ItemID:        attempt.ItemID,
			ItemIndex:     attempt.ItemIndex,
			Status:        attempt.Status,
			ProviderID:    job.ProviderID,
			ModelID:       job.ModelID,
			Toolset:       job.Toolset,
			SessionID:     attempt.SessionID,
			RunID:         attempt.RunID,
			Prompt:        attempt.Prompt,
			ItemMetadata:  attempt.Metadata,
			JobMetadata:   job.Metadata,
			Usage:         attempt.Usage,
			OutputText:    attempt.OutputText,
			ErrorMessage:  attempt.ErrorMessage,
			GeneratedAt:   s.now(),
		}
		if s.sessions != nil && attempt.SessionID != "" {
			messages, err := s.sessions.ListMessages(ctx, attempt.SessionID)
			if err != nil {
				return nil, fmt.Errorf("list trajectory messages: %w", err)
			}
			row.Turns = make([]TrajectoryTurn, 0, len(messages))
			for _, message := range messages {
				row.Turns = append(row.Turns, TrajectoryTurn{
					Role:        message.Role,
					Ordinal:     message.Ordinal,
					Text:        message.VisibleText,
					Content:     message.Content,
					ToolCalls:   message.ToolCalls,
					ToolResults: message.ToolResults,
				})
				if len(row.Usage) == 0 && len(message.Usage) > 0 && message.Role == "assistant" {
					row.Usage = message.Usage
				}
			}
		}
		if s.events != nil && attempt.RunID != "" {
			events, err := s.events.ListRunEvents(ctx, attempt.RunID, 0)
			if err != nil {
				return nil, fmt.Errorf("list trajectory events: %w", err)
			}
			row.Events = events
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Service) WriteTrajectoryExport(ctx context.Context, jobID, profileRoot string) (ExportBundle, error) {
	rows, err := s.BuildTrajectoryRows(ctx, jobID)
	if err != nil {
		return ExportBundle{}, err
	}
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return ExportBundle{}, err
	}

	exportDir, err := profile.ResolveOwnedPath(profileRoot, filepath.ToSlash(filepath.Join("exports", "batches", job.ID)))
	if err != nil {
		return ExportBundle{}, err
	}
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return ExportBundle{}, fmt.Errorf("create batch export dir: %w", err)
	}

	manifestPath := filepath.Join(exportDir, "manifest.json")
	trajectoryPath := filepath.Join(exportDir, "trajectory.jsonl")

	manifestBody, err := json.MarshalIndent(ExportManifest{
		SchemaVersion: SchemaVersion,
		Job:           job,
		AttemptCount:  len(rows),
		GeneratedAt:   s.now(),
		Metadata: map[string]any{
			"trajectory_file": "trajectory.jsonl",
		},
	}, "", "  ")
	if err != nil {
		return ExportBundle{}, fmt.Errorf("marshal batch manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestBody, 0o644); err != nil {
		return ExportBundle{}, fmt.Errorf("write batch manifest: %w", err)
	}

	file, err := os.Create(trajectoryPath)
	if err != nil {
		return ExportBundle{}, fmt.Errorf("create trajectory export: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return ExportBundle{}, fmt.Errorf("encode trajectory row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return ExportBundle{}, fmt.Errorf("flush trajectory export: %w", err)
	}

	relativeExportPath := filepath.ToSlash(filepath.Join("exports", "batches", job.ID, "trajectory.jsonl"))
	if _, err := s.UpdateJob(ctx, job.ID, UpdateJobInput{
		Status:         job.Status,
		CompletedCount: job.CompletedCount,
		FailedCount:    job.FailedCount,
		ExportPath:     relativeExportPath,
		StartedAt:      job.StartedAt,
		EndedAt:        job.EndedAt,
	}); err != nil {
		return ExportBundle{}, err
	}

	return ExportBundle{
		ManifestPath:   manifestPath,
		TrajectoryPath: trajectoryPath,
	}, nil
}

func (s *Service) newRecord(collection string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

func normalizeItemID(value string, index int) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("item-%03d", index+1)
}

func jobFromRecord(record *core.Record) (Job, error) {
	item := Job{
		ID:               record.Id,
		ProfileID:        record.GetString("profile_id"),
		Name:             record.GetString("name"),
		SchemaVersion:    record.GetString("schema_version"),
		Status:           record.GetString("status"),
		ProviderID:       record.GetString("provider_id"),
		ModelID:          record.GetString("model_id"),
		Toolset:          record.GetString("toolset"),
		WorkingDirectory: record.GetString("working_directory"),
		ItemCount:        record.GetInt("item_count"),
		CompletedCount:   record.GetInt("completed_count"),
		FailedCount:      record.GetInt("failed_count"),
		CreatedBy:        record.GetString("created_by"),
		ExportPath:       record.GetString("export_path"),
		StartedAt:        record.GetDateTime("started_at").Time(),
		EndedAt:          record.GetDateTime("ended_at").Time(),
		CreatedAt:        record.GetDateTime("created").Time(),
		UpdatedAt:        record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Job{}, err
	}
	return item, nil
}

func attemptFromRecord(record *core.Record) (Attempt, error) {
	item := Attempt{
		ID:           record.Id,
		ProfileID:    record.GetString("profile_id"),
		JobID:        record.GetString("job_id"),
		ItemID:       record.GetString("item_id"),
		ItemIndex:    record.GetInt("item_index"),
		Status:       record.GetString("status"),
		Prompt:       record.GetString("prompt"),
		SessionID:    record.GetString("session_id"),
		RunID:        record.GetString("run_id"),
		OutputText:   record.GetString("output_text"),
		ErrorMessage: record.GetString("error_message"),
		StartedAt:    record.GetDateTime("started_at").Time(),
		EndedAt:      record.GetDateTime("ended_at").Time(),
		CreatedAt:    record.GetDateTime("created").Time(),
		UpdatedAt:    record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Attempt{}, err
	}
	if err := decodeJSONField(record, "usage_json", &item.Usage); err != nil {
		return Attempt{}, err
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

func setTime(record *core.Record, field string, value time.Time) error {
	if value.IsZero() {
		return nil
	}
	dt, err := types.ParseDateTime(value.UTC())
	if err != nil {
		return fmt.Errorf("parse %s: %w", field, err)
	}
	record.Set(field, dt)
	return nil
}
