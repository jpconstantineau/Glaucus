package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/approvals"
	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/goals"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	"github.com/jpconstantineau/Glaucus/internal/kanban"
	"github.com/jpconstantineau/Glaucus/internal/mcp"
	"github.com/jpconstantineau/Glaucus/internal/messaging"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/observability"
	"github.com/jpconstantineau/Glaucus/internal/plugins"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/search"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/skills"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
)

func TestHealthAndAuthenticatedDashboardFlow(t *testing.T) {
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
	if err := EnsureDefaultOperator(app, "admin@glaucus.local", "glaucus-admin"); err != nil {
		t.Fatalf("ensure operator: %v", err)
	}
	approvalService := approvals.NewService(app, config.Default().Approvals)
	kanbanService := kanban.NewService(app)
	profileRoot := t.TempDir()
	repoPluginsRoot := t.TempDir()
	cfg := config.Default()
	cfg.MCPServers["reference"] = config.MCPServerConfig{
		Command: "npx",
		Args:    []string{"@modelcontextprotocol/server-memory"},
		Tools: []config.MCPToolConfig{
			{Name: "mcp_lookup", Description: "Lookup memory", Toolsets: []string{"safe"}, AllowedSurfaces: []string{tools.SurfaceWebChat}, ReadOnly: true},
		},
	}
	registry := func() *tools.Registry {
		r := tools.NewRegistry()
		tools.RegisterCatalogDefaults(r)
		tools.RegisterFileTools(r)
		return r
	}()
	mcpService := mcp.NewService(app)
	if err := mcpService.Reconcile(t.Context(), cfg, registry); err != nil {
		t.Fatalf("reconcile mcp service: %v", err)
	}
	cfg.Plugins.RepoPaths = []string{filepath.Join(repoPluginsRoot, ".agents", "plugins")}
	cfg.Plugins.ProfilePaths = []string{"plugins"}
	pluginManifestPath := filepath.Join(repoPluginsRoot, ".agents", "plugins", "dashboard-kit", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(pluginManifestPath), 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(pluginManifestPath, []byte(`{"id":"dashboard-kit","name":"Dashboard Kit","category":"dashboard_extension","description":"Dashboard widgets","entryPoint":"index.js","configSchema":{"type":"object"}}`), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	pluginService := plugins.NewService(app)
	if err := pluginService.Reconcile(t.Context(), profileRoot, cfg.Plugins); err != nil {
		t.Fatalf("reconcile plugin service: %v", err)
	}

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	module := &Module{options: Options{
		AppName:         "Glaucus",
		Version:         "dev",
		Commit:          "local",
		BuiltAt:         "now",
		BindAddress:     "127.0.0.1:8090",
		SessionTTL:      24 * time.Hour,
		Profile:         profile.ActiveProfile{Slug: "default", Root: profileRoot},
		ProviderCatalog: providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1"}}},
		SessionService:  sessions.NewService(app),
		GoalService:     goals.NewService(app),
		JobService:      jobs.NewService(app),
		KanbanService:   kanbanService,
		SearchService:   search.NewService(app, sessions.NewService(app)),
		MessagingGateway: func() *messaging.Gateway {
			gateway := messaging.NewGateway(app, sessions.NewService(app))
			gateway.Register(messaging.NewWebhookAdapter(messaging.WebhookConfig{
				ProfileID:    "default",
				AdapterID:    "adapter_webhook",
				HMACSecret:   "secret",
				AllowedCIDRs: []string{"127.0.0.0/8"},
			}, nil))
			return gateway
		}(),
		SkillsService: skills.NewService(app),
		ExportService: exports.NewService(app),
		ObservabilityService: observability.NewService(app, observability.BuildInfo{
			AppName: "Glaucus",
			Version: "dev",
			Commit:  "local",
			BuiltAt: "now",
		}),
		EventService:            runtime.NewEventService(app),
		PromptBuilder:           runtime.NewPromptBuilder(),
		Orchestrator:            runtime.NewOrchestrator(sessions.NewService(app), providers.NewRouter(providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1", Dialect: "openai-chat", BaseURL: "http://127.0.0.1:1", DisplayName: "Model One", Capabilities: []string{"chat"}}}}, cfg), runtime.NewEventService(app), registry, nil),
		QueueManager:            kanban.NewQueueManager(kanbanService, sessions.NewService(app), runtime.NewOrchestrator(sessions.NewService(app), providers.NewRouter(providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1", Dialect: "openai-chat", BaseURL: "http://127.0.0.1:1", DisplayName: "Model One", Capabilities: []string{"chat"}}}}, cfg), runtime.NewEventService(app), registry, nil)),
		ApprovalService:         approvalService,
		ToolRegistry:            registry,
		MCPService:              mcpService,
		PluginService:           pluginService,
		LoadedConfig:            cfg,
		DefaultOperatorEmail:    "admin@glaucus.local",
		DefaultOperatorPassword: "glaucus-admin",
	}}
	module.BindRoutes(app, router)

	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/health", nil)
	healthReq.Host = "127.0.0.1:8090"
	healthRes := httptest.NewRecorder()
	mux.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /health, got %d", healthRes.Code)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/login", nil)
	loginPageReq.Host = "127.0.0.1:8090"
	loginPageRes := httptest.NewRecorder()
	mux.ServeHTTP(loginPageRes, loginPageReq)
	if loginPageRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /login, got %d", loginPageRes.Code)
	}

	csrfCookie := loginPageRes.Result().Cookies()[0]
	csrfToken := extractValue(loginPageRes.Body.String(), `name="csrf" value="`, `"`)

	form := url.Values{
		"csrf":     {csrfToken},
		"email":    {"admin@glaucus.local"},
		"password": {"glaucus-admin"},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/login", strings.NewReader(form.Encode()))
	loginReq.Host = "127.0.0.1:8090"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.AddCookie(csrfCookie)
	loginRes := httptest.NewRecorder()
	mux.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusSeeOther {
		body, _ := io.ReadAll(loginRes.Result().Body)
		t.Fatalf("expected login redirect, got %d: %s", loginRes.Code, body)
	}

	var sessionCookie *http.Cookie
	for _, cookie := range loginRes.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/dashboard", nil)
	dashboardReq.Host = "127.0.0.1:8090"
	dashboardReq.AddCookie(sessionCookie)
	dashboardReq.AddCookie(csrfCookie)
	dashboardRes := httptest.NewRecorder()
	mux.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /dashboard, got %d", dashboardRes.Code)
	}
	if !strings.Contains(dashboardRes.Body.String(), "Providers") {
		t.Fatal("expected dashboard to render provider card")
	}
	if !strings.Contains(dashboardRes.Body.String(), "Pending Approvals") {
		t.Fatal("expected dashboard to render approvals card")
	}
	if !strings.Contains(dashboardRes.Body.String(), "/dashboard/sessions") || !strings.Contains(dashboardRes.Body.String(), "/dashboard/jobs") {
		t.Fatal("expected dashboard to link to operator console pages")
	}
	if !strings.Contains(dashboardRes.Body.String(), "/dashboard/adapters") {
		t.Fatal("expected dashboard to link to adapters page")
	}

	detailedReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/health/detailed", nil)
	detailedReq.Host = "127.0.0.1:8090"
	detailedReq.AddCookie(sessionCookie)
	detailedRes := httptest.NewRecorder()
	mux.ServeHTTP(detailedRes, detailedReq)
	if detailedRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /health/detailed, got %d", detailedRes.Code)
	}

	runEvent, err := module.options.EventService.Append(t.Context(), runtime.AppendEventInput{
		ProfileID:  "profile_default",
		RunID:      "run_1",
		SessionID:  "session_1",
		Type:       "run.completed",
		Payload:    map[string]any{"status": "completed"},
		IsTerminal: true,
	})
	if err != nil {
		t.Fatalf("append run event: %v", err)
	}

	streamReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/dashboard/runs/run_1/stream?once=1", nil)
	streamReq.Host = "127.0.0.1:8090"
	streamReq.AddCookie(sessionCookie)
	streamRes := httptest.NewRecorder()
	mux.ServeHTTP(streamRes, streamReq)
	if streamRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from run stream, got %d", streamRes.Code)
	}
	if !strings.Contains(streamRes.Body.String(), "event: run.completed") || !strings.Contains(streamRes.Body.String(), runEvent.ID) {
		t.Fatalf("expected run event in SSE response, got %s", streamRes.Body.String())
	}

	chatReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/chat", nil)
	chatReq.Host = "127.0.0.1:8090"
	chatReq.AddCookie(sessionCookie)
	chatReq.AddCookie(csrfCookie)
	chatRes := httptest.NewRecorder()
	mux.ServeHTTP(chatRes, chatReq)
	if chatRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /chat, got %d", chatRes.Code)
	}
	if !strings.Contains(chatRes.Body.String(), "Send Prompt") || !strings.Contains(chatRes.Body.String(), "Streaming Output") {
		t.Fatalf("expected chat page to render MVP controls, got %s", chatRes.Body.String())
	}
	if !strings.Contains(chatRes.Body.String(), "Tool Activity") {
		t.Fatalf("expected chat page to render tool activity rail, got %s", chatRes.Body.String())
	}
	if !strings.Contains(chatRes.Body.String(), "Goal Context") {
		t.Fatalf("expected chat page to render goal context, got %s", chatRes.Body.String())
	}

	for _, path := range []string{
		"/dashboard/sessions",
		"/dashboard/goals",
		"/dashboard/jobs",
		"/dashboard/adapters",
		"/dashboard/skills",
		"/dashboard/logs",
		"/dashboard/kanban",
		"/api/dashboard/status",
		"/api/dashboard/config",
		"/api/dashboard/providers",
		"/api/dashboard/goals?scope=profile",
		"/api/dashboard/adapters",
		"/api/dashboard/secrets",
		"/api/dashboard/analytics",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090"+path, nil)
		req.Host = "127.0.0.1:8090"
		req.AddCookie(sessionCookie)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 from %s, got %d", path, res.Code)
		}
	}

	configReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/dashboard/config", nil)
	configReq.Host = "127.0.0.1:8090"
	configReq.AddCookie(sessionCookie)
	configRes := httptest.NewRecorder()
	mux.ServeHTTP(configRes, configReq)
	if configRes.Code != http.StatusOK || !strings.Contains(configRes.Body.String(), `"mcp_servers"`) || !strings.Contains(configRes.Body.String(), `"mcp_lookup"`) || !strings.Contains(configRes.Body.String(), `"plugins"`) || !strings.Contains(configRes.Body.String(), `"dashboard-kit"`) || !strings.Contains(configRes.Body.String(), `"feature_contracts"`) {
		t.Fatalf("expected config api to expose mcp inspection data, got %d %s", configRes.Code, configRes.Body.String())
	}

	toolsetsReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/dashboard/toolsets", nil)
	toolsetsReq.Host = "127.0.0.1:8090"
	toolsetsReq.AddCookie(sessionCookie)
	toolsetsRes := httptest.NewRecorder()
	mux.ServeHTTP(toolsetsRes, toolsetsReq)
	if toolsetsRes.Code != http.StatusOK || !strings.Contains(toolsetsRes.Body.String(), "mcp_lookup") {
		t.Fatalf("expected toolsets api to expose dynamic mcp tools, got %d %s", toolsetsRes.Code, toolsetsRes.Body.String())
	}

	secretsReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/api/dashboard/secrets", nil)
	secretsReq.Host = "127.0.0.1:8090"
	secretsReq.AddCookie(sessionCookie)
	secretsRes := httptest.NewRecorder()
	mux.ServeHTTP(secretsRes, secretsReq)
	if !strings.Contains(secretsRes.Body.String(), `"data"`) || strings.Contains(secretsRes.Body.String(), `"secret"`) {
		t.Fatalf("expected secrets endpoint to expose metadata only, got %s", secretsRes.Body.String())
	}
	if secretsRes.Result().Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected local web safety headers on dashboard APIs, got %v", secretsRes.Result().Header)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/metrics", nil)
	metricsReq.Host = "127.0.0.1:8090"
	metricsRes := httptest.NewRecorder()
	mux.ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK || !strings.Contains(metricsRes.Body.String(), "glaucus_build_info") {
		t.Fatalf("expected prometheus metrics, got %d %s", metricsRes.Code, metricsRes.Body.String())
	}
}

func TestInvalidHostIsRejected(t *testing.T) {
	if isAllowedHost("127.0.0.1:8090", "evil.example.com") {
		t.Fatal("expected remote host to be rejected")
	}
}

func TestLoginPageReusesExistingCSRFCookie(t *testing.T) {
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

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	module := &Module{options: Options{
		AppName:         "Glaucus",
		BindAddress:     "127.0.0.1:8090",
		SessionTTL:      24 * time.Hour,
		Profile:         profile.ActiveProfile{Slug: "default"},
		ProviderCatalog: providers.Catalog{},
		GoalService:     goals.NewService(app),
		ToolRegistry: func() *tools.Registry {
			r := tools.NewRegistry()
			tools.RegisterCatalogDefaults(r)
			tools.RegisterFileTools(r)
			return r
		}(),
	}}
	module.BindRoutes(app, router)

	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	firstReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/login", nil)
	firstReq.Host = "127.0.0.1:8090"
	firstRes := httptest.NewRecorder()
	mux.ServeHTTP(firstRes, firstReq)
	if firstRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from first /login, got %d", firstRes.Code)
	}

	firstCookie := firstRes.Result().Cookies()[0]
	firstToken := extractValue(firstRes.Body.String(), `name="csrf" value="`, `"`)
	if firstToken == "" {
		t.Fatal("expected first login form csrf token")
	}

	secondReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/login", nil)
	secondReq.Host = "127.0.0.1:8090"
	secondReq.AddCookie(firstCookie)
	secondRes := httptest.NewRecorder()
	mux.ServeHTTP(secondRes, secondReq)
	if secondRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from second /login, got %d", secondRes.Code)
	}

	secondToken := extractValue(secondRes.Body.String(), `name="csrf" value="`, `"`)
	if secondToken != firstToken {
		t.Fatalf("expected csrf token reuse, got first=%q second=%q", firstToken, secondToken)
	}

	if len(secondRes.Result().Cookies()) != 0 {
		t.Fatalf("expected no replacement csrf cookie when one already exists, got %d cookies", len(secondRes.Result().Cookies()))
	}
}

func extractValue(body, prefix, suffix string) string {
	start := strings.Index(body, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(body[start:], suffix)
	if end == -1 {
		return ""
	}
	return body[start : start+end]
}

func TestParseToolPrompt(t *testing.T) {
	invocation, err := parseToolPrompt(`/tool read_file {"path":"SOUL.md","start_line":1}`)
	if err != nil {
		t.Fatalf("parse tool prompt: %v", err)
	}
	if invocation == nil || invocation.Name != "read_file" {
		t.Fatalf("expected read_file invocation, got %+v", invocation)
	}
	if invocation.Arguments["path"] != "SOUL.md" {
		t.Fatalf("expected path argument, got %+v", invocation.Arguments)
	}
}

func TestApprovalsPageRendersPendingRequest(t *testing.T) {
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
	if err := EnsureDefaultOperator(app, "admin@glaucus.local", "glaucus-admin"); err != nil {
		t.Fatalf("ensure operator: %v", err)
	}

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	approvalService := approvals.NewService(app, config.Default().Approvals)
	if _, err := approvalService.Evaluate(t.Context(), approvals.EvaluationInput{
		ProfileID: "default",
		SessionID: "session_1",
		RunID:     "run_1",
		ToolName:  "write_file",
		ToolDefinition: tools.ToolDefinition{
			Name:  "write_file",
			Flags: tools.ToolFlags{ApprovalSensitive: true},
		},
		Arguments: map[string]any{"path": "docs/test.txt"},
	}); err != nil {
		t.Fatalf("create pending approval: %v", err)
	}

	module := &Module{options: Options{
		AppName:         "Glaucus",
		BindAddress:     "127.0.0.1:8090",
		SessionTTL:      24 * time.Hour,
		Profile:         profile.ActiveProfile{Slug: "default"},
		ApprovalService: approvalService,
		GoalService:     goals.NewService(app),
		ToolRegistry: func() *tools.Registry {
			r := tools.NewRegistry()
			tools.RegisterCatalogDefaults(r)
			tools.RegisterFileTools(r)
			return r
		}(),
	}}
	module.BindRoutes(app, router)

	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/login", nil)
	loginPageReq.Host = "127.0.0.1:8090"
	loginPageRes := httptest.NewRecorder()
	mux.ServeHTTP(loginPageRes, loginPageReq)
	csrfCookie := loginPageRes.Result().Cookies()[0]
	csrfToken := extractValue(loginPageRes.Body.String(), `name="csrf" value="`, `"`)

	form := url.Values{
		"csrf":     {csrfToken},
		"email":    {"admin@glaucus.local"},
		"password": {"glaucus-admin"},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/login", strings.NewReader(form.Encode()))
	loginReq.Host = "127.0.0.1:8090"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.AddCookie(csrfCookie)
	loginRes := httptest.NewRecorder()
	mux.ServeHTTP(loginRes, loginReq)

	var sessionCookie *http.Cookie
	for _, cookie := range loginRes.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/dashboard/approvals", nil)
	req.Host = "127.0.0.1:8090"
	req.AddCookie(sessionCookie)
	req.AddCookie(csrfCookie)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 from approvals page, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Approvals Queue") || !strings.Contains(res.Body.String(), "write_file") {
		t.Fatalf("expected approvals page to show pending request, got %s", res.Body.String())
	}
}

func TestKanbanDashboardDispatchAndCommentFlow(t *testing.T) {
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
	if err := EnsureDefaultOperator(app, "admin@glaucus.local", "glaucus-admin"); err != nil {
		t.Fatalf("ensure operator: %v", err)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "m1",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "delegated queue run complete"},
			}},
		})
	}))
	defer providerServer.Close()

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	sessionService := sessions.NewService(app)
	eventService := runtime.NewEventService(app)
	orchestrator := runtime.NewOrchestrator(sessionService, providers.NewRouter(providers.Catalog{Entries: []providers.CatalogEntry{{
		ProviderID:   "one",
		ModelID:      "m1",
		Dialect:      "openai-chat",
		BaseURL:      providerServer.URL,
		DisplayName:  "Model One",
		Capabilities: []string{"chat"},
	}}}, config.Default()), eventService, func() *tools.Registry {
		r := tools.NewRegistry()
		tools.RegisterCatalogDefaults(r)
		tools.RegisterFileTools(r)
		return r
	}(), nil)
	kanbanService := kanban.NewService(app)

	module := &Module{options: Options{
		AppName:         "Glaucus",
		Version:         "dev",
		Commit:          "local",
		BuiltAt:         "now",
		BindAddress:     "127.0.0.1:8090",
		SessionTTL:      24 * time.Hour,
		Profile:         profile.ActiveProfile{Slug: "default", Root: t.TempDir()},
		ProviderCatalog: providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1", DisplayName: "Model One"}}},
		SessionService:  sessionService,
		GoalService:     goals.NewService(app),
		JobService:      jobs.NewService(app),
		KanbanService:   kanbanService,
		QueueManager:    kanban.NewQueueManager(kanbanService, sessionService, orchestrator),
		EventService:    eventService,
		Orchestrator:    orchestrator,
		ToolRegistry: func() *tools.Registry {
			r := tools.NewRegistry()
			tools.RegisterCatalogDefaults(r)
			tools.RegisterFileTools(r)
			return r
		}(),
		LoadedConfig: config.Config{
			Model:     config.ModelConfig{DefaultProvider: "one", DefaultModel: "m1"},
			Web:       config.Default().Web,
			Cron:      config.Default().Cron,
			Approvals: config.Default().Approvals,
		},
		DefaultOperatorEmail:    "admin@glaucus.local",
		DefaultOperatorPassword: "glaucus-admin",
	}}
	module.BindRoutes(app, router)

	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/login", nil)
	loginPageReq.Host = "127.0.0.1:8090"
	loginPageRes := httptest.NewRecorder()
	mux.ServeHTTP(loginPageRes, loginPageReq)
	csrfCookie := loginPageRes.Result().Cookies()[0]
	csrfToken := extractValue(loginPageRes.Body.String(), `name="csrf" value="`, `"`)

	loginForm := url.Values{
		"csrf":     {csrfToken},
		"email":    {"admin@glaucus.local"},
		"password": {"glaucus-admin"},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/login", strings.NewReader(loginForm.Encode()))
	loginReq.Host = "127.0.0.1:8090"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.AddCookie(csrfCookie)
	loginRes := httptest.NewRecorder()
	mux.ServeHTTP(loginRes, loginReq)

	var sessionCookie *http.Cookie
	for _, cookie := range loginRes.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	createBoardForm := url.Values{
		"csrf":        {csrfToken},
		"name":        {"Slice 8 Board"},
		"description": {"Dashboard board"},
		"wip_limit":   {"2"},
	}
	createBoardReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/dashboard/kanban/boards", strings.NewReader(createBoardForm.Encode()))
	createBoardReq.Host = "127.0.0.1:8090"
	createBoardReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createBoardReq.AddCookie(sessionCookie)
	createBoardReq.AddCookie(csrfCookie)
	createBoardRes := httptest.NewRecorder()
	mux.ServeHTTP(createBoardRes, createBoardReq)
	if createBoardRes.Code != http.StatusSeeOther {
		t.Fatalf("expected board redirect, got %d", createBoardRes.Code)
	}

	boards, err := kanbanService.ListBoards(t.Context(), "default", 10)
	if err != nil || len(boards) != 1 {
		t.Fatalf("expected created board, got %#v err=%v", boards, err)
	}

	createTaskForm := url.Values{
		"csrf":              {csrfToken},
		"board_id":          {boards[0].ID},
		"title":             {"Investigate worker backlog"},
		"description":       {"Open the queue and summarize run linkage."},
		"delegation_prompt": {"Inspect the task board and report current blockers."},
		"status":            {kanban.TaskStatusReady},
		"priority":          {"high"},
		"position":          {"10"},
	}
	createTaskReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/dashboard/kanban/tasks", strings.NewReader(createTaskForm.Encode()))
	createTaskReq.Host = "127.0.0.1:8090"
	createTaskReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createTaskReq.AddCookie(sessionCookie)
	createTaskReq.AddCookie(csrfCookie)
	createTaskRes := httptest.NewRecorder()
	mux.ServeHTTP(createTaskRes, createTaskReq)
	if createTaskRes.Code != http.StatusSeeOther {
		t.Fatalf("expected task redirect, got %d", createTaskRes.Code)
	}

	tasks, err := kanbanService.ListTasksByBoard(t.Context(), boards[0].ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("expected created task, got %#v err=%v", tasks, err)
	}

	dispatchForm := url.Values{
		"csrf":              {csrfToken},
		"model":             {"one/m1"},
		"toolset":           {tools.SurfaceWebChat},
		"delegation_prompt": {"Inspect the task board and report current blockers."},
	}
	dispatchReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/dashboard/kanban/tasks/"+tasks[0].ID+"/dispatch", strings.NewReader(dispatchForm.Encode()))
	dispatchReq.Host = "127.0.0.1:8090"
	dispatchReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	dispatchReq.AddCookie(sessionCookie)
	dispatchReq.AddCookie(csrfCookie)
	dispatchRes := httptest.NewRecorder()
	mux.ServeHTTP(dispatchRes, dispatchReq)
	if dispatchRes.Code != http.StatusSeeOther {
		t.Fatalf("expected dispatch redirect, got %d body=%s", dispatchRes.Code, dispatchRes.Body.String())
	}

	commentForm := url.Values{
		"csrf":    {csrfToken},
		"comment": {"Operator verified the linked run."},
	}
	commentReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/dashboard/kanban/tasks/"+tasks[0].ID+"/comments", strings.NewReader(commentForm.Encode()))
	commentReq.Host = "127.0.0.1:8090"
	commentReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	commentReq.AddCookie(sessionCookie)
	commentReq.AddCookie(csrfCookie)
	commentRes := httptest.NewRecorder()
	mux.ServeHTTP(commentRes, commentReq)
	if commentRes.Code != http.StatusSeeOther {
		t.Fatalf("expected comment redirect, got %d", commentRes.Code)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/dashboard/kanban", nil)
	pageReq.Host = "127.0.0.1:8090"
	pageReq.AddCookie(sessionCookie)
	pageReq.AddCookie(csrfCookie)
	pageRes := httptest.NewRecorder()
	mux.ServeHTTP(pageRes, pageReq)
	if pageRes.Code != http.StatusOK {
		t.Fatalf("expected kanban page, got %d", pageRes.Code)
	}
	body := pageRes.Body.String()
	if !strings.Contains(body, "Slice 8 Board") || !strings.Contains(body, "Investigate worker backlog") {
		t.Fatalf("expected kanban page to render board and task, got %s", body)
	}
	if !strings.Contains(body, "/dashboard/runs/") || !strings.Contains(body, "Operator verified the linked run.") {
		t.Fatalf("expected kanban page to render run linkage and comment, got %s", body)
	}
}
