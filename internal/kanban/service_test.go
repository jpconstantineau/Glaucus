package kanban

import (
	"context"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestServiceCreatesBoardsTasksAndComments(t *testing.T) {
	app := newKanbanTestApp(t)
	service := NewService(app)

	board, err := service.CreateBoard(context.Background(), CreateBoardInput{
		ProfileID:   "default",
		Name:        "Launch Board",
		Description: "Track slice 8 rollout",
		Owner:       "operator@glaucus.local",
		WIPLimit:    3,
		Metadata:    map[string]any{"color": "teal"},
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if board.Slug != "launch-board" {
		t.Fatalf("expected normalized slug, got %q", board.Slug)
	}

	dueAt := time.Now().UTC().Add(24 * time.Hour).Round(time.Second)
	task, err := service.CreateTask(context.Background(), CreateTaskInput{
		ProfileID:        "default",
		BoardID:          board.ID,
		Title:            "Dispatch delegated run",
		Description:      "Kick off the child worker for backlog grooming.",
		Status:           TaskStatusReady,
		QueueState:       QueueStateIdle,
		Priority:         "high",
		Position:         20,
		Owner:            "operator@glaucus.local",
		Assignee:         "worker-a",
		DelegationPrompt: "Review the backlog and summarize blockers.",
		DueAt:            dueAt,
		Metadata:         map[string]any{"lane": "todo"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != TaskStatusReady || task.Priority != "high" {
		t.Fatalf("unexpected task defaults: %+v", task)
	}

	comment, err := service.AddComment(context.Background(), AddCommentInput{
		ProfileID: "default",
		BoardID:   board.ID,
		TaskID:    task.ID,
		Author:    "operator@glaucus.local",
		Kind:      CommentKindNote,
		Body:      "Remember to attach the parent run before queueing.",
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if comment.Kind != CommentKindNote {
		t.Fatalf("expected note comment, got %+v", comment)
	}

	boards, err := service.ListBoards(context.Background(), "default", 10)
	if err != nil {
		t.Fatalf("list boards: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != board.ID {
		t.Fatalf("unexpected boards: %+v", boards)
	}

	tasks, err := service.ListTasksByBoard(context.Background(), board.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}

	comments, err := service.ListCommentsByTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("unexpected comments: %+v", comments)
	}
}

func TestServiceUpdateTaskPersistsRunLinkageAndQueueState(t *testing.T) {
	app := newKanbanTestApp(t)
	service := NewService(app)

	board, err := service.CreateBoard(context.Background(), CreateBoardInput{
		ProfileID: "default",
		Name:      "Queue Board",
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}

	task, err := service.CreateTask(context.Background(), CreateTaskInput{
		ProfileID: "default",
		BoardID:   board.ID,
		Title:     "Process queued work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	position := 5
	retryCount := 2
	startedAt := time.Now().UTC().Round(time.Second)
	updated, err := service.UpdateTask(context.Background(), task.ID, UpdateTaskInput{
		Status:           TaskStatusInProgress,
		QueueState:       QueueStateRunning,
		Position:         &position,
		Assignee:         "worker-b",
		SessionID:        "session_kanban_1",
		ParentRunID:      "run_parent_1",
		LatestRunID:      "run_child_1",
		DelegationPrompt: "Work through the queue in deterministic order.",
		LastError:        "transient failure recovered",
		RetryCount:       &retryCount,
		StartedAt:        &startedAt,
	})
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	if updated.QueueState != QueueStateRunning || updated.LatestRunID != "run_child_1" {
		t.Fatalf("expected updated queue linkage, got %+v", updated)
	}
	if updated.RetryCount != 2 || updated.Position != 5 {
		t.Fatalf("expected updated counters and position, got %+v", updated)
	}

	active, err := service.ListActiveTasks(context.Background(), "default", 10)
	if err != nil {
		t.Fatalf("list active tasks: %v", err)
	}
	if len(active) != 1 || active[0].ID != task.ID {
		t.Fatalf("unexpected active tasks: %+v", active)
	}
}

func newKanbanTestApp(t *testing.T) core.App {
	t.Helper()

	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return app
}
