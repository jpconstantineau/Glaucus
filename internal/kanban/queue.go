package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type runExecutor interface {
	QueueRun(context.Context, runtime.ExecuteRunInput) (sessions.Run, error)
	ProcessRun(context.Context, sessions.Run, runtime.ExecuteRunInput) (runtime.ExecuteRunResult, error)
	CancelRun(context.Context, string) (sessions.Run, error)
}

type QueueManager struct {
	kanban       *Service
	sessions     *sessions.Service
	orchestrator runExecutor
	now          func() time.Time
}

type DispatchInput struct {
	TaskID           string
	Actor            string
	ParentRunID      string
	ProviderID       string
	ModelID          string
	Prompt           string
	ApprovalMode     string
	ToolResolution   tools.Resolution
	WorkingDirectory string
}

type DispatchResult struct {
	Task   Task
	Run    sessions.Run
	Result runtime.ExecuteRunResult
}

func NewQueueManager(kanbanService *Service, sessionService *sessions.Service, orchestrator runExecutor) *QueueManager {
	return &QueueManager{
		kanban:       kanbanService,
		sessions:     sessionService,
		orchestrator: orchestrator,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (m *QueueManager) QueueTaskRun(ctx context.Context, input DispatchInput) (Task, sessions.Run, runtime.ExecuteRunInput, error) {
	if m == nil || m.kanban == nil || m.sessions == nil || m.orchestrator == nil {
		return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, errors.New("queue manager is not configured")
	}

	task, err := m.kanban.GetTask(ctx, input.TaskID)
	if err != nil {
		return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, err
	}

	sessionID := task.SessionID
	if strings.TrimSpace(sessionID) == "" {
		session, err := m.sessions.CreateSession(ctx, sessions.CreateSessionInput{
			ProfileID: task.ProfileID,
			Source:    "kanban",
			Title:     "Kanban: " + task.Title,
			Status:    "active",
			Metadata: map[string]any{
				"board_id": task.BoardID,
				"task_id":  task.ID,
			},
		})
		if err != nil {
			return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, fmt.Errorf("create task session: %w", err)
		}
		sessionID = session.ID
	}

	parentRunID := firstNonEmpty(input.ParentRunID, task.ParentRunID)
	execInput, err := m.buildExecuteInput(task, sessionID, parentRunID, input)
	if err != nil {
		return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, err
	}

	run, err := m.orchestrator.QueueRun(ctx, execInput)
	if err != nil {
		return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, fmt.Errorf("queue delegated run: %w", err)
	}

	queuedAt := m.now()
	noError := ""
	updatedTask, err := m.kanban.UpdateTask(ctx, task.ID, UpdateTaskInput{
		Status:      TaskStatusReady,
		QueueState:  QueueStateQueued,
		SessionID:   sessionID,
		ParentRunID: parentRunID,
		LatestRunID: run.ID,
		LastError:   &noError,
		StartedAt:   &queuedAt,
		CompletedAt: &time.Time{},
		RetryCount:  intPtr(task.RetryCount),
	})
	if err != nil {
		return Task{}, sessions.Run{}, runtime.ExecuteRunInput{}, fmt.Errorf("update queued task: %w", err)
	}

	_, _ = m.kanban.AddComment(context.Background(), AddCommentInput{
		ProfileID: task.ProfileID,
		BoardID:   task.BoardID,
		TaskID:    task.ID,
		RunID:     run.ID,
		Author:    firstNonEmpty(input.Actor, "system"),
		Kind:      CommentKindEvent,
		Body:      fmt.Sprintf("Queued delegated run %s for task %s.", run.ID, task.Title),
		Metadata: map[string]any{
			"state":         QueueStateQueued,
			"parent_run_id": parentRunID,
		},
	})

	return updatedTask, run, execInput, nil
}

func (m *QueueManager) DispatchTaskRun(ctx context.Context, input DispatchInput) (DispatchResult, error) {
	task, run, execInput, err := m.QueueTaskRun(ctx, input)
	if err != nil {
		return DispatchResult{}, err
	}

	runningAt := m.now()
	noError := ""
	task, err = m.kanban.UpdateTask(ctx, task.ID, UpdateTaskInput{
		Status:      TaskStatusInProgress,
		QueueState:  QueueStateRunning,
		StartedAt:   &runningAt,
		CompletedAt: &time.Time{},
		LastError:   &noError,
	})
	if err != nil {
		return DispatchResult{}, fmt.Errorf("mark task running: %w", err)
	}

	result, execErr := m.orchestrator.ProcessRun(ctx, run, execInput)
	finalTask, taskErr := m.finishTask(ctx, task, result.Run, input.Actor, execErr)
	if taskErr != nil {
		return DispatchResult{}, taskErr
	}
	if execErr != nil {
		return DispatchResult{Task: finalTask, Run: run, Result: result}, execErr
	}
	return DispatchResult{Task: finalTask, Run: run, Result: result}, nil
}

func (m *QueueManager) RetryTaskRun(ctx context.Context, input DispatchInput) (DispatchResult, error) {
	task, err := m.kanban.GetTask(ctx, input.TaskID)
	if err != nil {
		return DispatchResult{}, err
	}
	nextRetry := task.RetryCount + 1
	if _, err := m.kanban.UpdateTask(ctx, task.ID, UpdateTaskInput{
		RetryCount: &nextRetry,
	}); err != nil {
		return DispatchResult{}, fmt.Errorf("increment retry count: %w", err)
	}
	return m.DispatchTaskRun(ctx, input)
}

func (m *QueueManager) CancelTaskRun(ctx context.Context, taskID, actor string) (Task, sessions.Run, error) {
	if m == nil || m.kanban == nil || m.orchestrator == nil {
		return Task{}, sessions.Run{}, errors.New("queue manager is not configured")
	}

	task, err := m.kanban.GetTask(ctx, taskID)
	if err != nil {
		return Task{}, sessions.Run{}, err
	}
	if strings.TrimSpace(task.LatestRunID) == "" {
		return Task{}, sessions.Run{}, errors.New("task does not have a linked run")
	}

	run, err := m.orchestrator.CancelRun(ctx, task.LatestRunID)
	if err != nil {
		return Task{}, sessions.Run{}, fmt.Errorf("cancel delegated run: %w", err)
	}

	completedAt := m.now()
	cancelledMessage := "run cancelled"
	updatedTask, err := m.kanban.UpdateTask(ctx, task.ID, UpdateTaskInput{
		Status:      TaskStatusCancelled,
		QueueState:  QueueStateCancelled,
		CompletedAt: &completedAt,
		LastError:   &cancelledMessage,
	})
	if err != nil {
		return Task{}, sessions.Run{}, fmt.Errorf("mark task cancelled: %w", err)
	}

	_, _ = m.kanban.AddComment(context.Background(), AddCommentInput{
		ProfileID: task.ProfileID,
		BoardID:   task.BoardID,
		TaskID:    task.ID,
		RunID:     run.ID,
		Author:    firstNonEmpty(actor, "system"),
		Kind:      CommentKindEvent,
		Body:      fmt.Sprintf("Cancelled delegated run %s.", run.ID),
		Metadata: map[string]any{
			"state": QueueStateCancelled,
		},
	})

	return updatedTask, run, nil
}

func (m *QueueManager) buildExecuteInput(task Task, sessionID, parentRunID string, input DispatchInput) (runtime.ExecuteRunInput, error) {
	providerID := strings.TrimSpace(input.ProviderID)
	modelID := strings.TrimSpace(input.ModelID)
	if providerID == "" || modelID == "" {
		return runtime.ExecuteRunInput{}, errors.New("provider id and model id are required to dispatch a task")
	}

	promptText := strings.TrimSpace(firstNonEmpty(input.Prompt, task.DelegationPrompt, task.Description, task.Title))
	if promptText == "" {
		return runtime.ExecuteRunInput{}, errors.New("task prompt is required to dispatch a task")
	}

	return runtime.ExecuteRunInput{
		ProfileID:      task.ProfileID,
		SessionID:      sessionID,
		ParentRunID:    parentRunID,
		TriggerSource:  "kanban_queue",
		Surface:        tools.SurfaceWebAdmin,
		Actor:          firstNonEmpty(input.Actor, "system"),
		ApprovalMode:   input.ApprovalMode,
		ToolResolution: input.ToolResolution,
		Prompt: runtime.PromptDocument{
			Fragments: []runtime.PromptFragment{
				{Name: "identity", Priority: 10, Content: "You are executing a delegated kanban queue task."},
				{Name: "task_context", Priority: 20, Content: "Board task: " + task.Title},
			},
		},
		Request: providers.NormalizedRequest{
			Messages:     []providers.RequestMessage{{Role: "user", Content: promptText}},
			RequiredCaps: []string{"chat"},
		},
		Resolution: providers.ResolutionInput{
			ProviderID:           providerID,
			ModelID:              modelID,
			RequiredCapabilities: []string{"chat"},
		},
		WorkingDirectory: input.WorkingDirectory,
	}, nil
}

func (m *QueueManager) finishTask(ctx context.Context, task Task, run sessions.Run, actor string, execErr error) (Task, error) {
	completedAt := m.now()
	update := UpdateTaskInput{
		LatestRunID: run.ID,
		CompletedAt: &completedAt,
	}

	commentBody := ""
	commentMetadata := map[string]any{
		"run_status": run.Status,
	}
	switch run.Status {
	case runtime.RunStatusCompleted:
		noError := ""
		update.Status = TaskStatusReview
		update.QueueState = QueueStateCompleted
		update.LastError = &noError
		commentBody = fmt.Sprintf("Delegated run %s completed successfully.", run.ID)
	case runtime.RunStatusCancelled:
		message := "run cancelled"
		update.Status = TaskStatusCancelled
		update.QueueState = QueueStateCancelled
		update.LastError = &message
		commentBody = fmt.Sprintf("Delegated run %s was cancelled.", run.ID)
	default:
		message := strings.TrimSpace(run.ErrorMessage)
		if message == "" && execErr != nil {
			message = execErr.Error()
		}
		update.Status = TaskStatusReady
		update.QueueState = QueueStateFailed
		update.LastError = &message
		commentBody = fmt.Sprintf("Delegated run %s failed: %s", run.ID, message)
		commentMetadata["error"] = message
	}

	updatedTask, err := m.kanban.UpdateTask(ctx, task.ID, update)
	if err != nil {
		return Task{}, fmt.Errorf("update task completion: %w", err)
	}

	_, _ = m.kanban.AddComment(context.Background(), AddCommentInput{
		ProfileID: task.ProfileID,
		BoardID:   task.BoardID,
		TaskID:    task.ID,
		RunID:     run.ID,
		Author:    firstNonEmpty(actor, "system"),
		Kind:      CommentKindEvent,
		Body:      commentBody,
		Metadata:  commentMetadata,
	})

	return updatedTask, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func intPtr(value int) *int {
	return &value
}
