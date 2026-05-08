package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/approvals"
	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/observability"
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
		Profile:         profile.ActiveProfile{Slug: "default"},
		ProviderCatalog: providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1"}}},
		SessionService:  sessions.NewService(app),
		JobService:      jobs.NewService(app),
		SearchService:   search.NewService(app, sessions.NewService(app)),
		SkillsService:   skills.NewService(app),
		ExportService:   exports.NewService(app),
		ObservabilityService: observability.NewService(app, observability.BuildInfo{
			AppName: "Glaucus",
			Version: "dev",
			Commit:  "local",
			BuiltAt: "now",
		}),
		EventService:  runtime.NewEventService(app),
		PromptBuilder: runtime.NewPromptBuilder(),
		Orchestrator: runtime.NewOrchestrator(sessions.NewService(app), providers.NewRouter(providers.Catalog{Entries: []providers.CatalogEntry{{ProviderID: "one", ModelID: "m1", Dialect: "openai-chat", BaseURL: "http://127.0.0.1:1", DisplayName: "Model One", Capabilities: []string{"chat"}}}}, config.Default()), runtime.NewEventService(app), func() *tools.Registry {
			r := tools.NewRegistry()
			tools.RegisterCatalogDefaults(r)
			tools.RegisterFileTools(r)
			return r
		}(), nil),
		ApprovalService: approvalService,
		ToolRegistry: func() *tools.Registry {
			r := tools.NewRegistry()
			tools.RegisterCatalogDefaults(r)
			tools.RegisterFileTools(r)
			return r
		}(),
		LoadedConfig:            config.Default(),
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

	for _, path := range []string{
		"/dashboard/sessions",
		"/dashboard/jobs",
		"/dashboard/skills",
		"/dashboard/logs",
		"/api/dashboard/status",
		"/api/dashboard/config",
		"/api/dashboard/providers",
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
