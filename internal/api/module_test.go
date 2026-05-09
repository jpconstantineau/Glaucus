package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	batchsvc "github.com/jpconstantineau/Glaucus/internal/batch"
	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/goals"
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
		GoalService:     goals.NewService(app),
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
	batchService := batchsvc.NewService(app, sessionService, eventService)
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
		BatchService:    batchService,
		GoalService:     goals.NewService(app),
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

	batchReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/batches", strings.NewReader(`{
		"name":"Trajectory sample",
		"items":[
			{"prompt":"first batch prompt"},
			{"prompt":"second batch prompt"}
		]
	}`))
	batchReq.Header.Set("Authorization", "Bearer test-token")
	batchReq.Header.Set("Content-Type", "application/json")
	batchRes := httptest.NewRecorder()
	mux.ServeHTTP(batchRes, batchReq)
	if batchRes.Code != http.StatusOK {
		t.Fatalf("expected batch create 200, got %d: %s", batchRes.Code, batchRes.Body.String())
	}
	var batchPayload map[string]any
	if err := json.Unmarshal(batchRes.Body.Bytes(), &batchPayload); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	batchID, _ := batchPayload["id"].(string)
	if batchID == "" {
		t.Fatal("expected batch id")
	}

	runBatchReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/batches/"+batchID+"/run", nil)
	runBatchReq.SetPathValue("jobID", batchID)
	runBatchReq.Header.Set("Authorization", "Bearer test-token")
	runBatchRes := httptest.NewRecorder()
	mux.ServeHTTP(runBatchRes, runBatchReq)
	if runBatchRes.Code != http.StatusOK || !strings.Contains(runBatchRes.Body.String(), `"completed_count":2`) {
		t.Fatalf("expected batch run payload, got %d %s", runBatchRes.Code, runBatchRes.Body.String())
	}

	getBatchReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/batches/"+batchID, nil)
	getBatchReq.SetPathValue("jobID", batchID)
	getBatchReq.Header.Set("Authorization", "Bearer test-token")
	getBatchRes := httptest.NewRecorder()
	mux.ServeHTTP(getBatchRes, getBatchReq)
	if getBatchRes.Code != http.StatusOK || !strings.Contains(getBatchRes.Body.String(), `"attempts"`) {
		t.Fatalf("expected batch detail payload, got %d %s", getBatchRes.Code, getBatchRes.Body.String())
	}

	trajReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/batches/"+batchID+"/trajectory", nil)
	trajReq.SetPathValue("jobID", batchID)
	trajReq.Header.Set("Authorization", "Bearer test-token")
	trajRes := httptest.NewRecorder()
	mux.ServeHTTP(trajRes, trajReq)
	if trajRes.Code != http.StatusOK || !strings.Contains(trajRes.Body.String(), "batch.v1") {
		t.Fatalf("expected trajectory export payload, got %d %s", trajRes.Code, trajRes.Body.String())
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

	goalCreateReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/goals", strings.NewReader(`{
		"scope":"session",
		"session_id":"`+session.ID+`",
		"title":"Finish run API coverage",
		"statement":"Keep lifecycle APIs green.",
		"success_criteria":"List, update, clear, and evaluate all work."
	}`))
	goalCreateReq.Header.Set("Authorization", "Bearer test-token")
	goalCreateReq.Header.Set("Content-Type", "application/json")
	goalCreateRes := httptest.NewRecorder()
	mux.ServeHTTP(goalCreateRes, goalCreateReq)
	if goalCreateRes.Code != http.StatusOK {
		t.Fatalf("expected goal create 200, got %d: %s", goalCreateRes.Code, goalCreateRes.Body.String())
	}
	var createdGoal struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(goalCreateRes.Body.Bytes(), &createdGoal); err != nil {
		t.Fatalf("decode goal create response: %v", err)
	}
	goalID, _ := createdGoal.Data["ID"].(string)
	if goalID == "" {
		t.Fatalf("expected goal ID in payload, got %s", goalCreateRes.Body.String())
	}

	goalPatchReq := httptest.NewRequest(http.MethodPatch, "http://example.com/v1/goals/session/"+goalID, strings.NewReader(`{
		"priority":"high",
		"status":"in_review"
	}`))
	goalPatchReq.SetPathValue("scope", "session")
	goalPatchReq.SetPathValue("goalID", goalID)
	goalPatchReq.Header.Set("Authorization", "Bearer test-token")
	goalPatchReq.Header.Set("Content-Type", "application/json")
	goalPatchRes := httptest.NewRecorder()
	mux.ServeHTTP(goalPatchRes, goalPatchReq)
	if goalPatchRes.Code != http.StatusOK || !strings.Contains(goalPatchRes.Body.String(), "in_review") {
		t.Fatalf("expected goal patch response, got %d %s", goalPatchRes.Code, goalPatchRes.Body.String())
	}

	goalEvalReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/goals/session/"+goalID+"/evaluate", strings.NewReader(`{
		"status":"satisfied",
		"evaluation":{"summary":"verification complete","outcome":"met"}
	}`))
	goalEvalReq.SetPathValue("scope", "session")
	goalEvalReq.SetPathValue("goalID", goalID)
	goalEvalReq.Header.Set("Authorization", "Bearer test-token")
	goalEvalReq.Header.Set("Content-Type", "application/json")
	goalEvalRes := httptest.NewRecorder()
	mux.ServeHTTP(goalEvalRes, goalEvalReq)
	if goalEvalRes.Code != http.StatusOK || !strings.Contains(goalEvalRes.Body.String(), "verification complete") {
		t.Fatalf("expected goal evaluation response, got %d %s", goalEvalRes.Code, goalEvalRes.Body.String())
	}

	goalListReq := httptest.NewRequest(http.MethodGet, "http://example.com/v1/goals?scope=session&session_id="+session.ID, nil)
	goalListReq.Header.Set("Authorization", "Bearer test-token")
	goalListRes := httptest.NewRecorder()
	mux.ServeHTTP(goalListRes, goalListReq)
	if goalListRes.Code != http.StatusOK || !strings.Contains(goalListRes.Body.String(), "Finish run API coverage") {
		t.Fatalf("expected goal list response, got %d %s", goalListRes.Code, goalListRes.Body.String())
	}

	goalClearReq := httptest.NewRequest(http.MethodPost, "http://example.com/v1/goals/session/"+goalID+"/clear", nil)
	goalClearReq.SetPathValue("scope", "session")
	goalClearReq.SetPathValue("goalID", goalID)
	goalClearReq.Header.Set("Authorization", "Bearer test-token")
	goalClearRes := httptest.NewRecorder()
	mux.ServeHTTP(goalClearRes, goalClearReq)
	if goalClearRes.Code != http.StatusOK || !strings.Contains(goalClearRes.Body.String(), goals.StatusCleared) {
		t.Fatalf("expected goal clear response, got %d %s", goalClearRes.Code, goalClearRes.Body.String())
	}
}
