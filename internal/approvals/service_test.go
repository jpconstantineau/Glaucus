package approvals

import (
	"context"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/core"
)

func TestEvaluateCreatesPendingApprovalForSensitiveTool(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app, config.ApprovalsConfig{Mode: ModeManual})

	result, err := service.Evaluate(context.Background(), EvaluationInput{
		ProfileID: "profile_default",
		SessionID: "session_1",
		RunID:     "run_1",
		ToolName:  "write_file",
		ToolDefinition: tools.ToolDefinition{
			Name:  "write_file",
			Flags: tools.ToolFlags{ApprovalSensitive: true},
		},
		Arguments: map[string]any{"path": "x.txt"},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.RequiresApproval || result.Request == nil {
		t.Fatalf("expected pending approval request, got %+v", result)
	}
}

func TestEvaluateHonorsSessionApprovalDecision(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app, config.ApprovalsConfig{Mode: ModeManual})

	first, err := service.Evaluate(context.Background(), EvaluationInput{
		ProfileID: "profile_default",
		SessionID: "session_1",
		RunID:     "run_1",
		ToolName:  "write_file",
		ToolDefinition: tools.ToolDefinition{
			Name:  "write_file",
			Flags: tools.ToolFlags{ApprovalSensitive: true},
		},
		Arguments: map[string]any{"path": "x.txt"},
	})
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if _, err := service.Decide(context.Background(), first.Request.ID, "allow", "session", "operator@example.com"); err != nil {
		t.Fatalf("decide: %v", err)
	}

	second, err := service.Evaluate(context.Background(), EvaluationInput{
		ProfileID: "profile_default",
		SessionID: "session_1",
		RunID:     "run_2",
		ToolName:  "write_file",
		ToolDefinition: tools.ToolDefinition{
			Name:  "write_file",
			Flags: tools.ToolFlags{ApprovalSensitive: true},
		},
		Arguments: map[string]any{"path": "x.txt"},
	})
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if !second.Allowed {
		t.Fatalf("expected stored session approval to allow the request, got %+v", second)
	}
}

func TestEvaluateBlocksDangerousCommands(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app, config.ApprovalsConfig{Mode: ModeSmart})

	result, err := service.Evaluate(context.Background(), EvaluationInput{
		ProfileID: "profile_default",
		SessionID: "session_1",
		RunID:     "run_1",
		ToolName:  "terminal",
		ToolDefinition: tools.ToolDefinition{
			Name:  "terminal",
			Flags: tools.ToolFlags{ApprovalSensitive: true},
		},
		Arguments: map[string]any{"command": "rm -rf /"},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Denied {
		t.Fatalf("expected dangerous command to be denied, got %+v", result)
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
