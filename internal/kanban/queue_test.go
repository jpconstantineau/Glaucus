package kanban

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
)

func TestQueueManagerDispatchTaskRunPersistsRunLinkage(t *testing.T) {
	app := newKanbanTestApp(t)
	kanbanService := NewService(app)
	sessionService := sessions.NewService(app)
	orch := runtime.NewOrchestrator(sessionService, newQueueTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-primary",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "delegated work complete"},
			}},
		})
	}), runtime.NewEventService(app), nil, nil)
	manager := NewQueueManager(kanbanService, sessionService, orch)

	board, err := kanbanService.CreateBoard(context.Background(), CreateBoardInput{
		ProfileID: "default",
		Name:      "Delegation Board",
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	task, err := kanbanService.CreateTask(context.Background(), CreateTaskInput{
		ProfileID:        "default",
		BoardID:          board.ID,
		Title:            "Investigate flaky queue",
		Description:      "Summarize the worker backlog and propose next steps.",
		DelegationPrompt: "Inspect the queue and produce a concise summary.",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	result, err := manager.DispatchTaskRun(context.Background(), DispatchInput{
		TaskID:           task.ID,
		Actor:            "operator@glaucus.local",
		ParentRunID:      "run_parent_123",
		ProviderID:       "primary",
		ModelID:          "chat-primary",
		WorkingDirectory: "C:/GIT/Glaucus",
	})
	if err != nil {
		t.Fatalf("dispatch task run: %v", err)
	}
	if result.Task.QueueState != QueueStateCompleted || result.Task.Status != TaskStatusReview {
		t.Fatalf("expected completed review task, got %+v", result.Task)
	}
	if result.Task.SessionID == "" || result.Task.LatestRunID == "" {
		t.Fatalf("expected linked session and run, got %+v", result.Task)
	}
	run, err := sessionService.GetRun(context.Background(), result.Task.LatestRunID)
	if err != nil {
		t.Fatalf("get delegated run: %v", err)
	}
	if run.ParentRunID != "run_parent_123" || run.TriggerSource != "kanban_queue" {
		t.Fatalf("unexpected delegated run: %+v", run)
	}

	comments, err := kanbanService.ListCommentsByTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("list task comments: %v", err)
	}
	if len(comments) < 2 {
		t.Fatalf("expected audit comments for queue and completion, got %+v", comments)
	}
}

func TestQueueManagerRetryAndCancelRepresentTaskState(t *testing.T) {
	app := newKanbanTestApp(t)
	kanbanService := NewService(app)
	sessionService := sessions.NewService(app)

	firstAttempt := true
	orch := runtime.NewOrchestrator(sessionService, newQueueTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if firstAttempt {
			firstAttempt = false
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-primary",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "retry succeeded"},
			}},
		})
	}), runtime.NewEventService(app), nil, nil)
	manager := NewQueueManager(kanbanService, sessionService, orch)

	board, err := kanbanService.CreateBoard(context.Background(), CreateBoardInput{
		ProfileID: "default",
		Name:      "Failure Board",
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	task, err := kanbanService.CreateTask(context.Background(), CreateTaskInput{
		ProfileID:        "default",
		BoardID:          board.ID,
		Title:            "Recover from failure",
		DelegationPrompt: "Retry after a recoverable error.",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = manager.DispatchTaskRun(context.Background(), DispatchInput{
		TaskID:           task.ID,
		Actor:            "operator@glaucus.local",
		ProviderID:       "primary",
		ModelID:          "chat-primary",
		WorkingDirectory: "C:/GIT/Glaucus",
	})
	if err == nil {
		t.Fatal("expected first dispatch to fail")
	}
	failedTask, err := kanbanService.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("reload failed task: %v", err)
	}
	if failedTask.QueueState != QueueStateFailed || failedTask.Status != TaskStatusReady {
		t.Fatalf("expected failed task to remain ready for retry, got %+v", failedTask)
	}
	if !strings.Contains(failedTask.LastError, "all provider attempts failed") {
		t.Fatalf("expected last error to mention provider failure, got %+v", failedTask)
	}

	retried, err := manager.RetryTaskRun(context.Background(), DispatchInput{
		TaskID:           task.ID,
		Actor:            "operator@glaucus.local",
		ProviderID:       "primary",
		ModelID:          "chat-primary",
		WorkingDirectory: "C:/GIT/Glaucus",
	})
	if err != nil {
		t.Fatalf("retry task run: %v", err)
	}
	if retried.Task.RetryCount != 1 || retried.Task.QueueState != QueueStateCompleted {
		t.Fatalf("expected successful retry state, got %+v", retried.Task)
	}

	queuedTask, run, _, err := manager.QueueTaskRun(context.Background(), DispatchInput{
		TaskID:           task.ID,
		Actor:            "operator@glaucus.local",
		ProviderID:       "primary",
		ModelID:          "chat-primary",
		WorkingDirectory: "C:/GIT/Glaucus",
	})
	if err != nil {
		t.Fatalf("queue task run: %v", err)
	}
	if queuedTask.QueueState != QueueStateQueued {
		t.Fatalf("expected queued task, got %+v", queuedTask)
	}

	cancelledTask, cancelledRun, err := manager.CancelTaskRun(context.Background(), task.ID, "operator@glaucus.local")
	if err != nil {
		t.Fatalf("cancel task run: %v", err)
	}
	if cancelledRun.ID != run.ID {
		t.Fatalf("expected cancelled run %s, got %+v", run.ID, cancelledRun)
	}
	if cancelledTask.QueueState != QueueStateCancelled || cancelledTask.Status != TaskStatusCancelled {
		t.Fatalf("expected cancelled task state, got %+v", cancelledTask)
	}
}

func newQueueTestRouter(t *testing.T, handler http.HandlerFunc) *providers.Router {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return providers.NewRouter(providers.Catalog{
		Entries: []providers.CatalogEntry{{
			ProviderID:   "primary",
			ModelID:      "chat-primary",
			Dialect:      "openai-chat",
			BaseURL:      server.URL,
			Capabilities: []string{"chat"},
		}},
	}, config.Default())
}

func TestQueueManagerRequiresProviderSelection(t *testing.T) {
	app := newKanbanTestApp(t)
	kanbanService := NewService(app)
	sessionService := sessions.NewService(app)
	manager := NewQueueManager(kanbanService, sessionService, stubRunExecutor{})

	board, err := kanbanService.CreateBoard(context.Background(), CreateBoardInput{
		ProfileID: "default",
		Name:      "Validation Board",
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	task, err := kanbanService.CreateTask(context.Background(), CreateTaskInput{
		ProfileID:        "default",
		BoardID:          board.ID,
		Title:            "Validation task",
		DelegationPrompt: "Run without provider should fail.",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = manager.DispatchTaskRun(context.Background(), DispatchInput{TaskID: task.ID})
	if err == nil || !strings.Contains(err.Error(), "provider id and model id are required") {
		t.Fatalf("expected provider/model validation error, got %v", err)
	}
}

type stubRunExecutor struct{}

func (stubRunExecutor) QueueRun(context.Context, runtime.ExecuteRunInput) (sessions.Run, error) {
	return sessions.Run{}, nil
}

func (stubRunExecutor) ProcessRun(context.Context, sessions.Run, runtime.ExecuteRunInput) (runtime.ExecuteRunResult, error) {
	return runtime.ExecuteRunResult{}, nil
}

func (stubRunExecutor) CancelRun(context.Context, string) (sessions.Run, error) {
	return sessions.Run{}, nil
}
