package sessions

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
	CollectionSessions = "agent_sessions"
	CollectionMessages = "agent_messages"
	CollectionRuns     = "agent_runs"
)

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type MessageContent []ContentPart

type Session struct {
	ID              string
	ProfileID       string
	Source          string
	Title           string
	ParentSessionID string
	Status          string
	ModelSnapshot   map[string]any
	ToolsetSnapshot map[string]any
	LastMessageAt   time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Message struct {
	ID          string
	ProfileID   string
	SessionID   string
	RunID       string
	Role        string
	Ordinal     int
	Content     MessageContent
	VisibleText string
	ToolCalls   map[string]any
	ToolResults map[string]any
	Usage       map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Run struct {
	ID                 string
	ProfileID          string
	SessionID          string
	ParentRunID        string
	TriggerSource      string
	Status             string
	Request            map[string]any
	ProviderResolution map[string]any
	WorkingDirectory   string
	StartedAt          time.Time
	EndedAt            time.Time
	ErrorCode          string
	ErrorMessage       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreateSessionInput struct {
	ProfileID       string
	Source          string
	Title           string
	ParentSessionID string
	Status          string
	ModelSnapshot   map[string]any
	ToolsetSnapshot map[string]any
}

type CreateMessageInput struct {
	ProfileID   string
	SessionID   string
	RunID       string
	Role        string
	Content     MessageContent
	VisibleText string
	ToolCalls   map[string]any
	ToolResults map[string]any
	Usage       map[string]any
}

type CreateRunInput struct {
	ProfileID          string
	SessionID          string
	ParentRunID        string
	TriggerSource      string
	Status             string
	Request            map[string]any
	ProviderResolution map[string]any
	WorkingDirectory   string
	StartedAt          time.Time
	EndedAt            time.Time
	ErrorCode          string
	ErrorMessage       string
}

type UpdateRunInput struct {
	Status             string
	ProviderResolution map[string]any
	EndedAt            time.Time
	ErrorCode          string
	ErrorMessage       string
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func (s *Service) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Session{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.Source) == "" {
		return Session{}, errors.New("source is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Session{}, errors.New("title is required")
	}
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "active"
	}

	record, err := s.newRecord(CollectionSessions)
	if err != nil {
		return Session{}, err
	}

	record.Set("profile_id", input.ProfileID)
	record.Set("source", input.Source)
	record.Set("title", input.Title)
	record.Set("parent_session_id", input.ParentSessionID)
	record.Set("status", input.Status)
	if err := setJSON(record, "model_snapshot_json", input.ModelSnapshot); err != nil {
		return Session{}, err
	}
	if err := setJSON(record, "toolset_snapshot_json", input.ToolsetSnapshot); err != nil {
		return Session{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Session{}, fmt.Errorf("save session: %w", err)
	}

	return sessionFromRecord(record)
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (Session, error) {
	record, err := s.app.FindRecordById(CollectionSessions, sessionID)
	if err != nil {
		return Session{}, fmt.Errorf("find session: %w", err)
	}
	_ = ctx
	return sessionFromRecord(record)
}

func (s *Service) ListSessions(ctx context.Context, profileID string, limit int) ([]Session, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionSessions,
		"profile_id = {:profile_id}",
		"-last_message_at",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	sessions := make([]Session, 0, len(records))
	for _, record := range records {
		session, err := sessionFromRecord(record)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	_ = ctx
	return sessions, nil
}

func (s *Service) CreateMessage(ctx context.Context, input CreateMessageInput) (Message, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Message{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return Message{}, errors.New("session id is required")
	}
	if strings.TrimSpace(input.Role) == "" {
		return Message{}, errors.New("role is required")
	}
	if len(input.Content) == 0 {
		return Message{}, errors.New("content is required")
	}

	var message Message
	err := s.app.RunInTransaction(func(txApp core.App) error {
		service := NewService(txApp)
		ordinal, err := service.nextOrdinal(input.SessionID)
		if err != nil {
			return err
		}

		record, err := service.newRecord(CollectionMessages)
		if err != nil {
			return err
		}

		record.Set("profile_id", input.ProfileID)
		record.Set("session_id", input.SessionID)
		record.Set("run_id", input.RunID)
		record.Set("role", input.Role)
		record.Set("ordinal", ordinal)
		record.Set("visible_text", input.VisibleText)
		if err := setJSON(record, "content_json", input.Content); err != nil {
			return err
		}
		if err := setJSON(record, "tool_calls_json", input.ToolCalls); err != nil {
			return err
		}
		if err := setJSON(record, "tool_results_json", input.ToolResults); err != nil {
			return err
		}
		if err := setJSON(record, "usage_json", input.Usage); err != nil {
			return err
		}

		if err := txApp.SaveWithContext(ctx, record); err != nil {
			return fmt.Errorf("save message: %w", err)
		}

		sessionRecord, err := txApp.FindRecordById(CollectionSessions, input.SessionID)
		if err != nil {
			return fmt.Errorf("find parent session: %w", err)
		}
		sessionRecord.Set("last_message_at", types.NowDateTime())
		if err := txApp.SaveWithContext(ctx, sessionRecord); err != nil {
			return fmt.Errorf("touch parent session: %w", err)
		}

		message, err = messageFromRecord(record)
		return err
	})
	if err != nil {
		return Message{}, err
	}

	return message, nil
}

func (s *Service) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session id is required")
	}

	records, err := s.app.FindRecordsByFilter(
		CollectionMessages,
		"session_id = {:session_id}",
		"ordinal",
		0,
		0,
		dbx.Params{"session_id": sessionID},
	)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	messages := make([]Message, 0, len(records))
	for _, record := range records {
		message, err := messageFromRecord(record)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}

	_ = ctx
	return messages, nil
}

func (s *Service) CreateRun(ctx context.Context, input CreateRunInput) (Run, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Run{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return Run{}, errors.New("session id is required")
	}
	if strings.TrimSpace(input.TriggerSource) == "" {
		return Run{}, errors.New("trigger source is required")
	}
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "queued"
	}

	record, err := s.newRecord(CollectionRuns)
	if err != nil {
		return Run{}, err
	}

	record.Set("profile_id", input.ProfileID)
	record.Set("session_id", input.SessionID)
	record.Set("parent_run_id", input.ParentRunID)
	record.Set("trigger_source", input.TriggerSource)
	record.Set("status", input.Status)
	record.Set("working_directory", input.WorkingDirectory)
	record.Set("error_code", input.ErrorCode)
	record.Set("error_message", input.ErrorMessage)
	if !input.StartedAt.IsZero() {
		dt, err := types.ParseDateTime(input.StartedAt)
		if err != nil {
			return Run{}, fmt.Errorf("parse started_at: %w", err)
		}
		record.Set("started_at", dt)
	}
	if !input.EndedAt.IsZero() {
		dt, err := types.ParseDateTime(input.EndedAt)
		if err != nil {
			return Run{}, fmt.Errorf("parse ended_at: %w", err)
		}
		record.Set("ended_at", dt)
	}
	if err := setJSON(record, "request_json", input.Request); err != nil {
		return Run{}, err
	}
	if err := setJSON(record, "provider_resolution_json", input.ProviderResolution); err != nil {
		return Run{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Run{}, fmt.Errorf("save run: %w", err)
	}

	return runFromRecord(record)
}

func (s *Service) GetRun(ctx context.Context, runID string) (Run, error) {
	record, err := s.app.FindRecordById(CollectionRuns, runID)
	if err != nil {
		return Run{}, fmt.Errorf("find run: %w", err)
	}
	_ = ctx
	return runFromRecord(record)
}

func (s *Service) UpdateRun(ctx context.Context, runID string, input UpdateRunInput) (Run, error) {
	record, err := s.app.FindRecordById(CollectionRuns, runID)
	if err != nil {
		return Run{}, fmt.Errorf("find run: %w", err)
	}

	if strings.TrimSpace(input.Status) != "" {
		record.Set("status", input.Status)
	}
	if !input.EndedAt.IsZero() {
		dt, err := types.ParseDateTime(input.EndedAt)
		if err != nil {
			return Run{}, fmt.Errorf("parse ended_at: %w", err)
		}
		record.Set("ended_at", dt)
	}
	if input.ErrorCode != "" {
		record.Set("error_code", input.ErrorCode)
	}
	if input.ErrorMessage != "" {
		record.Set("error_message", input.ErrorMessage)
	}
	if input.ProviderResolution != nil {
		if err := setJSON(record, "provider_resolution_json", input.ProviderResolution); err != nil {
			return Run{}, err
		}
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Run{}, fmt.Errorf("update run: %w", err)
	}

	return runFromRecord(record)
}

func (s *Service) ListRuns(ctx context.Context, sessionID string) ([]Run, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session id is required")
	}

	records, err := s.app.FindRecordsByFilter(
		CollectionRuns,
		"session_id = {:session_id}",
		"-started_at",
		0,
		0,
		dbx.Params{"session_id": sessionID},
	)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}

	runs := make([]Run, 0, len(records))
	for _, record := range records {
		run, err := runFromRecord(record)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	_ = ctx
	return runs, nil
}

func (s *Service) nextOrdinal(sessionID string) (int, error) {
	records, err := s.app.FindRecordsByFilter(
		CollectionMessages,
		"session_id = {:session_id}",
		"-ordinal",
		1,
		0,
		dbx.Params{"session_id": sessionID},
	)
	if err != nil {
		return 0, fmt.Errorf("find next ordinal: %w", err)
	}
	if len(records) == 0 {
		return 1, nil
	}
	return records[0].GetInt("ordinal") + 1, nil
}

func (s *Service) newRecord(collection string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

func sessionFromRecord(record *core.Record) (Session, error) {
	var session Session
	session.ID = record.Id
	session.ProfileID = record.GetString("profile_id")
	session.Source = record.GetString("source")
	session.Title = record.GetString("title")
	session.ParentSessionID = record.GetString("parent_session_id")
	session.Status = record.GetString("status")
	session.LastMessageAt = record.GetDateTime("last_message_at").Time()
	session.CreatedAt = record.GetDateTime("created").Time()
	session.UpdatedAt = record.GetDateTime("updated").Time()
	if err := decodeJSONField(record, "model_snapshot_json", &session.ModelSnapshot); err != nil {
		return Session{}, err
	}
	if err := decodeJSONField(record, "toolset_snapshot_json", &session.ToolsetSnapshot); err != nil {
		return Session{}, err
	}
	return session, nil
}

func messageFromRecord(record *core.Record) (Message, error) {
	var message Message
	message.ID = record.Id
	message.ProfileID = record.GetString("profile_id")
	message.SessionID = record.GetString("session_id")
	message.RunID = record.GetString("run_id")
	message.Role = record.GetString("role")
	message.Ordinal = record.GetInt("ordinal")
	message.VisibleText = record.GetString("visible_text")
	message.CreatedAt = record.GetDateTime("created").Time()
	message.UpdatedAt = record.GetDateTime("updated").Time()
	if err := decodeJSONField(record, "content_json", &message.Content); err != nil {
		return Message{}, err
	}
	if err := decodeJSONField(record, "tool_calls_json", &message.ToolCalls); err != nil {
		return Message{}, err
	}
	if err := decodeJSONField(record, "tool_results_json", &message.ToolResults); err != nil {
		return Message{}, err
	}
	if err := decodeJSONField(record, "usage_json", &message.Usage); err != nil {
		return Message{}, err
	}
	return message, nil
}

func runFromRecord(record *core.Record) (Run, error) {
	var run Run
	run.ID = record.Id
	run.ProfileID = record.GetString("profile_id")
	run.SessionID = record.GetString("session_id")
	run.ParentRunID = record.GetString("parent_run_id")
	run.TriggerSource = record.GetString("trigger_source")
	run.Status = record.GetString("status")
	run.WorkingDirectory = record.GetString("working_directory")
	run.StartedAt = record.GetDateTime("started_at").Time()
	run.EndedAt = record.GetDateTime("ended_at").Time()
	run.ErrorCode = record.GetString("error_code")
	run.ErrorMessage = record.GetString("error_message")
	run.CreatedAt = record.GetDateTime("created").Time()
	run.UpdatedAt = record.GetDateTime("updated").Time()
	if err := decodeJSONField(record, "request_json", &run.Request); err != nil {
		return Run{}, err
	}
	if err := decodeJSONField(record, "provider_resolution_json", &run.ProviderResolution); err != nil {
		return Run{}, err
	}
	return run, nil
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
