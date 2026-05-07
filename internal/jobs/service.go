package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	CollectionJobs    = "cron_jobs"
	CollectionJobRuns = "cron_job_runs"

	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
)

type Job struct {
	ID                string
	ProfileID         string
	Name              string
	Prompt            string
	ScheduleKind      string
	ScheduleValue     string
	Timezone          string
	Enabled           bool
	DeliveryTarget    map[string]any
	ToolsetOverrides  map[string]any
	ProviderOverrides map[string]any
	CWD               string
	NextRunAt         time.Time
	LastRunAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type JobRun struct {
	ID            string
	ProfileID     string
	JobID         string
	RunID         string
	Status        string
	ScheduledFor  time.Time
	StartedAt     time.Time
	EndedAt       time.Time
	OutputExcerpt string
	ErrorMessage  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CreateJobInput struct {
	ProfileID         string
	Name              string
	Prompt            string
	ScheduleKind      string
	ScheduleValue     string
	Timezone          string
	Enabled           bool
	DeliveryTarget    map[string]any
	ToolsetOverrides  map[string]any
	ProviderOverrides map[string]any
	CWD               string
}

type UpdateJobInput struct {
	Name              string
	Prompt            string
	ScheduleKind      string
	ScheduleValue     string
	Timezone          string
	Enabled           *bool
	DeliveryTarget    map[string]any
	ToolsetOverrides  map[string]any
	ProviderOverrides map[string]any
	CWD               string
}

type RecordRunInput struct {
	ProfileID     string
	JobID         string
	RunID         string
	Status        string
	ScheduledFor  time.Time
	StartedAt     time.Time
	EndedAt       time.Time
	OutputExcerpt string
	ErrorMessage  string
}

type UpdateRunInput struct {
	RunID         string
	Status        string
	StartedAt     time.Time
	EndedAt       time.Time
	OutputExcerpt string
	ErrorMessage  string
}

type Service struct {
	app core.App
	now func() time.Time
}

func NewService(app core.App) *Service {
	return &Service{
		app: app,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateJob(ctx context.Context, input CreateJobInput) (Job, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Job{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return Job{}, errors.New("name is required")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Job{}, errors.New("prompt is required")
	}

	schedule, err := parseSchedule(input.ScheduleKind, input.ScheduleValue, input.Timezone)
	if err != nil {
		return Job{}, err
	}

	record, err := s.newRecord(CollectionJobs)
	if err != nil {
		return Job{}, err
	}

	record.Set("profile_id", input.ProfileID)
	record.Set("name", strings.TrimSpace(input.Name))
	record.Set("prompt", input.Prompt)
	record.Set("schedule_kind", schedule.Kind)
	record.Set("schedule_value", schedule.Value)
	record.Set("timezone", schedule.Timezone)
	record.Set("enabled", input.Enabled)
	record.Set("cwd", strings.TrimSpace(input.CWD))
	if err := setJSON(record, "delivery_target_json", input.DeliveryTarget); err != nil {
		return Job{}, err
	}
	if err := setJSON(record, "toolset_overrides_json", input.ToolsetOverrides); err != nil {
		return Job{}, err
	}
	if err := setJSON(record, "provider_overrides_json", input.ProviderOverrides); err != nil {
		return Job{}, err
	}
	if input.Enabled {
		nextRunAt, err := schedule.NextRun(s.now())
		if err != nil {
			return Job{}, err
		}
		if !nextRunAt.IsZero() {
			dt, err := types.ParseDateTime(nextRunAt)
			if err != nil {
				return Job{}, fmt.Errorf("parse next_run_at: %w", err)
			}
			record.Set("next_run_at", dt)
		}
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Job{}, fmt.Errorf("save job: %w", err)
	}
	return jobFromRecord(record)
}

func (s *Service) GetJob(ctx context.Context, jobID string) (Job, error) {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("find job: %w", err)
	}
	_ = ctx
	return jobFromRecord(record)
}

func (s *Service) ListJobs(ctx context.Context, profileID string, limit int) ([]Job, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionJobs,
		"profile_id = {:profile_id}",
		"next_run_at,name",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	result := make([]Job, 0, len(records))
	for _, record := range records {
		job, err := jobFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	_ = ctx
	return result, nil
}

func (s *Service) ListDueJobs(ctx context.Context, profileID string, now time.Time, limit int) ([]Job, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionJobs,
		"profile_id = {:profile_id} && enabled = true",
		"next_run_at",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list due jobs: %w", err)
	}
	result := make([]Job, 0, len(records))
	for _, record := range records {
		job, err := jobFromRecord(record)
		if err != nil {
			return nil, err
		}
		if !job.NextRunAt.IsZero() && !job.NextRunAt.After(now.UTC()) {
			result = append(result, job)
		}
	}
	_ = ctx
	return result, nil
}

func (s *Service) UpdateJob(ctx context.Context, jobID string, input UpdateJobInput) (Job, error) {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("find job: %w", err)
	}

	current, err := jobFromRecord(record)
	if err != nil {
		return Job{}, err
	}

	if name := strings.TrimSpace(input.Name); name != "" {
		record.Set("name", name)
		current.Name = name
	}
	if input.Prompt != "" {
		record.Set("prompt", input.Prompt)
		current.Prompt = input.Prompt
	}
	if value := strings.TrimSpace(input.CWD); value != "" {
		record.Set("cwd", value)
		current.CWD = value
	}
	if input.DeliveryTarget != nil {
		if err := setJSON(record, "delivery_target_json", input.DeliveryTarget); err != nil {
			return Job{}, err
		}
		current.DeliveryTarget = input.DeliveryTarget
	}
	if input.ToolsetOverrides != nil {
		if err := setJSON(record, "toolset_overrides_json", input.ToolsetOverrides); err != nil {
			return Job{}, err
		}
		current.ToolsetOverrides = input.ToolsetOverrides
	}
	if input.ProviderOverrides != nil {
		if err := setJSON(record, "provider_overrides_json", input.ProviderOverrides); err != nil {
			return Job{}, err
		}
		current.ProviderOverrides = input.ProviderOverrides
	}
	if input.Enabled != nil {
		record.Set("enabled", *input.Enabled)
		current.Enabled = *input.Enabled
	}

	if input.ScheduleKind != "" || input.ScheduleValue != "" || input.Timezone != "" {
		scheduleKind := current.ScheduleKind
		if input.ScheduleKind != "" {
			scheduleKind = input.ScheduleKind
		}
		scheduleValue := current.ScheduleValue
		if input.ScheduleValue != "" {
			scheduleValue = input.ScheduleValue
		}
		scheduleTimezone := current.Timezone
		if input.Timezone != "" {
			scheduleTimezone = input.Timezone
		}
		schedule, err := parseSchedule(scheduleKind, scheduleValue, scheduleTimezone)
		if err != nil {
			return Job{}, err
		}
		record.Set("schedule_kind", schedule.Kind)
		record.Set("schedule_value", schedule.Value)
		record.Set("timezone", schedule.Timezone)
		current.ScheduleKind = schedule.Kind
		current.ScheduleValue = schedule.Value
		current.Timezone = schedule.Timezone
	}

	if current.Enabled {
		nextRunAt, err := parseSchedule(current.ScheduleKind, current.ScheduleValue, current.Timezone)
		if err != nil {
			return Job{}, err
		}
		nextTime, err := nextRunAt.NextRun(maxTime(current.LastRunAt, s.now()))
		if err != nil {
			return Job{}, err
		}
		if !nextTime.IsZero() {
			dt, err := types.ParseDateTime(nextTime)
			if err != nil {
				return Job{}, fmt.Errorf("parse next_run_at: %w", err)
			}
			record.Set("next_run_at", dt)
		}
	} else {
		record.Set("next_run_at", nil)
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Job{}, fmt.Errorf("update job: %w", err)
	}
	return jobFromRecord(record)
}

func (s *Service) PauseJob(ctx context.Context, jobID string) (Job, error) {
	enabled := false
	return s.UpdateJob(ctx, jobID, UpdateJobInput{Enabled: &enabled})
}

func (s *Service) ResumeJob(ctx context.Context, jobID string) (Job, error) {
	enabled := true
	return s.UpdateJob(ctx, jobID, UpdateJobInput{Enabled: &enabled})
}

func (s *Service) DeleteJob(ctx context.Context, jobID string) error {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return fmt.Errorf("find job: %w", err)
	}
	if err := s.app.Delete(record); err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	_ = ctx
	return nil
}

func (s *Service) RecordRun(ctx context.Context, input RecordRunInput) (JobRun, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return JobRun{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.JobID) == "" {
		return JobRun{}, errors.New("job id is required")
	}
	if strings.TrimSpace(input.Status) == "" {
		input.Status = JobStatusQueued
	}

	record, err := s.newRecord(CollectionJobRuns)
	if err != nil {
		return JobRun{}, err
	}
	record.Set("profile_id", input.ProfileID)
	record.Set("job_id", input.JobID)
	record.Set("run_id", strings.TrimSpace(input.RunID))
	record.Set("status", input.Status)
	record.Set("output_excerpt", trimExcerpt(input.OutputExcerpt))
	record.Set("error_message", strings.TrimSpace(input.ErrorMessage))
	if err := setTime(record, "scheduled_for", input.ScheduledFor); err != nil {
		return JobRun{}, err
	}
	if err := setTime(record, "started_at", input.StartedAt); err != nil {
		return JobRun{}, err
	}
	if err := setTime(record, "ended_at", input.EndedAt); err != nil {
		return JobRun{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return JobRun{}, fmt.Errorf("save job run: %w", err)
	}
	return runFromRecord(record)
}

func (s *Service) UpdateRun(ctx context.Context, jobRunID string, input UpdateRunInput) (JobRun, error) {
	record, err := s.app.FindRecordById(CollectionJobRuns, jobRunID)
	if err != nil {
		return JobRun{}, fmt.Errorf("find job run: %w", err)
	}
	if input.RunID != "" {
		record.Set("run_id", input.RunID)
	}
	if strings.TrimSpace(input.Status) != "" {
		record.Set("status", input.Status)
	}
	if err := setTime(record, "started_at", input.StartedAt); err != nil {
		return JobRun{}, err
	}
	if err := setTime(record, "ended_at", input.EndedAt); err != nil {
		return JobRun{}, err
	}
	if input.OutputExcerpt != "" {
		record.Set("output_excerpt", trimExcerpt(input.OutputExcerpt))
	}
	if input.ErrorMessage != "" {
		record.Set("error_message", strings.TrimSpace(input.ErrorMessage))
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return JobRun{}, fmt.Errorf("update job run: %w", err)
	}
	return runFromRecord(record)
}

func (s *Service) ListRuns(ctx context.Context, jobID string, limit int) ([]JobRun, error) {
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("job id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionJobRuns,
		"job_id = {:job_id}",
		"-scheduled_for",
		limit,
		0,
		dbx.Params{"job_id": jobID},
	)
	if err != nil {
		return nil, fmt.Errorf("list job runs: %w", err)
	}
	result := make([]JobRun, 0, len(records))
	for _, record := range records {
		jobRun, err := runFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, jobRun)
	}
	_ = ctx
	return result, nil
}

func (s *Service) ListActiveJobRuns(ctx context.Context, profileID string, limit int) ([]JobRun, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionJobRuns,
		"profile_id = {:profile_id} && (status = 'queued' || status = 'running')",
		"",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list active job runs: %w", err)
	}
	result := make([]JobRun, 0, len(records))
	for _, record := range records {
		jobRun, err := runFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, jobRun)
	}
	_ = ctx
	return result, nil
}

func (s *Service) MarkJobTriggered(ctx context.Context, jobID string, triggeredAt time.Time) (Job, error) {
	record, err := s.app.FindRecordById(CollectionJobs, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("find job: %w", err)
	}
	job, err := jobFromRecord(record)
	if err != nil {
		return Job{}, err
	}

	schedule, err := parseSchedule(job.ScheduleKind, job.ScheduleValue, job.Timezone)
	if err != nil {
		return Job{}, err
	}
	nextRunAt, err := schedule.NextRun(triggeredAt)
	if err != nil {
		return Job{}, err
	}
	if err := setTime(record, "last_run_at", triggeredAt); err != nil {
		return Job{}, err
	}
	if job.Enabled {
		if err := setTime(record, "next_run_at", nextRunAt); err != nil {
			return Job{}, err
		}
	} else {
		record.Set("next_run_at", nil)
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Job{}, fmt.Errorf("save triggered job: %w", err)
	}
	return jobFromRecord(record)
}

func (s *Service) Now() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now()
}

func (s *Service) newRecord(collection string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

type scheduleSpec struct {
	Kind     string
	Value    string
	Timezone string
}

func parseSchedule(kind, value, timezone string) (scheduleSpec, error) {
	spec := scheduleSpec{
		Kind:     strings.ToLower(strings.TrimSpace(kind)),
		Value:    strings.TrimSpace(value),
		Timezone: strings.TrimSpace(timezone),
	}
	if spec.Kind == "" {
		spec.Kind = "interval"
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	if spec.Value == "" {
		return scheduleSpec{}, errors.New("schedule value is required")
	}
	switch spec.Kind {
	case "interval":
		d, err := time.ParseDuration(spec.Value)
		if err != nil || d <= 0 {
			return scheduleSpec{}, errors.New("interval schedules require a positive Go duration")
		}
	case "daily":
		if _, err := time.Parse("15:04", spec.Value); err != nil {
			return scheduleSpec{}, errors.New("daily schedules require HH:MM in 24-hour time")
		}
	case "once":
		if _, err := time.Parse(time.RFC3339, spec.Value); err != nil {
			return scheduleSpec{}, errors.New("once schedules require an RFC3339 timestamp")
		}
	default:
		return scheduleSpec{}, fmt.Errorf("unsupported schedule kind %q", spec.Kind)
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return scheduleSpec{}, fmt.Errorf("load timezone: %w", err)
	}
	return spec, nil
}

func (s scheduleSpec) NextRun(after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load timezone: %w", err)
	}
	anchor := after.UTC()
	switch s.Kind {
	case "interval":
		d, _ := time.ParseDuration(s.Value)
		return anchor.Add(d).UTC(), nil
	case "daily":
		clock, _ := time.Parse("15:04", s.Value)
		local := anchor.In(loc)
		next := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if !next.After(local) {
			next = next.Add(24 * time.Hour)
		}
		return next.UTC(), nil
	case "once":
		when, _ := time.Parse(time.RFC3339, s.Value)
		if !when.After(anchor) {
			return time.Time{}, nil
		}
		return when.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule kind %q", s.Kind)
	}
}

func jobFromRecord(record *core.Record) (Job, error) {
	job := Job{
		ID:            record.Id,
		ProfileID:     record.GetString("profile_id"),
		Name:          record.GetString("name"),
		Prompt:        record.GetString("prompt"),
		ScheduleKind:  record.GetString("schedule_kind"),
		ScheduleValue: record.GetString("schedule_value"),
		Timezone:      record.GetString("timezone"),
		Enabled:       record.GetBool("enabled"),
		CWD:           record.GetString("cwd"),
		NextRunAt:     record.GetDateTime("next_run_at").Time(),
		LastRunAt:     record.GetDateTime("last_run_at").Time(),
		CreatedAt:     record.GetDateTime("created").Time(),
		UpdatedAt:     record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "delivery_target_json", &job.DeliveryTarget); err != nil {
		return Job{}, err
	}
	if err := decodeJSONField(record, "toolset_overrides_json", &job.ToolsetOverrides); err != nil {
		return Job{}, err
	}
	if err := decodeJSONField(record, "provider_overrides_json", &job.ProviderOverrides); err != nil {
		return Job{}, err
	}
	return job, nil
}

func runFromRecord(record *core.Record) (JobRun, error) {
	return JobRun{
		ID:            record.Id,
		ProfileID:     record.GetString("profile_id"),
		JobID:         record.GetString("job_id"),
		RunID:         record.GetString("run_id"),
		Status:        record.GetString("status"),
		ScheduledFor:  record.GetDateTime("scheduled_for").Time(),
		StartedAt:     record.GetDateTime("started_at").Time(),
		EndedAt:       record.GetDateTime("ended_at").Time(),
		OutputExcerpt: record.GetString("output_excerpt"),
		ErrorMessage:  record.GetString("error_message"),
		CreatedAt:     record.GetDateTime("created").Time(),
		UpdatedAt:     record.GetDateTime("updated").Time(),
	}, nil
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

func trimExcerpt(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return strings.TrimSpace(value[:240]) + "..."
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
