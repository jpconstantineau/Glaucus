package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	CollectionBoards   = "kanban_boards"
	CollectionTasks    = "kanban_tasks"
	CollectionComments = "kanban_comments"

	BoardStatusActive   = "active"
	BoardStatusArchived = "archived"

	TaskStatusBacklog    = "backlog"
	TaskStatusReady      = "ready"
	TaskStatusInProgress = "in_progress"
	TaskStatusReview     = "review"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"

	QueueStateIdle      = "idle"
	QueueStateQueued    = "queued"
	QueueStateRunning   = "running"
	QueueStateFailed    = "failed"
	QueueStateCompleted = "completed"
	QueueStateCancelled = "cancelled"

	CommentKindNote  = "note"
	CommentKindEvent = "event"
)

type Board struct {
	ID          string
	ProfileID   string
	Name        string
	Slug        string
	Description string
	Status      string
	Owner       string
	WIPLimit    int
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Task struct {
	ID               string
	ProfileID        string
	BoardID          string
	ParentTaskID     string
	Title            string
	Description      string
	Status           string
	QueueState       string
	Priority         string
	Position         int
	Owner            string
	Assignee         string
	SessionID        string
	ParentRunID      string
	LatestRunID      string
	DelegationPrompt string
	LastError        string
	RetryCount       int
	Metadata         map[string]any
	DueAt            time.Time
	StartedAt        time.Time
	CompletedAt      time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Comment struct {
	ID        string
	ProfileID string
	BoardID   string
	TaskID    string
	RunID     string
	Author    string
	Kind      string
	Body      string
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateBoardInput struct {
	ProfileID   string
	Name        string
	Slug        string
	Description string
	Status      string
	Owner       string
	WIPLimit    int
	Metadata    map[string]any
}

type CreateTaskInput struct {
	ProfileID        string
	BoardID          string
	ParentTaskID     string
	Title            string
	Description      string
	Status           string
	QueueState       string
	Priority         string
	Position         int
	Owner            string
	Assignee         string
	SessionID        string
	ParentRunID      string
	LatestRunID      string
	DelegationPrompt string
	LastError        string
	RetryCount       int
	Metadata         map[string]any
	DueAt            time.Time
	StartedAt        time.Time
	CompletedAt      time.Time
}

type UpdateTaskInput struct {
	Title            string
	Description      string
	Status           string
	QueueState       string
	Priority         string
	Position         *int
	Owner            string
	Assignee         string
	SessionID        string
	ParentRunID      string
	LatestRunID      string
	DelegationPrompt string
	LastError        string
	RetryCount       *int
	Metadata         map[string]any
	DueAt            *time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
}

type AddCommentInput struct {
	ProfileID string
	BoardID   string
	TaskID    string
	RunID     string
	Author    string
	Kind      string
	Body      string
	Metadata  map[string]any
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func (s *Service) CreateBoard(ctx context.Context, input CreateBoardInput) (Board, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Board{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return Board{}, errors.New("name is required")
	}

	record, err := s.newRecord(CollectionBoards)
	if err != nil {
		return Board{}, err
	}

	record.Set("profile_id", strings.TrimSpace(input.ProfileID))
	record.Set("name", strings.TrimSpace(input.Name))
	record.Set("slug", normalizeSlug(input.Slug, input.Name))
	record.Set("description", strings.TrimSpace(input.Description))
	record.Set("status", defaultString(input.Status, BoardStatusActive))
	record.Set("owner", strings.TrimSpace(input.Owner))
	record.Set("wip_limit", maxInt(input.WIPLimit, 0))
	if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
		return Board{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Board{}, fmt.Errorf("save board: %w", err)
	}
	return boardFromRecord(record)
}

func (s *Service) ListBoards(ctx context.Context, profileID string, limit int) ([]Board, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionBoards,
		"profile_id = {:profile_id}",
		"name",
		limit,
		0,
		dbx.Params{"profile_id": strings.TrimSpace(profileID)},
	)
	if err != nil {
		return nil, fmt.Errorf("list boards: %w", err)
	}
	items := make([]Board, 0, len(records))
	for _, record := range records {
		item, err := boardFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) GetBoard(ctx context.Context, boardID string) (Board, error) {
	record, err := s.app.FindRecordById(CollectionBoards, boardID)
	if err != nil {
		return Board{}, fmt.Errorf("find board: %w", err)
	}
	_ = ctx
	return boardFromRecord(record)
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (Task, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Task{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.BoardID) == "" {
		return Task{}, errors.New("board id is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return Task{}, errors.New("title is required")
	}

	record, err := s.newRecord(CollectionTasks)
	if err != nil {
		return Task{}, err
	}

	record.Set("profile_id", strings.TrimSpace(input.ProfileID))
	record.Set("board_id", strings.TrimSpace(input.BoardID))
	record.Set("parent_task_id", strings.TrimSpace(input.ParentTaskID))
	record.Set("title", strings.TrimSpace(input.Title))
	record.Set("description", strings.TrimSpace(input.Description))
	record.Set("status", defaultString(input.Status, TaskStatusBacklog))
	record.Set("queue_state", defaultString(input.QueueState, QueueStateIdle))
	record.Set("priority", defaultString(input.Priority, "normal"))
	record.Set("position", maxInt(input.Position, 0))
	record.Set("owner", strings.TrimSpace(input.Owner))
	record.Set("assignee", strings.TrimSpace(input.Assignee))
	record.Set("session_id", strings.TrimSpace(input.SessionID))
	record.Set("parent_run_id", strings.TrimSpace(input.ParentRunID))
	record.Set("latest_run_id", strings.TrimSpace(input.LatestRunID))
	record.Set("delegation_prompt", strings.TrimSpace(input.DelegationPrompt))
	record.Set("last_error", strings.TrimSpace(input.LastError))
	record.Set("retry_count", maxInt(input.RetryCount, 0))
	if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
		return Task{}, err
	}
	if err := setTime(record, "due_at", input.DueAt); err != nil {
		return Task{}, err
	}
	if err := setTime(record, "started_at", input.StartedAt); err != nil {
		return Task{}, err
	}
	if err := setTime(record, "completed_at", input.CompletedAt); err != nil {
		return Task{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Task{}, fmt.Errorf("save task: %w", err)
	}
	return taskFromRecord(record)
}

func (s *Service) GetTask(ctx context.Context, taskID string) (Task, error) {
	record, err := s.app.FindRecordById(CollectionTasks, taskID)
	if err != nil {
		return Task{}, fmt.Errorf("find task: %w", err)
	}
	_ = ctx
	return taskFromRecord(record)
}

func (s *Service) ListTasksByBoard(ctx context.Context, boardID string) ([]Task, error) {
	if strings.TrimSpace(boardID) == "" {
		return nil, errors.New("board id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionTasks,
		"board_id = {:board_id}",
		"position,title",
		0,
		0,
		dbx.Params{"board_id": strings.TrimSpace(boardID)},
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks by board: %w", err)
	}
	items := make([]Task, 0, len(records))
	for _, record := range records {
		item, err := taskFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) ListActiveTasks(ctx context.Context, profileID string, limit int) ([]Task, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionTasks,
		"profile_id = {:profile_id} && (queue_state = 'queued' || queue_state = 'running')",
		"position,title",
		limit,
		0,
		dbx.Params{"profile_id": strings.TrimSpace(profileID)},
	)
	if err != nil {
		return nil, fmt.Errorf("list active tasks: %w", err)
	}
	items := make([]Task, 0, len(records))
	for _, record := range records {
		item, err := taskFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	_ = ctx
	return items, nil
}

func (s *Service) UpdateTask(ctx context.Context, taskID string, input UpdateTaskInput) (Task, error) {
	record, err := s.app.FindRecordById(CollectionTasks, taskID)
	if err != nil {
		return Task{}, fmt.Errorf("find task: %w", err)
	}

	if value := strings.TrimSpace(input.Title); value != "" {
		record.Set("title", value)
	}
	if value := strings.TrimSpace(input.Description); value != "" {
		record.Set("description", value)
	}
	if value := strings.TrimSpace(input.Status); value != "" {
		record.Set("status", value)
	}
	if value := strings.TrimSpace(input.QueueState); value != "" {
		record.Set("queue_state", value)
	}
	if value := strings.TrimSpace(input.Priority); value != "" {
		record.Set("priority", value)
	}
	if input.Position != nil {
		record.Set("position", maxInt(*input.Position, 0))
	}
	if value := strings.TrimSpace(input.Owner); value != "" {
		record.Set("owner", value)
	}
	if value := strings.TrimSpace(input.Assignee); value != "" {
		record.Set("assignee", value)
	}
	if value := strings.TrimSpace(input.SessionID); value != "" {
		record.Set("session_id", value)
	}
	if value := strings.TrimSpace(input.ParentRunID); value != "" {
		record.Set("parent_run_id", value)
	}
	if value := strings.TrimSpace(input.LatestRunID); value != "" {
		record.Set("latest_run_id", value)
	}
	if input.DelegationPrompt != "" {
		record.Set("delegation_prompt", strings.TrimSpace(input.DelegationPrompt))
	}
	if input.LastError != "" {
		record.Set("last_error", strings.TrimSpace(input.LastError))
	}
	if input.RetryCount != nil {
		record.Set("retry_count", maxInt(*input.RetryCount, 0))
	}
	if input.Metadata != nil {
		if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
			return Task{}, err
		}
	}
	if input.DueAt != nil {
		record.Set("due_at", nil)
		if err := setTime(record, "due_at", *input.DueAt); err != nil {
			return Task{}, err
		}
	}
	if input.StartedAt != nil {
		record.Set("started_at", nil)
		if err := setTime(record, "started_at", *input.StartedAt); err != nil {
			return Task{}, err
		}
	}
	if input.CompletedAt != nil {
		record.Set("completed_at", nil)
		if err := setTime(record, "completed_at", *input.CompletedAt); err != nil {
			return Task{}, err
		}
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	return taskFromRecord(record)
}

func (s *Service) AddComment(ctx context.Context, input AddCommentInput) (Comment, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Comment{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.BoardID) == "" {
		return Comment{}, errors.New("board id is required")
	}
	if strings.TrimSpace(input.TaskID) == "" {
		return Comment{}, errors.New("task id is required")
	}
	if strings.TrimSpace(input.Author) == "" {
		return Comment{}, errors.New("author is required")
	}
	if strings.TrimSpace(input.Body) == "" {
		return Comment{}, errors.New("body is required")
	}

	record, err := s.newRecord(CollectionComments)
	if err != nil {
		return Comment{}, err
	}

	record.Set("profile_id", strings.TrimSpace(input.ProfileID))
	record.Set("board_id", strings.TrimSpace(input.BoardID))
	record.Set("task_id", strings.TrimSpace(input.TaskID))
	record.Set("run_id", strings.TrimSpace(input.RunID))
	record.Set("author", strings.TrimSpace(input.Author))
	record.Set("kind", defaultString(input.Kind, CommentKindNote))
	record.Set("body", strings.TrimSpace(input.Body))
	if err := setJSON(record, "metadata_json", input.Metadata); err != nil {
		return Comment{}, err
	}

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Comment{}, fmt.Errorf("save comment: %w", err)
	}
	return commentFromRecord(record)
}

func (s *Service) ListCommentsByTask(ctx context.Context, taskID string) ([]Comment, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("task id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionComments,
		"task_id = {:task_id}",
		"",
		0,
		0,
		dbx.Params{"task_id": strings.TrimSpace(taskID)},
	)
	if err != nil {
		return nil, fmt.Errorf("list comments by task: %w", err)
	}
	items := make([]Comment, 0, len(records))
	for _, record := range records {
		item, err := commentFromRecord(record)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	_ = ctx
	return items, nil
}

func (s *Service) newRecord(collection string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, fmt.Errorf("find collection %s: %w", collection, err)
	}
	return core.NewRecord(col), nil
}

func boardFromRecord(record *core.Record) (Board, error) {
	item := Board{
		ID:          record.Id,
		ProfileID:   record.GetString("profile_id"),
		Name:        record.GetString("name"),
		Slug:        record.GetString("slug"),
		Description: record.GetString("description"),
		Status:      record.GetString("status"),
		Owner:       record.GetString("owner"),
		WIPLimit:    record.GetInt("wip_limit"),
		CreatedAt:   record.GetDateTime("created").Time(),
		UpdatedAt:   record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Board{}, err
	}
	return item, nil
}

func taskFromRecord(record *core.Record) (Task, error) {
	item := Task{
		ID:               record.Id,
		ProfileID:        record.GetString("profile_id"),
		BoardID:          record.GetString("board_id"),
		ParentTaskID:     record.GetString("parent_task_id"),
		Title:            record.GetString("title"),
		Description:      record.GetString("description"),
		Status:           record.GetString("status"),
		QueueState:       record.GetString("queue_state"),
		Priority:         record.GetString("priority"),
		Position:         record.GetInt("position"),
		Owner:            record.GetString("owner"),
		Assignee:         record.GetString("assignee"),
		SessionID:        record.GetString("session_id"),
		ParentRunID:      record.GetString("parent_run_id"),
		LatestRunID:      record.GetString("latest_run_id"),
		DelegationPrompt: record.GetString("delegation_prompt"),
		LastError:        record.GetString("last_error"),
		RetryCount:       record.GetInt("retry_count"),
		DueAt:            record.GetDateTime("due_at").Time(),
		StartedAt:        record.GetDateTime("started_at").Time(),
		CompletedAt:      record.GetDateTime("completed_at").Time(),
		CreatedAt:        record.GetDateTime("created").Time(),
		UpdatedAt:        record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Task{}, err
	}
	return item, nil
}

func commentFromRecord(record *core.Record) (Comment, error) {
	item := Comment{
		ID:        record.Id,
		ProfileID: record.GetString("profile_id"),
		BoardID:   record.GetString("board_id"),
		TaskID:    record.GetString("task_id"),
		RunID:     record.GetString("run_id"),
		Author:    record.GetString("author"),
		Kind:      record.GetString("kind"),
		Body:      record.GetString("body"),
		CreatedAt: record.GetDateTime("created").Time(),
		UpdatedAt: record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Comment{}, err
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

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func normalizeSlug(slug, name string) string {
	value := strings.ToLower(strings.TrimSpace(slug))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(name))
	}
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteRune(r)
				lastDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "board"
	}
	return result
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}
