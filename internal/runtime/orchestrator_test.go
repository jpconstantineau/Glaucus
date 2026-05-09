package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/hooks"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
)

func TestOrchestratorExecuteSuccess(t *testing.T) {
	router := newTestRouter(t, []providers.CatalogEntry{chatEntry("primary", "")}, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-primary",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "hello back"},
			}},
		})
	})
	service := sessions.NewService(newRuntimeTestApp(t))
	orch := NewOrchestrator(service, router, nil, nil, nil)
	session := createRuntimeTestSession(t, service)

	result, err := orch.Execute(context.Background(), ExecuteRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		UserMessageID: "msg_user_1",
		Prompt:        PromptDocument{Fragments: []PromptFragment{{Name: "identity", Priority: 10, Content: "You are helpful."}}},
		Request: providers.NormalizedRequest{
			Messages: []providers.RequestMessage{{Role: "user", Content: "hi"}},
		},
		Resolution: providers.ResolutionInput{ProviderID: "primary", ModelID: "chat-primary", RequiredCapabilities: []string{"chat"}},
	})
	if err != nil {
		t.Fatalf("execute run: %v", err)
	}
	if result.Run.Status != RunStatusCompleted || result.Response.OutputText != "hello back" {
		t.Fatalf("unexpected success result: %#v", result)
	}
	if len(result.Attempts) != 1 || !result.Attempts[0].Success {
		t.Fatalf("unexpected attempt records: %#v", result.Attempts)
	}
}

func TestOrchestratorExecuteFallbackAfterRecoverableFailure(t *testing.T) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary upstream failure", http.StatusBadGateway)
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-fallback",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "fallback reply"},
			}},
		})
	}))
	defer fallbackServer.Close()

	catalog := providers.Catalog{Entries: []providers.CatalogEntry{
		chatEntry("primary", primaryServer.URL),
		chatEntry("fallback", fallbackServer.URL),
	}}
	router := providers.NewRouter(catalog, config.Default())
	service := sessions.NewService(newRuntimeTestApp(t))
	orch := NewOrchestrator(service, router, nil, nil, nil)
	session := createRuntimeTestSession(t, service)

	result, err := orch.Execute(context.Background(), ExecuteRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		UserMessageID: "msg_user_2",
		Prompt:        PromptDocument{},
		Request: providers.NormalizedRequest{
			Messages: []providers.RequestMessage{{Role: "user", Content: "recover"}},
		},
		Resolution: providers.ResolutionInput{ProviderID: "primary", ModelID: "chat-primary", RequiredCapabilities: []string{"chat"}},
	})
	if err != nil {
		t.Fatalf("execute run with fallback: %v", err)
	}
	if result.Response.OutputText != "fallback reply" || len(result.Attempts) != 2 {
		t.Fatalf("unexpected fallback result: %#v", result)
	}
	if result.Attempts[0].Success || !result.Attempts[1].Success {
		t.Fatalf("expected first attempt failure and second success, got %#v", result.Attempts)
	}
}

func TestOrchestratorExecuteFailure(t *testing.T) {
	router := newTestRouter(t, []providers.CatalogEntry{chatEntry("primary", "")}, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "hard failure", http.StatusInternalServerError)
	})
	service := sessions.NewService(newRuntimeTestApp(t))
	orch := NewOrchestrator(service, router, nil, nil, nil)
	session := createRuntimeTestSession(t, service)

	result, err := orch.Execute(context.Background(), ExecuteRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		UserMessageID: "msg_user_3",
		Request: providers.NormalizedRequest{
			Messages: []providers.RequestMessage{{Role: "user", Content: "fail"}},
		},
		Resolution: providers.ResolutionInput{ProviderID: "primary", ModelID: "chat-primary", RequiredCapabilities: []string{"chat"}},
	})
	if err == nil {
		t.Fatal("expected execution failure")
	}
	if result.Run.Status != RunStatusFailed || len(result.Attempts) != 1 {
		t.Fatalf("unexpected failed result: %#v", result)
	}
}

func TestOrchestratorExecuteCancellation(t *testing.T) {
	router := providers.NewRouter(providers.Catalog{Entries: []providers.CatalogEntry{chatEntry("primary", "http://127.0.0.1:1")}}, config.Default())
	service := sessions.NewService(newRuntimeTestApp(t))
	orch := NewOrchestrator(service, router, nil, nil, nil)
	session := createRuntimeTestSession(t, service)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := orch.Execute(ctx, ExecuteRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		UserMessageID: "msg_user_4",
		Request: providers.NormalizedRequest{
			Messages: []providers.RequestMessage{{Role: "user", Content: "cancel"}},
		},
		Resolution: providers.ResolutionInput{ProviderID: "primary", ModelID: "chat-primary", RequiredCapabilities: []string{"chat"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if result.Run.Status != RunStatusCancelled {
		t.Fatalf("expected cancelled status, got %#v", result.Run)
	}
}

func TestOrchestratorBlocksRunWhenHookDeniesRequest(t *testing.T) {
	router := newTestRouter(t, []providers.CatalogEntry{chatEntry("primary", "")}, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not execute", http.StatusInternalServerError)
	})
	service := sessions.NewService(newRuntimeTestApp(t))
	orch := NewOrchestrator(service, router, nil, nil, nil)
	bus := hooks.NewBus()
	bus.AddRunBlock("policy.block", 10, func(_ context.Context, input hooks.RunContext) hooks.BlockDecision {
		return hooks.BlockDecision{Blocked: true, Reason: "blocked by policy", Audit: map[string]any{"slot": "pre_provider"}}
	})
	orch.SetHooks(bus)
	session := createRuntimeTestSession(t, service)

	result, err := orch.Execute(context.Background(), ExecuteRunInput{
		ProfileID:     session.ProfileID,
		SessionID:     session.ID,
		TriggerSource: "web_chat",
		UserMessageID: "msg_user_5",
		Request: providers.NormalizedRequest{
			Messages: []providers.RequestMessage{{Role: "user", Content: "blocked"}},
		},
		Resolution: providers.ResolutionInput{ProviderID: "primary", ModelID: "chat-primary", RequiredCapabilities: []string{"chat"}},
	})
	if err == nil || result.Run.Status != RunStatusFailed || result.Run.ErrorCode != "hook_blocked" {
		t.Fatalf("expected hook-blocked run failure, got result=%#v err=%v", result, err)
	}
}

func newRuntimeTestApp(t *testing.T) core.App {
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

func createRuntimeTestSession(t *testing.T, service *sessions.Service) sessions.Session {
	t.Helper()

	session, err := service.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "profile_default",
		Source:    "web",
		Title:     "Runtime test session",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create runtime test session: %v", err)
	}
	return session
}

func newTestRouter(t *testing.T, entries []providers.CatalogEntry, handler http.HandlerFunc) *providers.Router {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	for i := range entries {
		if entries[i].BaseURL == "" {
			entries[i].BaseURL = server.URL
		}
	}

	return providers.NewRouter(providers.Catalog{Entries: entries}, config.Default())
}

func chatEntry(providerID, baseURL string) providers.CatalogEntry {
	return providers.CatalogEntry{
		ProviderID:   providerID,
		ModelID:      "chat-primary",
		Dialect:      "openai-chat",
		BaseURL:      baseURL,
		Capabilities: []string{"chat"},
	}
}
