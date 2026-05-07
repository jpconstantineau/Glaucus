package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestTerminalToolExecutesCommand(t *testing.T) {
	result := TerminalTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      t.TempDir(),
		WorkingDirectory: t.TempDir(),
		Arguments: map[string]any{
			"command":    "go env GOOS",
			"timeout_ms": 10000,
		},
	})

	if result.Status != StatusSuccess {
		t.Fatalf("expected success, got %s (%s)", result.Status, result.DisplayText)
	}
	if strings.TrimSpace(result.DisplayText) == "" {
		t.Fatal("expected command output")
	}
}

func TestProcessToolCreatesDurableHandle(t *testing.T) {
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

	service := NewBackgroundProcessService(app)
	root := t.TempDir()
	result := ProcessTool{service: service}.Execute(context.Background(), ToolRequest{
		ProfileID:        "profile_default",
		SessionID:        "session_1",
		RunID:            "run_1",
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"action":  "start",
			"command": "go env GOOS",
		},
	})
	if result.Status != StatusSuccess {
		t.Fatalf("expected process start success, got %s (%s)", result.Status, result.DisplayText)
	}

	payload := result.Payload.(map[string]any)
	process := payload["process"].(BackgroundProcess)
	if process.Handle == "" {
		t.Fatal("expected durable process handle")
	}

	time.Sleep(300 * time.Millisecond)
	inspect := ProcessTool{service: service}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"action": "inspect",
			"handle": process.Handle,
		},
	})
	if inspect.Status != StatusSuccess {
		t.Fatalf("expected inspect success, got %s (%s)", inspect.Status, inspect.DisplayText)
	}
}
