package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
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
	module := Register(app, Options{
		Profile:         profile.ActiveProfile{Slug: "default", Root: t.TempDir()},
		Config:          cfg,
		ProviderCatalog: catalog,
		Router:          providers.NewRouter(catalog, cfg),
		SessionService:  sessions.NewService(app),
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
