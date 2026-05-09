package batch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/core"
)

func TestRuntimeExecutorResumesOnlyIncompleteAttempts(t *testing.T) {
	app := newExecutorTestApp(t)
	sessionService := sessions.NewService(app)
	eventService := runtime.NewEventService(app)
	service := NewService(app, sessionService, eventService)
	service.now = func() time.Time {
		return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	}

	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	cfg := config.Default()
	cfg.Model.DefaultProvider = "test-provider"
	cfg.Model.DefaultModel = "test-model"
	var callCount atomic.Int32
	var failSecond atomic.Bool
	failSecond.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := callCount.Add(1)
		if current == 2 && failSecond.Load() {
			http.Error(w, "provider unavailable", http.StatusBadGateway)
			return
		}
		output := "attempt completed"
		if current == 1 {
			output = "first completed"
		}
		if current >= 3 {
			output = "second completed after resume"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"content": output,
				},
			}},
			"usage": map[string]any{"total_tokens": 9},
		})
	}))
	defer server.Close()

	catalog := providers.Catalog{Entries: []providers.CatalogEntry{{
		ProviderID:      "test-provider",
		ModelID:         "test-model",
		DisplayName:     "Test Model",
		Dialect:         "openai-chat",
		BaseURL:         server.URL,
		Capabilities:    []string{"chat"},
		LifecycleStatus: "ga",
	}}}
	registry := tools.NewRegistry()
	tools.RegisterCatalogDefaults(registry)
	tools.RegisterFileTools(registry)

	executor := RuntimeExecutor{
		Profile:       activeProfile,
		Config:        cfg,
		Sessions:      sessionService,
		PromptBuilder: runtime.NewPromptBuilder(),
		Orchestrator:  runtime.NewOrchestrator(sessionService, providers.NewRouter(catalog, cfg), eventService, registry, nil),
		ToolRegistry:  registry,
	}

	job, attempts, err := service.CreateJob(context.Background(), CreateJobInput{
		ProfileID:        "default",
		Name:             "Resumable batch",
		ProviderID:       "test-provider",
		ModelID:          "test-model",
		Toolset:          "safe",
		WorkingDirectory: activeProfile.Root,
		CreatedBy:        "test",
		Items: []Item{
			{Prompt: "first"},
			{Prompt: "second"},
		},
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}

	firstResult, err := executor.ExecuteJob(context.Background(), service, job)
	if err == nil {
		t.Fatal("expected first execution to report a failure for the second attempt")
	}
	if firstResult.CompletedCount != 1 || firstResult.FailedCount != 1 || firstResult.Status != JobStatusPartial {
		t.Fatalf("unexpected first execution result: %+v", firstResult)
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected two provider calls on first execution, got %d", callCount.Load())
	}

	reloadedJob, err := service.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	runnable, err := service.ListRunnableAttempts(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("list runnable attempts: %v", err)
	}
	if len(runnable) != 1 || runnable[0].ItemIndex != 2 {
		t.Fatalf("expected only second attempt to remain runnable, got %+v", runnable)
	}

	failSecond.Store(false)
	secondResult, err := executor.ExecuteJob(context.Background(), service, reloadedJob)
	if err != nil {
		t.Fatalf("resume batch job: %v", err)
	}
	if secondResult.CompletedCount != 2 || secondResult.FailedCount != 0 || secondResult.Status != JobStatusCompleted {
		t.Fatalf("unexpected resumed execution result: %+v", secondResult)
	}
	if callCount.Load() != 3 {
		t.Fatalf("expected resume to run only one more provider call, got %d", callCount.Load())
	}

	updatedAttempts, err := service.ListAttempts(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("list attempts after resume: %v", err)
	}
	if updatedAttempts[0].Status != AttemptStatusCompleted || updatedAttempts[1].Status != AttemptStatusCompleted {
		t.Fatalf("expected both attempts completed after resume, got %+v", updatedAttempts)
	}
	if updatedAttempts[0].OutputText != "first completed" {
		t.Fatalf("expected first attempt output to remain intact, got %+v", updatedAttempts[0])
	}
	if updatedAttempts[1].OutputText != "second completed after resume" {
		t.Fatalf("expected resumed output to be persisted, got %+v", updatedAttempts[1])
	}
	if secondResult.ExportPath == "" {
		t.Fatalf("expected trajectory export path after execution, got %+v", secondResult)
	}
}

func newExecutorTestApp(t *testing.T) core.App {
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
