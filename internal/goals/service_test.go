package goals

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateAndListGoalsByScope(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	ctx := context.Background()

	sessionGoal, err := service.CreateGoal(ctx, CreateGoalInput{
		Scope:           ScopeSession,
		ProfileID:       "default",
		SessionID:       "session_1",
		Title:           "Fix CI before merge",
		Statement:       "Get the slice branch green before opening the PR.",
		SuccessCriteria: "All required checks pass.",
		Priority:        "high",
		Tags:            []string{"release", "ci"},
		State:           map[string]any{"owner": "operator"},
		Metadata:        map[string]any{"source": "dashboard"},
		CreatedByRunID:  "run_1",
	})
	if err != nil {
		t.Fatalf("create session goal: %v", err)
	}
	if sessionGoal.ID == "" || sessionGoal.Scope != ScopeSession {
		t.Fatalf("expected persisted session goal, got %#v", sessionGoal)
	}
	if sessionGoal.Version != 1 {
		t.Fatalf("expected initial version 1, got %d", sessionGoal.Version)
	}

	profileGoal, err := service.CreateGoal(ctx, CreateGoalInput{
		Scope:           ScopeProfile,
		ProfileID:       "default",
		Title:           "Preserve review quality",
		Statement:       "Keep findings and verification easy to audit.",
		SuccessCriteria: "Each change has a matching test.",
	})
	if err != nil {
		t.Fatalf("create profile goal: %v", err)
	}
	if profileGoal.Scope != ScopeProfile {
		t.Fatalf("expected profile goal scope, got %#v", profileGoal)
	}

	loadedSessionGoal, err := service.GetGoal(ctx, ScopeSession, sessionGoal.ID)
	if err != nil {
		t.Fatalf("get session goal: %v", err)
	}
	if loadedSessionGoal.Statement != sessionGoal.Statement || loadedSessionGoal.CreatedByRunID != "run_1" {
		t.Fatalf("expected session goal fields to round-trip, got %#v", loadedSessionGoal)
	}

	sessionGoals, err := service.ListGoals(ctx, ListGoalsInput{
		Scope:     ScopeSession,
		ProfileID: "default",
		SessionID: "session_1",
	})
	if err != nil {
		t.Fatalf("list session goals: %v", err)
	}
	if len(sessionGoals) != 1 {
		t.Fatalf("expected 1 session goal, got %d", len(sessionGoals))
	}

	profileGoals, err := service.ListGoals(ctx, ListGoalsInput{
		Scope:     ScopeProfile,
		ProfileID: "default",
	})
	if err != nil {
		t.Fatalf("list profile goals: %v", err)
	}
	if len(profileGoals) != 1 {
		t.Fatalf("expected 1 profile goal, got %d", len(profileGoals))
	}
}

func TestUpdateClearAndEvaluateGoal(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	ctx := context.Background()

	goal, err := service.CreateGoal(ctx, CreateGoalInput{
		Scope:     ScopeSession,
		ProfileID: "default",
		SessionID: "session_2",
		Title:     "Close the loop",
		Statement: "Ship the follow-up changes with verification.",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	goal, err = service.UpdateGoal(ctx, ScopeSession, goal.ID, UpdateGoalInput{
		Statement:      "Ship the follow-up changes with full verification.",
		Priority:       "high",
		UpdatedByRunID: "run_update",
	})
	if err != nil {
		t.Fatalf("update goal: %v", err)
	}
	if goal.Priority != "high" || goal.Version != 2 || goal.UpdatedByRunID != "run_update" {
		t.Fatalf("expected updated goal fields, got %#v", goal)
	}

	goal, err = service.EvaluateGoal(ctx, ScopeSession, goal.ID, EvaluateGoalInput{
		Evaluation: map[string]any{
			"outcome": "partial",
			"summary": "Tests pass but dashboard review remains.",
		},
		Status:           "in_review",
		UpdatedByRunID:   "run_eval",
		EvaluatedByRunID: "run_eval",
	})
	if err != nil {
		t.Fatalf("evaluate goal: %v", err)
	}
	if goal.Status != "in_review" || goal.LastEvaluatedRunID != "run_eval" || len(goal.EvaluationHistory) != 1 {
		t.Fatalf("expected persisted evaluation, got %#v", goal)
	}

	goal, err = service.ClearGoal(ctx, ScopeSession, goal.ID, ClearGoalInput{ClearedByRunID: "run_clear"})
	if err != nil {
		t.Fatalf("clear goal: %v", err)
	}
	if goal.Status != StatusCleared || goal.ClearedByRunID != "run_clear" || goal.ClearedAt.IsZero() {
		t.Fatalf("expected cleared goal metadata, got %#v", goal)
	}
}

func newTestApp(t *testing.T) core.App {
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
