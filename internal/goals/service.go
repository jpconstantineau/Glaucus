package goals

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
	CollectionSessionGoals = "session_goals"
	CollectionProfileGoals = "profile_goals"

	ScopeSession = "session"
	ScopeProfile = "profile"

	StatusActive  = "active"
	StatusCleared = "cleared"
)

type Goal struct {
	ID                 string
	Scope              string
	ProfileID          string
	SessionID          string
	Title              string
	Statement          string
	SuccessCriteria    string
	Status             string
	Priority           string
	Tags               []string
	State              map[string]any
	Metadata           map[string]any
	CreatedByRunID     string
	UpdatedByRunID     string
	ClearedByRunID     string
	LastEvaluatedRunID string
	Version            int
	ClearedAt          time.Time
	LastEvaluatedAt    time.Time
	LastEvaluation     map[string]any
	EvaluationHistory  []map[string]any
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreateGoalInput struct {
	Scope           string
	ProfileID       string
	SessionID       string
	Title           string
	Statement       string
	SuccessCriteria string
	Status          string
	Priority        string
	Tags            []string
	State           map[string]any
	Metadata        map[string]any
	CreatedByRunID  string
}

type ListGoalsInput struct {
	Scope     string
	ProfileID string
	SessionID string
	Status    string
	Limit     int
}

type UpdateGoalInput struct {
	Title           string
	Statement       string
	SuccessCriteria string
	Status          string
	Priority        string
	Tags            []string
	State           map[string]any
	Metadata        map[string]any
	UpdatedByRunID  string
}

type ClearGoalInput struct {
	ClearedByRunID string
}

type EvaluateGoalInput struct {
	Evaluation       map[string]any
	Status           string
	UpdatedByRunID   string
	EvaluatedByRunID string
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func (s *Service) CreateGoal(ctx context.Context, input CreateGoalInput) (Goal, error) {
	collection, scope, err := collectionForScope(input.Scope)
	if err != nil {
		return Goal{}, err
	}
	if strings.TrimSpace(input.ProfileID) == "" {
		return Goal{}, errors.New("profile id is required")
	}
	if scope == ScopeSession && strings.TrimSpace(input.SessionID) == "" {
		return Goal{}, errors.New("session id is required for session goals")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Goal{}, errors.New("title is required")
	}
	if strings.TrimSpace(input.Statement) == "" {
		return Goal{}, errors.New("statement is required")
	}

	if strings.TrimSpace(input.Status) == "" {
		input.Status = StatusActive
	}
	if strings.TrimSpace(input.Priority) == "" {
		input.Priority = "medium"
	}

	record, err := s.newRecord(collection)
	if err != nil {
		return Goal{}, err
	}
	record.Set("profile_id", input.ProfileID)
	record.Set("session_id", input.SessionID)
	record.Set("title", input.Title)
	record.Set("statement", input.Statement)
	record.Set("success_criteria", input.SuccessCriteria)
	record.Set("status", input.Status)
	record.Set("priority", input.Priority)
	record.Set("created_by_run_id", input.CreatedByRunID)
	record.Set("updated_by_run_id", input.CreatedByRunID)
	record.Set("version", 1)
	if err := setJSON(record, "tags_json", input.Tags); err != nil {
		return Goal{}, err
	}
	if err := setJSON(record, "state_json", input.State); err != nil {
		return Goal{}, err
	}
	if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
		return Goal{}, err
	}
	if err := setJSON(record, "evaluation_history_json", []map[string]any{}); err != nil {
		return Goal{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Goal{}, fmt.Errorf("save goal: %w", err)
	}

	return goalFromRecord(scope, record)
}

func (s *Service) GetGoal(ctx context.Context, scope, goalID string) (Goal, error) {
	collection, normalizedScope, err := collectionForScope(scope)
	if err != nil {
		return Goal{}, err
	}
	record, err := s.app.FindRecordById(collection, goalID)
	if err != nil {
		return Goal{}, fmt.Errorf("find goal: %w", err)
	}
	_ = ctx
	return goalFromRecord(normalizedScope, record)
}

func (s *Service) ListGoals(ctx context.Context, input ListGoalsInput) ([]Goal, error) {
	collection, scope, err := collectionForScope(input.Scope)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ProfileID) == "" {
		return nil, errors.New("profile id is required")
	}

	filter := "profile_id = {:profile_id}"
	params := dbx.Params{"profile_id": input.ProfileID}
	if scope == ScopeSession {
		if strings.TrimSpace(input.SessionID) == "" {
			return nil, errors.New("session id is required for session goals")
		}
		filter += " && session_id = {:session_id}"
		params["session_id"] = input.SessionID
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		filter += " && status = {:status}"
		params["status"] = status
	}

	records, err := s.app.FindRecordsByFilter(collection, filter, "title", input.Limit, 0, params)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}

	items := make([]Goal, 0, len(records))
	for _, record := range records {
		item, err := goalFromRecord(scope, record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) UpdateGoal(ctx context.Context, scope, goalID string, input UpdateGoalInput) (Goal, error) {
	collection, normalizedScope, err := collectionForScope(scope)
	if err != nil {
		return Goal{}, err
	}
	record, err := s.app.FindRecordById(collection, goalID)
	if err != nil {
		return Goal{}, fmt.Errorf("find goal: %w", err)
	}

	if value := strings.TrimSpace(input.Title); value != "" {
		record.Set("title", value)
	}
	if value := strings.TrimSpace(input.Statement); value != "" {
		record.Set("statement", value)
	}
	if input.SuccessCriteria != "" {
		record.Set("success_criteria", strings.TrimSpace(input.SuccessCriteria))
	}
	if value := strings.TrimSpace(input.Status); value != "" {
		record.Set("status", value)
	}
	if value := strings.TrimSpace(input.Priority); value != "" {
		record.Set("priority", value)
	}
	if input.Tags != nil {
		if err := setJSON(record, "tags_json", input.Tags); err != nil {
			return Goal{}, err
		}
	}
	if input.State != nil {
		if err := setJSON(record, "state_json", input.State); err != nil {
			return Goal{}, err
		}
	}
	if input.Metadata != nil {
		if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
			return Goal{}, err
		}
	}
	if value := strings.TrimSpace(input.UpdatedByRunID); value != "" {
		record.Set("updated_by_run_id", value)
	}
	record.Set("version", record.GetInt("version")+1)

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Goal{}, fmt.Errorf("update goal: %w", err)
	}
	return goalFromRecord(normalizedScope, record)
}

func (s *Service) ClearGoal(ctx context.Context, scope, goalID string, input ClearGoalInput) (Goal, error) {
	collection, normalizedScope, err := collectionForScope(scope)
	if err != nil {
		return Goal{}, err
	}
	record, err := s.app.FindRecordById(collection, goalID)
	if err != nil {
		return Goal{}, fmt.Errorf("find goal: %w", err)
	}
	record.Set("status", StatusCleared)
	record.Set("cleared_by_run_id", strings.TrimSpace(input.ClearedByRunID))
	record.Set("updated_by_run_id", strings.TrimSpace(input.ClearedByRunID))
	record.Set("version", record.GetInt("version")+1)
	if err := setTime(record, "cleared_at", time.Now().UTC()); err != nil {
		return Goal{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Goal{}, fmt.Errorf("clear goal: %w", err)
	}
	return goalFromRecord(normalizedScope, record)
}

func (s *Service) EvaluateGoal(ctx context.Context, scope, goalID string, input EvaluateGoalInput) (Goal, error) {
	collection, normalizedScope, err := collectionForScope(scope)
	if err != nil {
		return Goal{}, err
	}
	record, err := s.app.FindRecordById(collection, goalID)
	if err != nil {
		return Goal{}, fmt.Errorf("find goal: %w", err)
	}

	evaluation := cloneMap(input.Evaluation)
	if evaluation == nil {
		evaluation = map[string]any{}
	}
	now := time.Now().UTC()
	evaluation["evaluated_at"] = now.Format(time.RFC3339Nano)
	if runID := strings.TrimSpace(input.EvaluatedByRunID); runID != "" {
		evaluation["run_id"] = runID
		record.Set("last_evaluated_run_id", runID)
	}

	history := []map[string]any{}
	if err := decodeJSONField(record, "evaluation_history_json", &history); err != nil {
		return Goal{}, err
	}
	history = append(history, evaluation)
	if err := setJSON(record, "last_evaluation_json", evaluation); err != nil {
		return Goal{}, err
	}
	if err := setJSON(record, "evaluation_history_json", history); err != nil {
		return Goal{}, err
	}
	if value := strings.TrimSpace(input.Status); value != "" {
		record.Set("status", value)
	}
	if value := strings.TrimSpace(input.UpdatedByRunID); value != "" {
		record.Set("updated_by_run_id", value)
	}
	record.Set("version", record.GetInt("version")+1)
	if err := setTime(record, "last_evaluated_at", now); err != nil {
		return Goal{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Goal{}, fmt.Errorf("evaluate goal: %w", err)
	}
	return goalFromRecord(normalizedScope, record)
}

func (s *Service) newRecord(collection string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

func collectionForScope(scope string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ScopeSession:
		return CollectionSessionGoals, ScopeSession, nil
	case ScopeProfile:
		return CollectionProfileGoals, ScopeProfile, nil
	default:
		return "", "", fmt.Errorf("unsupported goal scope %q", scope)
	}
}

func goalFromRecord(scope string, record *core.Record) (Goal, error) {
	item := Goal{
		ID:                 record.Id,
		Scope:              scope,
		ProfileID:          record.GetString("profile_id"),
		SessionID:          record.GetString("session_id"),
		Title:              record.GetString("title"),
		Statement:          record.GetString("statement"),
		SuccessCriteria:    record.GetString("success_criteria"),
		Status:             record.GetString("status"),
		Priority:           record.GetString("priority"),
		CreatedByRunID:     record.GetString("created_by_run_id"),
		UpdatedByRunID:     record.GetString("updated_by_run_id"),
		ClearedByRunID:     record.GetString("cleared_by_run_id"),
		LastEvaluatedRunID: record.GetString("last_evaluated_run_id"),
		Version:            record.GetInt("version"),
		ClearedAt:          record.GetDateTime("cleared_at").Time(),
		LastEvaluatedAt:    record.GetDateTime("last_evaluated_at").Time(),
		CreatedAt:          record.GetDateTime("created").Time(),
		UpdatedAt:          record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "tags_json", &item.Tags); err != nil {
		return Goal{}, err
	}
	if err := decodeJSONField(record, "state_json", &item.State); err != nil {
		return Goal{}, err
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Goal{}, err
	}
	if err := decodeJSONField(record, "last_evaluation_json", &item.LastEvaluation); err != nil {
		return Goal{}, err
	}
	if err := decodeJSONField(record, "evaluation_history_json", &item.EvaluationHistory); err != nil {
		return Goal{}, err
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

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func setTime(record *core.Record, field string, value time.Time) error {
	if value.IsZero() {
		record.Set(field, nil)
		return nil
	}
	dt, err := types.ParseDateTime(value)
	if err != nil {
		return fmt.Errorf("parse %s: %w", field, err)
	}
	record.Set(field, dt)
	return nil
}
