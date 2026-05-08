package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
)

func TestResponsesLifecycleAndModels(t *testing.T) {
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

	catalog := providers.Catalog{Entries: []providers.CatalogEntry{{
		ProviderID:      "test-provider",
		ModelID:         "test-model",
		DisplayName:     "Test Model",
		Dialect:         "openai-chat",
		BaseURL:         "http://127.0.0.1:1",
		Capabilities:    []string{"chat"},
		LifecycleStatus: "ga",
	}}}
	cfg := config.Default()
	cfg.API.BearerTokens = []string{"test-token"}
	cfg.Model.DefaultProvider = "test-provider"
	cfg.Model.DefaultModel = "test-model"
	module := Register(app, Options{
		Profile:         profile.ActiveProfile{Slug: "default", Root: t.TempDir()},
		Config:          cfg,
		ProviderCatalog: catalog,
		Router:          providers.NewRouter(catalog, cfg),
		SessionService:  sessions.NewService(app),
		JobService:      nil,
		EventService:    runtime.NewEventService(app),
		PromptBuilder:   runtime.NewPromptBuilder(),
		ToolRegistry:    nil,
		Orchestrator: runtime.NewOrchestrator(
			sessions.NewService(app),
			providers.NewRouter(catalog, cfg),
			runtime.NewEventService(app),
			nil,
			nil,
		),
	})

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	module.BindRoutes(router)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "test-model",
				"choices": []map[string]any{{
					"finish_reason": "stop",
					"message": map[string]any{
						"content": "stored response text",
					},
				}},
				"usage": map[string]any{"total_tokens": 12},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	module.options.ProviderCatalog.Entries[0].BaseURL = providerServer.URL
	module.options.Router = providers.NewRouter(module.options.ProviderCatalog, cfg)

	createReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/responses", strings.NewReader(`{
		"model":"test-model",
		"instructions":"be concise",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello there"}]}]
	}`))
	createReq.Header.Set("Authorization", "Bearer test-token")
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	mux.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		body, _ := io.ReadAll(createRes.Result().Body)
		t.Fatalf("expected create response 200, got %d: %s", createRes.Code, body)
	}

	var created map[string]any
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	responseID, _ := created["id"].(string)
	if responseID == "" {
		t.Fatal("expected response id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/responses/"+responseID, nil)
	getReq.SetPathValue("responseID", responseID)
	getReq.Header.Set("Authorization", "Bearer test-token")
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get response 200, got %d", getRes.Code)
	}
	if !strings.Contains(getRes.Body.String(), "stored response text") {
		t.Fatalf("expected stored response payload, got %s", getRes.Body.String())
	}

	modelsReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/models", nil)
	modelsReq.Header.Set("Authorization", "Bearer test-token")
	modelsRes := httptest.NewRecorder()
	mux.ServeHTTP(modelsRes, modelsReq)
	if modelsRes.Code != http.StatusOK || !strings.Contains(modelsRes.Body.String(), "test-model") {
		t.Fatalf("expected models list, got %d %s", modelsRes.Code, modelsRes.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "http://example.com/v1/responses/"+responseID, nil)
	deleteReq.SetPathValue("responseID", responseID)
	deleteReq.Header.Set("Authorization", "Bearer test-token")
	deleteRes := httptest.NewRecorder()
	mux.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected delete response 200, got %d", deleteRes.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/responses/"+responseID, nil)
	missingReq.SetPathValue("responseID", responseID)
	missingReq.Header.Set("Authorization", "Bearer test-token")
	missingRes := httptest.NewRecorder()
	mux.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("expected deleted response to return 404, got %d", missingRes.Code)
	}
}

func TestRunsAndJobsAPIs(t *testing.T) {
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

	catalog := providers.Catalog{Entries: []providers.CatalogEntry{{
		ProviderID:      "test-provider",
		ModelID:         "test-model",
		DisplayName:     "Test Model",
		Dialect:         "openai-chat",
		BaseURL:         "http://127.0.0.1:1",
		Capabilities:    []string{"chat"},
		LifecycleStatus: "ga",
	}}}
	cfg := config.Default()
	cfg.API.BearerTokens = []string{"test-token"}
	cfg.Model.DefaultProvider = "test-provider"
	cfg.Model.DefaultModel = "test-model"
	eventService := runtime.NewEventService(app)
	sessionService := sessions.NewService(app)
	jobService := jobs.NewService(app)
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"content": "run output text",
				},
			}},
			"usage": map[string]any{"total_tokens": 7},
		})
	}))
	defer providerServer.Close()
	catalog.Entries[0].BaseURL = providerServer.URL
	routerSvc := providers.NewRouter(catalog, cfg)

	module := Register(app, Options{
		Profile:         profile.ActiveProfile{Slug: "default", Root: t.TempDir()},
		Config:          cfg,
		ProviderCatalog: catalog,
		Router:          routerSvc,
		SessionService:  sessionService,
		JobService:      jobService,
		EventService:    eventService,
		PromptBuilder:   runtime.NewPromptBuilder(),
		Orchestrator:    runtime.NewOrchestrator(sessionService, routerSvc, eventService, nil, nil),
	})

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	module.BindRoutes(router)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	runReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/runs", strings.NewReader(`{
		"model":"test-model",
		"instructions":"be concise",
		"messages":[{"role":"user","content":"hello run"}]
	}`))
	runReq.Header.Set("Authorization", "Bearer test-token")
	runReq.Header.Set("Content-Type", "application/json")
	runRes := httptest.NewRecorder()
	mux.ServeHTTP(runRes, runReq)
	if runRes.Code != http.StatusOK {
		t.Fatalf("expected run create 200, got %d: %s", runRes.Code, runRes.Body.String())
	}
	var runPayload map[string]any
	if err := json.Unmarshal(runRes.Body.Bytes(), &runPayload); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	runID, _ := runPayload["id"].(string)
	if runID == "" {
		t.Fatal("expected run id")
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/runs/"+runID+"/events?once=1", nil)
	eventsReq.Header.Set("Authorization", "Bearer test-token")
	eventsRes := httptest.NewRecorder()
	mux.ServeHTTP(eventsRes, eventsReq)
	if eventsRes.Code != http.StatusOK || !strings.Contains(eventsRes.Body.String(), "run.completed") {
		t.Fatalf("expected run events SSE payload, got %d %s", eventsRes.Code, eventsRes.Body.String())
	}

	session, err := sessionService.CreateSession(t.Context(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "api.runs",
		Title:     "queued run",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	queuedRun, err := sessionService.CreateRun(t.Context(), sessions.CreateRunInput{
		ProfileID:     "default",
		SessionID:     session.ID,
		TriggerSource: "api.runs",
		Status:        runtime.RunStatusQueued,
	})
	if err != nil {
		t.Fatalf("create queued run: %v", err)
	}
	stopReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/runs/"+queuedRun.ID+"/stop", nil)
	stopReq.Header.Set("Authorization", "Bearer test-token")
	stopRes := httptest.NewRecorder()
	mux.ServeHTTP(stopRes, stopReq)
	if stopRes.Code != http.StatusOK || !strings.Contains(stopRes.Body.String(), runtime.RunStatusCancelled) {
		t.Fatalf("expected cancelled run response, got %d %s", stopRes.Code, stopRes.Body.String())
	}

	jobReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/jobs", strings.NewReader(`{
		"name":"Nightly health",
		"prompt":"summarize the status",
		"schedule_kind":"interval",
		"schedule_value":"1h"
	}`))
	jobReq.Header.Set("Authorization", "Bearer test-token")
	jobReq.Header.Set("Content-Type", "application/json")
	jobRes := httptest.NewRecorder()
	mux.ServeHTTP(jobRes, jobReq)
	if jobRes.Code != http.StatusOK {
		t.Fatalf("expected job create 200, got %d: %s", jobRes.Code, jobRes.Body.String())
	}
	var jobPayload map[string]any
	if err := json.Unmarshal(jobRes.Body.Bytes(), &jobPayload); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	jobID, _ := jobPayload["id"].(string)
	if jobID == "" {
		t.Fatal("expected job id")
	}

	runJobReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/jobs/"+jobID+"/run", nil)
	runJobReq.Header.Set("Authorization", "Bearer test-token")
	runJobRes := httptest.NewRecorder()
	mux.ServeHTTP(runJobRes, runJobReq)
	if runJobRes.Code != http.StatusOK || !strings.Contains(runJobRes.Body.String(), "run output text") {
		t.Fatalf("expected successful run-now payload, got %d %s", runJobRes.Code, runJobRes.Body.String())
	}

	getJobReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/jobs/"+jobID, nil)
	getJobReq.Header.Set("Authorization", "Bearer test-token")
	getJobRes := httptest.NewRecorder()
	mux.ServeHTTP(getJobRes, getJobReq)
	if getJobRes.Code != http.StatusOK || !strings.Contains(getJobRes.Body.String(), "history") {
		t.Fatalf("expected job history payload, got %d %s", getJobRes.Code, getJobRes.Body.String())
	}
}
