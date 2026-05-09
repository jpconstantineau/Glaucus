package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/approvals"
	batchsvc "github.com/jpconstantineau/Glaucus/internal/batch"
	"github.com/jpconstantineau/Glaucus/internal/config"
	exportsvc "github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/features"
	"github.com/jpconstantineau/Glaucus/internal/goals"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	"github.com/jpconstantineau/Glaucus/internal/kanban"
	"github.com/jpconstantineau/Glaucus/internal/mcp"
	"github.com/jpconstantineau/Glaucus/internal/messaging"
	"github.com/jpconstantineau/Glaucus/internal/observability"
	"github.com/jpconstantineau/Glaucus/internal/plugins"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/search"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/skills"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

const (
	sessionCookieName = "glaucus_session"
	csrfCookieName    = "glaucus_csrf"
)

type Options struct {
	AppName                 string
	Version                 string
	Commit                  string
	BuiltAt                 string
	BindAddress             string
	SessionTTL              time.Duration
	Profile                 profile.ActiveProfile
	ProviderCatalog         providers.Catalog
	SessionService          *sessions.Service
	BatchService            *batchsvc.Service
	GoalService             *goals.Service
	JobService              *jobs.Service
	KanbanService           *kanban.Service
	QueueManager            *kanban.QueueManager
	SearchService           *search.Service
	MessagingGateway        *messaging.Gateway
	SkillsService           *skills.Service
	ExportService           *exportsvc.Service
	MCPService              *mcp.Service
	PluginService           *plugins.Service
	FeatureService          *features.Service
	ObservabilityService    *observability.Service
	Scheduler               *jobs.Scheduler
	EventService            *runtime.EventService
	PromptBuilder           *runtime.PromptBuilder
	Orchestrator            *runtime.Orchestrator
	ApprovalService         *approvals.Service
	ToolRegistry            *tools.Registry
	LoadedConfig            config.Config
	DefaultOperatorEmail    string
	DefaultOperatorPassword string
}

type Module struct {
	options Options
}

type loginPageData struct {
	AppName              string
	CSRF                 string
	DefaultOperatorEmail string
	ErrorMessage         string
}

func Register(app core.App, options Options) *Module {
	module := &Module{options: options}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		module.BindRoutes(app, se.Router)
		return se.Next()
	})

	return module
}

func (m *Module) BindRoutes(app core.App, rg *router.Router[*core.RequestEvent]) {
	rg.GET("/", func(e *core.RequestEvent) error {
		if err := m.requireLocalHost(e); err != nil {
			return err
		}
		return e.Redirect(http.StatusTemporaryRedirect, "/dashboard")
	})
	rg.GET("/health", m.publicHealth)
	rg.GET("/metrics", m.metrics)
	rg.GET("/login", m.loginPage)
	rg.POST("/login", m.loginSubmit)
	rg.POST("/logout", m.withOperatorAuth(m.logoutSubmit))
	rg.GET("/dashboard", m.withOperatorAuth(m.dashboardShell))
	rg.GET("/dashboard/status", m.withOperatorAuth(m.dashboardStatusPage))
	rg.GET("/dashboard/sessions", m.withOperatorAuth(m.sessionsPage))
	rg.GET("/dashboard/goals", m.withOperatorAuth(m.goalsPage))
	rg.GET("/dashboard/runs/{runID}", m.withOperatorAuth(m.runDetailPage))
	rg.GET("/dashboard/batches", m.withOperatorAuth(m.batchesPage))
	rg.POST("/dashboard/batches", m.withOperatorAuth(m.batchCreateSubmit))
	rg.POST("/dashboard/batches/{jobID}/{action}", m.withOperatorAuth(m.batchActionSubmit))
	rg.GET("/dashboard/jobs", m.withOperatorAuth(m.jobsPage))
	rg.POST("/dashboard/jobs/{jobID}/{action}", m.withOperatorAuth(m.jobActionSubmit))
	rg.GET("/dashboard/adapters", m.withOperatorAuth(m.adaptersPage))
	rg.POST("/dashboard/adapters/{adapterID}/save", m.withOperatorAuth(m.adapterSaveSubmit))
	rg.POST("/dashboard/adapters/{adapterID}/reconnect", m.withOperatorAuth(m.adapterReconnectSubmit))
	rg.GET("/dashboard/skills", m.withOperatorAuth(m.skillsPage))
	rg.POST("/dashboard/skills/{slug}/{action}", m.withOperatorAuth(m.skillActionSubmit))
	rg.GET("/dashboard/logs", m.withOperatorAuth(m.logsPage))
	rg.GET("/dashboard/approvals", m.withOperatorAuth(m.approvalsPage))
	rg.POST("/dashboard/approvals/{approvalID}/decision", m.withOperatorAuth(m.approvalDecisionSubmit))
	rg.GET("/dashboard/tools", m.withOperatorAuth(m.toolsPage))
	rg.GET("/dashboard/kanban", m.withOperatorAuth(m.kanbanPage))
	rg.POST("/dashboard/kanban/boards", m.withOperatorAuth(m.kanbanBoardSubmit))
	rg.POST("/dashboard/kanban/tasks", m.withOperatorAuth(m.kanbanTaskSubmit))
	rg.POST("/dashboard/kanban/tasks/{taskID}/save", m.withOperatorAuth(m.kanbanTaskSaveSubmit))
	rg.POST("/dashboard/kanban/tasks/{taskID}/dispatch", m.withOperatorAuth(m.kanbanTaskDispatchSubmit))
	rg.POST("/dashboard/kanban/tasks/{taskID}/retry", m.withOperatorAuth(m.kanbanTaskRetrySubmit))
	rg.POST("/dashboard/kanban/tasks/{taskID}/cancel", m.withOperatorAuth(m.kanbanTaskCancelSubmit))
	rg.POST("/dashboard/kanban/tasks/{taskID}/comments", m.withOperatorAuth(m.kanbanCommentSubmit))
	rg.POST("/dashboard/goals/{scope}", m.withOperatorAuth(m.goalCreateSubmit))
	rg.POST("/dashboard/goals/{scope}/{goalID}/save", m.withOperatorAuth(m.goalSaveSubmit))
	rg.POST("/dashboard/goals/{scope}/{goalID}/evaluate", m.withOperatorAuth(m.goalEvaluateSubmit))
	rg.POST("/dashboard/goals/{scope}/{goalID}/clear", m.withOperatorAuth(m.goalClearSubmit))
	rg.GET("/chat", m.withOperatorAuth(m.chatPage))
	rg.GET("/chat/transcript", m.withOperatorAuth(m.chatTranscript))
	rg.POST("/chat/send", m.withOperatorAuth(m.chatSend))
	rg.GET("/health/detailed", m.withOperatorAuth(m.detailedHealth))
	rg.GET("/api/version", m.withOperatorAuth(m.versionInfo))
	rg.GET("/api/dashboard/status", m.withOperatorAuth(m.dashboardStatusAPI))
	rg.GET("/api/dashboard/config", m.withOperatorAuth(m.dashboardConfigAPI))
	rg.GET("/api/dashboard/providers", m.withOperatorAuth(m.dashboardProvidersAPI))
	rg.GET("/api/dashboard/toolsets", m.withOperatorAuth(m.dashboardToolsetsAPI))
	rg.GET("/api/dashboard/approvals", m.withOperatorAuth(m.dashboardApprovalsAPI))
	rg.GET("/api/dashboard/sessions", m.withOperatorAuth(m.dashboardSessionsAPI))
	rg.GET("/api/dashboard/goals", m.withOperatorAuth(m.dashboardGoalsAPI))
	rg.GET("/api/dashboard/jobs", m.withOperatorAuth(m.dashboardJobsAPI))
	rg.GET("/api/dashboard/adapters", m.withOperatorAuth(m.dashboardAdaptersAPI))
	rg.GET("/api/dashboard/secrets", m.withOperatorAuth(m.dashboardSecretsAPI))
	rg.GET("/api/dashboard/analytics", m.withOperatorAuth(m.dashboardAnalyticsAPI))
	rg.GET("/api/dashboard/runs/{runID}/stream", m.withOperatorAuth(m.streamRunEvents))
	rg.GET("/api/dashboard/sessions/{sessionID}/stream", m.withOperatorAuth(m.streamSessionEvents))
	rg.GET("/api/dashboard/status/stream", m.withOperatorAuth(m.streamStatusEvents))
	rg.POST("/gateway/webhooks/{adapterID}", m.webhookIngress)

	_ = app
}

func EnsureDefaultOperator(app core.App, email, password string) error {
	const collectionName = "operators"

	existing, err := app.FindFirstRecordByFilter(collectionName, "id != ''")
	if err == nil && existing != nil {
		return nil
	}

	collection, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(password)
	record.SetVerified(true)
	record.Set("display_name", "Local Operator")
	record.Set("profile_slug", "default")

	return app.Save(record)
}

func (m *Module) publicHealth(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	return e.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"service": m.options.AppName,
	})
}

func (m *Module) metrics(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}
	if m.options.ObservabilityService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "metrics unavailable"})
	}
	snapshot, err := m.options.ObservabilityService.Snapshot(e.Request.Context(), m.options.Profile.Slug)
	if err != nil {
		return e.InternalServerError("failed to build metrics snapshot", err)
	}
	applyLocalWebSafetyHeaders(e.Response)
	e.Response.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, err = e.Response.Write([]byte(m.options.ObservabilityService.Prometheus(snapshot)))
	return err
}

func (m *Module) loginPage(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create login form", err)
	}

	data := loginPageData{
		AppName:              m.options.AppName,
		CSRF:                 csrfToken,
		DefaultOperatorEmail: m.options.DefaultOperatorEmail,
		ErrorMessage:         strings.TrimSpace(e.Request.URL.Query().Get("error")),
	}

	return e.HTML(http.StatusOK, loginTemplate(data))
}

func (m *Module) loginSubmit(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	email := strings.TrimSpace(e.Request.FormValue("email"))
	password := e.Request.FormValue("password")
	if email == "" || password == "" {
		return m.renderLoginError(e, http.StatusBadRequest, "Email and password are required.")
	}

	record, err := e.App.FindAuthRecordByEmail("operators", email)
	if err != nil || !record.ValidatePassword(password) {
		return m.renderLoginError(e, http.StatusUnauthorized, "Invalid credentials.")
	}

	token, err := record.NewStaticAuthToken(m.options.SessionTTL)
	if err != nil {
		return e.InternalServerError("failed to create session", err)
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.options.SessionTTL.Seconds()),
	})

	return e.Redirect(http.StatusSeeOther, "/dashboard")
}

func (m *Module) renderLoginError(e *core.RequestEvent, status int, message string) error {
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create login form", err)
	}

	return e.HTML(status, loginTemplate(loginPageData{
		AppName:              m.options.AppName,
		CSRF:                 csrfToken,
		DefaultOperatorEmail: m.options.DefaultOperatorEmail,
		ErrorMessage:         message,
	}))
}

func (m *Module) logoutSubmit(e *core.RequestEvent, _ *core.Record) error {
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	return e.Redirect(http.StatusSeeOther, "/login")
}

func (m *Module) dashboardShell(e *core.RequestEvent, operator *core.Record) error {
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create dashboard session", err)
	}

	data := struct {
		AppName          string
		OperatorEmail    string
		ProfileSlug      string
		ProviderCount    int
		PendingApprovals int
		KanbanBoards     int
		ActiveQueueTasks int
		CSRF             string
	}{
		AppName:          m.options.AppName,
		OperatorEmail:    operator.Email(),
		ProfileSlug:      m.options.Profile.Slug,
		ProviderCount:    len(m.options.ProviderCatalog.Entries),
		PendingApprovals: m.pendingApprovalCount(e.Request.Context()),
		KanbanBoards:     m.kanbanBoardCount(e.Request.Context()),
		ActiveQueueTasks: m.kanbanActiveTaskCount(e.Request.Context()),
		CSRF:             csrfToken,
	}

	return e.HTML(http.StatusOK, dashboardTemplate(data))
}

func (m *Module) chatPage(e *core.RequestEvent, operator *core.Record) error {
	if m.options.SessionService == nil || m.options.PromptBuilder == nil || m.options.Orchestrator == nil {
		return e.InternalServerError("chat runtime unavailable", nil)
	}

	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create chat session", err)
	}

	profileID := m.options.Profile.Slug
	sessionList, err := m.options.SessionService.ListSessions(e.Request.Context(), profileID, 50)
	if err != nil {
		return e.InternalServerError("failed to list sessions", err)
	}

	activeSessionID := strings.TrimSpace(e.Request.URL.Query().Get("session"))
	if activeSessionID == "" && len(sessionList) > 0 {
		activeSessionID = sessionList[0].ID
	}

	var activeSession *sessions.Session
	var transcript []sessions.Message
	var sessionGoals []goals.Goal
	var profileGoals []goals.Goal
	if activeSessionID != "" {
		session, err := m.options.SessionService.GetSession(e.Request.Context(), activeSessionID)
		if err != nil {
			return e.InternalServerError("failed to load active session", err)
		}
		activeSession = &session

		transcript, err = m.options.SessionService.ListMessages(e.Request.Context(), activeSessionID)
		if err != nil {
			return e.InternalServerError("failed to load transcript", err)
		}
		sessionGoals, profileGoals, err = m.promptGoals(e.Request.Context(), activeSessionID)
		if err != nil {
			return e.InternalServerError("failed to load goals", err)
		}
	} else if m.options.GoalService != nil {
		_, profileGoals, err = m.options.GoalService.ListActiveGoals(e.Request.Context(), profileID, "")
		if err != nil {
			return e.InternalServerError("failed to load profile goals", err)
		}
	}

	runID := strings.TrimSpace(e.Request.URL.Query().Get("run"))
	selectedToolset := fallbackString(e.Request.URL.Query().Get("toolset"), m.defaultToolset())
	data := chatPageData{
		AppName:         m.options.AppName,
		OperatorEmail:   operator.Email(),
		CSRF:            csrfToken,
		Sessions:        sessionList,
		ActiveSession:   activeSession,
		Transcript:      transcript,
		Models:          m.modelOptions(),
		SelectedModel:   defaultModelRef(m.options.LoadedConfig),
		Toolsets:        m.toolsetOptions(),
		SelectedToolset: selectedToolset,
		ActiveRunID:     runID,
		StreamURL:       "/api/dashboard/runs/" + url.PathEscape(runID) + "/stream",
		TranscriptURL:   "/chat/transcript?session=" + url.QueryEscape(activeSessionID),
		SessionGoals:    sessionGoals,
		ProfileGoals:    profileGoals,
	}

	return e.HTML(http.StatusOK, chatTemplate(data))
}

func (m *Module) chatTranscript(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SessionService == nil {
		return e.InternalServerError("chat runtime unavailable", nil)
	}

	sessionID := strings.TrimSpace(e.Request.URL.Query().Get("session"))
	if sessionID == "" {
		return e.HTML(http.StatusOK, transcriptTemplate(nil))
	}

	messages, err := m.options.SessionService.ListMessages(e.Request.Context(), sessionID)
	if err != nil {
		return e.InternalServerError("failed to load transcript", err)
	}

	return e.HTML(http.StatusOK, transcriptTemplate(messages))
}

func (m *Module) chatSend(e *core.RequestEvent, operator *core.Record) error {
	if m.options.SessionService == nil || m.options.PromptBuilder == nil || m.options.Orchestrator == nil {
		return e.InternalServerError("chat runtime unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	promptText := strings.TrimSpace(e.Request.FormValue("prompt"))
	modelRef := strings.TrimSpace(e.Request.FormValue("model"))
	toolsetRef := strings.TrimSpace(e.Request.FormValue("toolset"))
	sessionID := strings.TrimSpace(e.Request.FormValue("session_id"))
	if promptText == "" {
		return e.BadRequestError("prompt is required", nil)
	}
	invocation, parseErr := parseToolPrompt(promptText)
	if parseErr != nil {
		return e.BadRequestError(parseErr.Error(), nil)
	}

	profileID := m.options.Profile.Slug
	var (
		session sessions.Session
		err     error
	)
	providerID, modelID := splitModelRef(modelRef, m.options.LoadedConfig)
	toolResolution := m.resolveToolset(e.Request.Context(), fallbackString(toolsetRef, m.defaultToolset()))
	if sessionID == "" {
		session, err = m.options.SessionService.CreateSession(e.Request.Context(), sessions.CreateSessionInput{
			ProfileID: profileID,
			Source:    "web",
			Title:     deriveSessionTitle(promptText),
			Status:    "active",
			ModelSnapshot: map[string]any{
				"provider": providerID,
				"model":    modelID,
			},
			ToolsetSnapshot: map[string]any{
				"name":         fallbackString(toolsetRef, m.defaultToolset()),
				"surface":      tools.SurfaceWebChat,
				"tool_names":   toolResolution.ToolNames,
				"availability": toolResolution.Availability,
			},
		})
		if err != nil {
			return e.InternalServerError("failed to create session", err)
		}
	} else {
		session, err = m.options.SessionService.GetSession(e.Request.Context(), sessionID)
		if err != nil {
			return e.InternalServerError("failed to load session", err)
		}
	}

	userMessage, err := m.options.SessionService.CreateMessage(e.Request.Context(), sessions.CreateMessageInput{
		ProfileID:   profileID,
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: promptText}},
		VisibleText: promptText,
	})
	if err != nil {
		return e.InternalServerError("failed to persist user message", err)
	}

	promptDoc := runtime.PromptDocument{}
	if invocation == nil {
		sessionGoals, profileGoals, goalErr := m.promptGoals(e.Request.Context(), session.ID)
		if goalErr != nil {
			return e.InternalServerError("failed to load goals", goalErr)
		}
		promptDoc, err = m.options.PromptBuilder.Build(runtime.PromptBuildInput{
			Profile:         m.options.Profile,
			Session:         session,
			ToolBehavior:    "Use toolset " + fallbackString(toolsetRef, m.defaultToolset()) + " unless no tools are needed.",
			ProjectContext:  "Current profile root: " + m.options.Profile.Root,
			PlatformHint:    "This turn originated from the browser chat surface.",
			ProviderOverlay: "Prefer the selected provider/model unless a deterministic fallback is required.",
			SessionGoals:    sessionGoals,
			ProfileGoals:    profileGoals,
		})
		if err != nil {
			return e.InternalServerError("failed to build prompt", err)
		}
	}

	input := runtime.ExecuteRunInput{
		ProfileID:      profileID,
		SessionID:      session.ID,
		TriggerSource:  "web_chat",
		UserMessageID:  userMessage.ID,
		Surface:        tools.SurfaceWebChat,
		Actor:          operator.Email(),
		ApprovalMode:   m.options.LoadedConfig.Approvals.Mode,
		ToolResolution: toolResolution,
		ToolInvocation: invocation,
		Prompt:         promptDoc,
		Request: providers.NormalizedRequest{
			Messages:     []providers.RequestMessage{{Role: "user", Content: promptText}},
			RequiredCaps: []string{"chat"},
		},
		Resolution: providers.ResolutionInput{
			ProviderID:           providerID,
			ModelID:              modelID,
			RequiredCapabilities: []string{"chat"},
		},
		WorkingDirectory: m.options.Profile.Root,
	}
	run, err := m.options.Orchestrator.QueueRun(context.Background(), input)
	if err != nil {
		return e.InternalServerError("failed to queue run", err)
	}

	go m.processChatRun(run, input)

	return e.Redirect(http.StatusSeeOther, "/chat?session="+url.QueryEscape(session.ID)+"&run="+url.QueryEscape(run.ID))
}

func (m *Module) dashboardStatusPage(e *core.RequestEvent, operator *core.Record) error {
	data := struct {
		OperatorEmail string
		BindAddress   string
		ProviderCount int
		Scheduler     jobs.SchedulerStatus
	}{
		OperatorEmail: operator.Email(),
		BindAddress:   m.options.BindAddress,
		ProviderCount: len(m.options.ProviderCatalog.Entries),
		Scheduler:     m.schedulerStatus(),
	}

	return e.HTML(http.StatusOK, statusTemplate(data))
}

func (m *Module) detailedHealth(e *core.RequestEvent, operator *core.Record) error {
	return e.JSON(http.StatusOK, map[string]any{
		"status":         "ok",
		"service":        m.options.AppName,
		"profile":        m.options.Profile.Slug,
		"bind_address":   m.options.BindAddress,
		"providers":      len(m.options.ProviderCatalog.Entries),
		"operator_email": operator.Email(),
		"version":        m.options.Version,
		"scheduler":      m.schedulerStatus(),
	})
}

func (m *Module) sessionsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SessionService == nil {
		return e.InternalServerError("session service unavailable", nil)
	}
	query := strings.TrimSpace(e.Request.URL.Query().Get("q"))
	sessionList, err := m.options.SessionService.ListSessions(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		return e.InternalServerError("failed to list sessions", err)
	}
	searchResults := []search.Result{}
	if query != "" && m.options.SearchService != nil {
		searchResults, err = m.options.SearchService.SearchSessions(e.Request.Context(), m.options.Profile.Slug, query, 20)
		if err != nil {
			return e.InternalServerError("failed to search sessions", err)
		}
	}
	return e.HTML(http.StatusOK, sessionsTemplate(struct {
		AppName string
		Query   string
		List    []sessions.Session
		Search  []search.Result
	}{
		AppName: m.options.AppName,
		Query:   query,
		List:    sessionList,
		Search:  searchResults,
	}))
}

func (m *Module) goalsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SessionService == nil || m.options.GoalService == nil {
		return e.InternalServerError("goal dashboard unavailable", nil)
	}
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create goal form", err)
	}
	sessionList, err := m.options.SessionService.ListSessions(e.Request.Context(), m.options.Profile.Slug, 50)
	if err != nil {
		return e.InternalServerError("failed to list sessions", err)
	}
	selectedSessionID := strings.TrimSpace(e.Request.URL.Query().Get("session"))
	if selectedSessionID == "" && len(sessionList) > 0 {
		selectedSessionID = sessionList[0].ID
	}
	sessionGoals, profileGoals, err := m.promptGoals(e.Request.Context(), selectedSessionID)
	if err != nil {
		return e.InternalServerError("failed to load goals", err)
	}
	return e.HTML(http.StatusOK, goalsTemplate(goalsPageData{
		AppName:           m.options.AppName,
		CSRF:              csrfToken,
		Sessions:          sessionList,
		SelectedSessionID: selectedSessionID,
		SessionGoals:      sessionGoals,
		ProfileGoals:      profileGoals,
	}))
}

func (m *Module) runDetailPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SessionService == nil || m.options.EventService == nil {
		return e.InternalServerError("run detail unavailable", nil)
	}
	runID := e.Request.PathValue("runID")
	run, err := m.options.SessionService.GetRun(e.Request.Context(), runID)
	if err != nil {
		return e.InternalServerError("failed to load run", err)
	}
	events, err := m.options.EventService.ListRunEvents(e.Request.Context(), runID, 0)
	if err != nil {
		return e.InternalServerError("failed to load run events", err)
	}
	return e.HTML(http.StatusOK, runDetailTemplate(struct {
		AppName string
		Run     sessions.Run
		Events  []runtime.RunEvent
	}{
		AppName: m.options.AppName,
		Run:     run,
		Events:  events,
	}))
}

func (m *Module) batchesPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.BatchService == nil {
		return e.InternalServerError("batch service unavailable", nil)
	}
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create batch form", err)
	}
	jobList, err := m.options.BatchService.ListJobs(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		jobList = []batchsvc.Job{}
	}
	selectedJobID := strings.TrimSpace(e.Request.URL.Query().Get("job"))
	attempts := []batchsvc.Attempt{}
	if selectedJobID != "" {
		attempts, err = m.options.BatchService.ListAttempts(e.Request.Context(), selectedJobID)
		if err != nil {
			attempts = []batchsvc.Attempt{}
		}
	}
	return e.HTML(http.StatusOK, batchesTemplate(struct {
		AppName     string
		CSRF        string
		Jobs        []batchsvc.Job
		SelectedJob string
		Attempts    []batchsvc.Attempt
	}{
		AppName:     m.options.AppName,
		CSRF:        csrfToken,
		Jobs:        jobList,
		SelectedJob: selectedJobID,
		Attempts:    attempts,
	}))
}

func (m *Module) batchCreateSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.BatchService == nil {
		return e.InternalServerError("batch service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	items := parseBatchPrompts(e.Request.FormValue("prompts"))
	if len(items) == 0 {
		return e.BadRequestError("at least one prompt is required", nil)
	}
	job, _, err := m.options.BatchService.CreateJob(e.Request.Context(), batchsvc.CreateJobInput{
		ProfileID:        m.options.Profile.Slug,
		Name:             firstValue(e.Request.FormValue("name"), "Batch Job"),
		ProviderID:       m.options.LoadedConfig.Model.DefaultProvider,
		ModelID:          m.options.LoadedConfig.Model.DefaultModel,
		Toolset:          firstValue(e.Request.FormValue("toolset"), "safe"),
		WorkingDirectory: m.options.Profile.Root,
		CreatedBy:        "dashboard",
		Items:            items,
		Metadata:         map[string]any{"surface": "dashboard"},
	})
	if err != nil {
		return e.InternalServerError("failed to create batch job", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/batches?job="+url.QueryEscape(job.ID))
}

func (m *Module) batchActionSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.BatchService == nil {
		return e.InternalServerError("batch service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	jobID := e.Request.PathValue("jobID")
	switch e.Request.PathValue("action") {
	case "run":
		job, err := m.options.BatchService.GetJob(e.Request.Context(), jobID)
		if err != nil {
			return e.InternalServerError("failed to load batch job", err)
		}
		_, err = m.batchExecutor().ExecuteJob(e.Request.Context(), m.options.BatchService, job)
		if err != nil {
			return e.InternalServerError("failed to run batch job", err)
		}
	case "export":
		if _, err := m.options.BatchService.WriteTrajectoryExport(e.Request.Context(), jobID, m.options.Profile.Root); err != nil {
			return e.InternalServerError("failed to export batch trajectory", err)
		}
	default:
		return e.BadRequestError("unsupported batch action", nil)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/batches?job="+url.QueryEscape(jobID))
}

func (m *Module) jobsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.JobService == nil {
		return e.InternalServerError("job service unavailable", nil)
	}
	jobList, err := m.options.JobService.ListJobs(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		return e.InternalServerError("failed to list jobs", err)
	}
	selectedJobID := strings.TrimSpace(e.Request.URL.Query().Get("job"))
	history := []jobs.JobRun{}
	if selectedJobID != "" {
		history, err = m.options.JobService.ListRuns(e.Request.Context(), selectedJobID, 25)
		if err != nil {
			return e.InternalServerError("failed to load job history", err)
		}
	}
	return e.HTML(http.StatusOK, jobsTemplate(struct {
		AppName string
		Jobs    []jobs.Job
		History []jobs.JobRun
	}{
		AppName: m.options.AppName,
		Jobs:    jobList,
		History: history,
	}))
}

func (m *Module) jobActionSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.JobService == nil {
		return e.InternalServerError("job service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	jobID := e.Request.PathValue("jobID")
	action := e.Request.PathValue("action")
	switch action {
	case "pause":
		_, _ = m.options.JobService.PauseJob(e.Request.Context(), jobID)
	case "resume":
		_, _ = m.options.JobService.ResumeJob(e.Request.Context(), jobID)
	case "run":
		_, _ = m.options.JobService.RecordRun(e.Request.Context(), jobs.RecordRunInput{
			ProfileID:    m.options.Profile.Slug,
			JobID:        jobID,
			Status:       jobs.JobStatusQueued,
			ScheduledFor: time.Now().UTC(),
		})
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/jobs?job="+url.QueryEscape(jobID))
}

func (m *Module) adaptersPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.MessagingGateway == nil {
		return e.InternalServerError("messaging gateway unavailable", nil)
	}
	if err := m.options.MessagingGateway.EnsurePhaseOneAdapters(e.Request.Context(), m.options.Profile.Slug); err != nil {
		return e.InternalServerError("failed to initialize adapters", err)
	}
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create adapters form", err)
	}
	items, err := m.options.MessagingGateway.ListAdapters(e.Request.Context(), m.options.Profile.Slug)
	if err != nil {
		return e.InternalServerError("failed to list adapters", err)
	}
	logs, err := m.options.MessagingGateway.ListLogs(e.Request.Context(), m.options.Profile.Slug, "", 25)
	if err != nil {
		return e.InternalServerError("failed to list adapter logs", err)
	}
	return e.HTML(http.StatusOK, adaptersTemplate(adaptersPageData{
		AppName:         m.options.AppName,
		CSRF:            csrfToken,
		Items:           items,
		Logs:            logs,
		PlatformCatalog: m.options.MessagingGateway.PlatformCatalog(),
	}))
}

func (m *Module) adapterSaveSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.MessagingGateway == nil {
		return e.InternalServerError("messaging gateway unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	adapterID := e.Request.PathValue("adapterID")
	record, err := m.options.MessagingGateway.GetAdapter(e.Request.Context(), adapterID)
	if err != nil {
		return e.InternalServerError("failed to load adapter", err)
	}
	if _, err := m.options.MessagingGateway.UpsertAdapter(e.Request.Context(), messaging.UpsertAdapterInput{
		ProfileID:    record.ProfileID,
		Platform:     record.Platform,
		Enabled:      e.Request.FormValue("enabled") == "on",
		Status:       firstValue(e.Request.FormValue("status"), record.Status),
		AuthMode:     firstValue(e.Request.FormValue("auth_mode"), record.AuthMode),
		Config:       record.Config,
		Allowlist:    splitAllowlist(e.Request.FormValue("allowlist")),
		Capabilities: record.Capabilities,
		Metadata:     record.Metadata,
	}); err != nil {
		return e.InternalServerError("failed to save adapter", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/adapters")
}

func (m *Module) adapterReconnectSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.MessagingGateway == nil {
		return e.InternalServerError("messaging gateway unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	adapterID := e.Request.PathValue("adapterID")
	record, err := m.options.MessagingGateway.GetAdapter(e.Request.Context(), adapterID)
	if err != nil {
		return e.InternalServerError("failed to load adapter", err)
	}
	adapter, ok := m.options.MessagingGateway.Adapter(record.Platform)
	if !ok {
		return e.InternalServerError("adapter backend is not registered", nil)
	}
	health, err := adapter.Health(e.Request.Context())
	if err != nil {
		return e.InternalServerError("failed to probe adapter", err)
	}
	if _, err := m.options.MessagingGateway.UpdateHealth(e.Request.Context(), adapterID, health); err != nil {
		return e.InternalServerError("failed to update adapter health", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/adapters")
}

func (m *Module) skillsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SkillsService == nil {
		return e.InternalServerError("skills service unavailable", nil)
	}
	skillList, err := m.options.SkillsService.ListSkills(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		return e.InternalServerError("failed to list skills", err)
	}
	return e.HTML(http.StatusOK, skillsTemplate(struct {
		AppName string
		Skills  []skills.Skill
	}{
		AppName: m.options.AppName,
		Skills:  skillList,
	}))
}

func (m *Module) skillActionSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SkillsService == nil {
		return e.InternalServerError("skills service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	slug := e.Request.PathValue("slug")
	action := e.Request.PathValue("action")
	switch action {
	case "pin":
		_, _ = m.options.SkillsService.UpdateSkillState(e.Request.Context(), m.options.Profile.Slug, slug, skills.UpdateInput{State: "pinned"})
	case "archive":
		_, _ = m.options.SkillsService.UpdateSkillState(e.Request.Context(), m.options.Profile.Slug, slug, skills.UpdateInput{State: "archived"})
	case "activate":
		_, _ = m.options.SkillsService.UpdateSkillState(e.Request.Context(), m.options.Profile.Slug, slug, skills.UpdateInput{State: "active"})
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/skills")
}

func (m *Module) logsPage(e *core.RequestEvent, _ *core.Record) error {
	logDir := filepath.Join(m.options.Profile.Root, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil && !os.IsNotExist(err) {
		return e.InternalServerError("failed to read logs", err)
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	selected := strings.TrimSpace(e.Request.URL.Query().Get("file"))
	if selected == "" && len(files) > 0 {
		selected = files[0]
	}
	content := ""
	if selected != "" {
		path, err := profile.ResolveOwnedPath(m.options.Profile.Root, filepath.Join("logs", selected))
		if err != nil {
			return e.InternalServerError("failed to resolve log path", err)
		}
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return e.InternalServerError("failed to read log file", err)
		}
		content = string(data)
	}
	return e.HTML(http.StatusOK, logsTemplate(struct {
		AppName string
		Files   []string
		Current string
		Content string
	}{
		AppName: m.options.AppName,
		Files:   files,
		Current: selected,
		Content: content,
	}))
}

func (m *Module) kanbanPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.KanbanService == nil {
		return e.InternalServerError("kanban service unavailable", nil)
	}
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create kanban form", err)
	}
	boards, err := m.options.KanbanService.ListBoards(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		return e.InternalServerError("failed to list boards", err)
	}
	boardViews := make([]kanbanBoardView, 0, len(boards))
	for _, board := range boards {
		tasks, err := m.options.KanbanService.ListTasksByBoard(e.Request.Context(), board.ID)
		if err != nil {
			return e.InternalServerError("failed to list board tasks", err)
		}
		taskViews := make([]kanbanTaskView, 0, len(tasks))
		for _, task := range tasks {
			comments, err := m.options.KanbanService.ListCommentsByTask(e.Request.Context(), task.ID)
			if err != nil {
				return e.InternalServerError("failed to load task comments", err)
			}
			taskViews = append(taskViews, kanbanTaskView{Task: task, Comments: comments})
		}
		boardViews = append(boardViews, kanbanBoardView{Board: board, Tasks: taskViews})
	}
	return e.HTML(http.StatusOK, kanbanTemplate(kanbanPageData{
		AppName:         m.options.AppName,
		CSRF:            csrfToken,
		Boards:          boardViews,
		Models:          m.modelOptions(),
		SelectedModel:   defaultModelRef(m.options.LoadedConfig),
		Toolsets:        m.toolsetOptions(),
		SelectedToolset: m.defaultToolset(),
		StatusOptions:   []string{kanban.TaskStatusBacklog, kanban.TaskStatusReady, kanban.TaskStatusInProgress, kanban.TaskStatusReview, kanban.TaskStatusDone, kanban.TaskStatusCancelled},
		PriorityOptions: []string{"low", "normal", "high", "urgent"},
	}))
}

func (m *Module) kanbanBoardSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil {
		return e.InternalServerError("kanban service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	wipLimit, _ := strconv.Atoi(strings.TrimSpace(e.Request.FormValue("wip_limit")))
	board, err := m.options.KanbanService.CreateBoard(e.Request.Context(), kanban.CreateBoardInput{
		ProfileID:   m.options.Profile.Slug,
		Name:        e.Request.FormValue("name"),
		Description: e.Request.FormValue("description"),
		Status:      kanban.BoardStatusActive,
		Owner:       operator.Email(),
		WIPLimit:    wipLimit,
	})
	if err != nil {
		return e.InternalServerError("failed to create board", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(board.ID))
}

func (m *Module) kanbanTaskSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil {
		return e.InternalServerError("kanban service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	position, _ := strconv.Atoi(strings.TrimSpace(e.Request.FormValue("position")))
	boardID := strings.TrimSpace(e.Request.FormValue("board_id"))
	task, err := m.options.KanbanService.CreateTask(e.Request.Context(), kanban.CreateTaskInput{
		ProfileID:        m.options.Profile.Slug,
		BoardID:          boardID,
		Title:            e.Request.FormValue("title"),
		Description:      e.Request.FormValue("description"),
		Status:           fallbackString(e.Request.FormValue("status"), kanban.TaskStatusBacklog),
		QueueState:       kanban.QueueStateIdle,
		Priority:         fallbackString(e.Request.FormValue("priority"), "normal"),
		Position:         position,
		Owner:            operator.Email(),
		Assignee:         e.Request.FormValue("assignee"),
		ParentRunID:      e.Request.FormValue("parent_run_id"),
		DelegationPrompt: e.Request.FormValue("delegation_prompt"),
	})
	if err != nil {
		return e.InternalServerError("failed to create task", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(task.BoardID))
}

func (m *Module) kanbanTaskSaveSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.KanbanService == nil {
		return e.InternalServerError("kanban service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	taskID := e.Request.PathValue("taskID")
	position, _ := strconv.Atoi(strings.TrimSpace(e.Request.FormValue("position")))
	task, err := m.options.KanbanService.GetTask(e.Request.Context(), taskID)
	if err != nil {
		return e.InternalServerError("failed to load task", err)
	}
	updated, err := m.options.KanbanService.UpdateTask(e.Request.Context(), taskID, kanban.UpdateTaskInput{
		Description:      e.Request.FormValue("description"),
		Status:           fallbackString(e.Request.FormValue("status"), task.Status),
		Priority:         fallbackString(e.Request.FormValue("priority"), task.Priority),
		Position:         &position,
		Assignee:         e.Request.FormValue("assignee"),
		ParentRunID:      e.Request.FormValue("parent_run_id"),
		DelegationPrompt: e.Request.FormValue("delegation_prompt"),
	})
	if err != nil {
		return e.InternalServerError("failed to update task", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(updated.BoardID))
}

func (m *Module) kanbanTaskDispatchSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil || m.options.QueueManager == nil {
		return e.InternalServerError("kanban queue unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	taskID := e.Request.PathValue("taskID")
	task, err := m.options.KanbanService.GetTask(e.Request.Context(), taskID)
	if err != nil {
		return e.InternalServerError("failed to load task", err)
	}
	providerID, modelID := splitModelRef(e.Request.FormValue("model"), m.options.LoadedConfig)
	toolset := fallbackString(e.Request.FormValue("toolset"), m.defaultToolset())
	if _, err := m.options.QueueManager.DispatchTaskRun(e.Request.Context(), kanban.DispatchInput{
		TaskID:           taskID,
		Actor:            operator.Email(),
		ParentRunID:      e.Request.FormValue("parent_run_id"),
		ProviderID:       providerID,
		ModelID:          modelID,
		Prompt:           e.Request.FormValue("delegation_prompt"),
		ApprovalMode:     m.options.LoadedConfig.Approvals.Mode,
		ToolResolution:   m.resolveToolset(e.Request.Context(), toolset),
		WorkingDirectory: m.options.Profile.Root,
	}); err != nil {
		return e.InternalServerError("failed to dispatch task", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(task.BoardID))
}

func (m *Module) kanbanTaskRetrySubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil || m.options.QueueManager == nil {
		return e.InternalServerError("kanban queue unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	taskID := e.Request.PathValue("taskID")
	task, err := m.options.KanbanService.GetTask(e.Request.Context(), taskID)
	if err != nil {
		return e.InternalServerError("failed to load task", err)
	}
	providerID, modelID := splitModelRef(e.Request.FormValue("model"), m.options.LoadedConfig)
	toolset := fallbackString(e.Request.FormValue("toolset"), m.defaultToolset())
	if _, err := m.options.QueueManager.RetryTaskRun(e.Request.Context(), kanban.DispatchInput{
		TaskID:           taskID,
		Actor:            operator.Email(),
		ParentRunID:      e.Request.FormValue("parent_run_id"),
		ProviderID:       providerID,
		ModelID:          modelID,
		Prompt:           e.Request.FormValue("delegation_prompt"),
		ApprovalMode:     m.options.LoadedConfig.Approvals.Mode,
		ToolResolution:   m.resolveToolset(e.Request.Context(), toolset),
		WorkingDirectory: m.options.Profile.Root,
	}); err != nil {
		return e.InternalServerError("failed to retry task", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(task.BoardID))
}

func (m *Module) kanbanTaskCancelSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil || m.options.QueueManager == nil {
		return e.InternalServerError("kanban queue unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	taskID := e.Request.PathValue("taskID")
	task, err := m.options.KanbanService.GetTask(e.Request.Context(), taskID)
	if err != nil {
		return e.InternalServerError("failed to load task", err)
	}
	if _, _, err := m.options.QueueManager.CancelTaskRun(e.Request.Context(), taskID, operator.Email()); err != nil {
		return e.InternalServerError("failed to cancel task", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(task.BoardID))
}

func (m *Module) kanbanCommentSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.KanbanService == nil {
		return e.InternalServerError("kanban service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	taskID := e.Request.PathValue("taskID")
	task, err := m.options.KanbanService.GetTask(e.Request.Context(), taskID)
	if err != nil {
		return e.InternalServerError("failed to load task", err)
	}
	if _, err := m.options.KanbanService.AddComment(e.Request.Context(), kanban.AddCommentInput{
		ProfileID: m.options.Profile.Slug,
		BoardID:   task.BoardID,
		TaskID:    task.ID,
		Author:    operator.Email(),
		Kind:      kanban.CommentKindNote,
		Body:      e.Request.FormValue("comment"),
	}); err != nil {
		return e.InternalServerError("failed to add comment", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/kanban?board="+url.QueryEscape(task.BoardID))
}

func (m *Module) versionInfo(e *core.RequestEvent, _ *core.Record) error {
	return e.JSON(http.StatusOK, map[string]any{
		"name":     m.options.AppName,
		"version":  m.options.Version,
		"commit":   m.options.Commit,
		"built_at": m.options.BuiltAt,
	})
}

func (m *Module) dashboardStatusAPI(e *core.RequestEvent, _ *core.Record) error {
	activeRuns := 0
	if m.options.SessionService != nil {
		runs, err := m.options.SessionService.ListActiveRuns(e.Request.Context(), m.options.Profile.Slug, 100)
		if err == nil {
			activeRuns = len(runs)
		}
	}
	activeJobs := 0
	if m.options.JobService != nil {
		jobRuns, err := m.options.JobService.ListActiveJobRuns(e.Request.Context(), m.options.Profile.Slug, 100)
		if err == nil {
			activeJobs = len(jobRuns)
		}
	}
	return e.JSON(http.StatusOK, map[string]any{
		"app_name":          m.options.AppName,
		"profile_slug":      m.options.Profile.Slug,
		"provider_count":    len(m.options.ProviderCatalog.Entries),
		"active_runs":       activeRuns,
		"active_job_runs":   activeJobs,
		"pending_approvals": m.pendingApprovalCount(e.Request.Context()),
		"scheduler_enabled": m.options.LoadedConfig.Cron.Enabled,
		"local_web_only":    true,
	})
}

func (m *Module) dashboardConfigAPI(e *core.RequestEvent, _ *core.Record) error {
	mcpServers := []any{}
	if m.options.MCPService != nil {
		if items, err := m.options.MCPService.ListServers(e.Request.Context(), 50); err == nil {
			mcpServers = make([]any, 0, len(items))
			for _, item := range items {
				mcpServers = append(mcpServers, item)
			}
		}
	}
	pluginList := []any{}
	if m.options.PluginService != nil {
		if items, err := m.options.PluginService.ListPlugins(e.Request.Context(), 50); err == nil {
			pluginList = make([]any, 0, len(items))
			for _, item := range items {
				pluginList = append(pluginList, item)
			}
		}
	}
	featureContracts := []any{}
	if m.options.FeatureService != nil {
		if items, err := m.options.FeatureService.ListContracts(e.Request.Context(), 50); err == nil {
			featureContracts = make([]any, 0, len(items))
			for _, item := range items {
				featureContracts = append(featureContracts, item)
			}
		}
	}
	return e.JSON(http.StatusOK, map[string]any{
		"model": map[string]any{
			"default_provider": m.options.LoadedConfig.Model.DefaultProvider,
			"default_model":    m.options.LoadedConfig.Model.DefaultModel,
		},
		"web": map[string]any{
			"bind_address": m.options.LoadedConfig.Web.BindAddress,
			"session_ttl":  m.options.LoadedConfig.Web.SessionTTL,
		},
		"media_providers": m.options.LoadedConfig.MediaProviders,
		"browser_contract": map[string]any{
			"recording": false,
			"stealth":   false,
			"proxies":   false,
			"keepalive": false,
			"live_view": false,
			"reason":    "browser contracts are capability-gated until a backend advertises support",
		},
		"approvals": map[string]any{
			"mode": m.options.LoadedConfig.Approvals.Mode,
		},
		"credential_pools":  m.options.LoadedConfig.CredentialPools,
		"routing":           m.options.LoadedConfig.Routing,
		"auxiliary_routing": m.options.LoadedConfig.AuxiliaryRouting,
		"profile": map[string]any{
			"slug": m.options.Profile.Slug,
			"root": m.options.Profile.Root,
		},
		"mcp_servers":       mcpServers,
		"plugins":           pluginList,
		"feature_contracts": featureContracts,
	})
}

func (m *Module) dashboardProvidersAPI(e *core.RequestEvent, _ *core.Record) error {
	items := make([]map[string]any, 0, len(m.options.ProviderCatalog.Entries))
	for _, entry := range m.options.ProviderCatalog.Entries {
		credentialConfigured := false
		authEnv := ""
		if providerCfg, ok := m.options.LoadedConfig.Providers[entry.ProviderID]; ok {
			authEnv = providerCfg.Auth.Env
			credentialConfigured = strings.TrimSpace(providerCfg.Auth.Env) != ""
		}
		items = append(items, map[string]any{
			"provider_id":           entry.ProviderID,
			"model_id":              entry.ModelID,
			"display_name":          entry.DisplayName,
			"family":                entry.ProviderFamily,
			"category":              entry.ProviderCategory,
			"dialect":               entry.Dialect,
			"capabilities":          entry.Capabilities,
			"lifecycle_status":      entry.LifecycleStatus,
			"credential_configured": credentialConfigured,
			"credential_env":        authEnv,
			"credential_pool":       m.options.LoadedConfig.Providers[entry.ProviderID].CredentialPool,
			"routing_policy":        m.options.LoadedConfig.Routing,
			"availability_reason":   m.providerAvailabilityReason(entry),
			"required_headers":      entry.RequiredHeaders,
		})
	}
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) providerAvailabilityReason(entry providers.CatalogEntry) string {
	if entry.ProviderCategory == "text_generation" {
		return ""
	}
	if _, ok := m.options.LoadedConfig.MediaProviders[entry.ProviderID]; ok {
		return ""
	}
	return "media provider category is declared but no media-specific configuration is loaded"
}

func (m *Module) dashboardToolsetsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.ToolRegistry == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	surfaces := []string{tools.SurfaceWebChat, tools.SurfaceWebAdmin, tools.SurfaceAPIDefault, tools.SurfaceBackgroundJob}
	data := make([]map[string]any, 0, len(surfaces))
	for _, surface := range surfaces {
		resolution := m.options.ToolRegistry.Resolve(e.Request.Context(), tools.ResolveRequest{
			Surface:          surface,
			RequestedToolset: surface,
			ProfileRoot:      m.options.Profile.Root,
			WorkingDirectory: m.options.Profile.Root,
		})
		data = append(data, map[string]any{
			"surface":           surface,
			"requested_toolset": resolution.RequestedToolset,
			"toolset_names":     resolution.ToolsetNames,
			"enabled_tools":     resolution.EnabledTools,
			"unavailable_tools": resolution.UnavailableTools,
		})
	}
	return e.JSON(http.StatusOK, map[string]any{"data": data})
}

func (m *Module) dashboardApprovalsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.ApprovalService == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	items, err := m.options.ApprovalService.ListPending(e.Request.Context(), m.options.Profile.Slug)
	if err != nil {
		return e.InternalServerError("failed to list approvals", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) dashboardSessionsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.SessionService == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	items, err := m.options.SessionService.ListSessions(e.Request.Context(), m.options.Profile.Slug, 50)
	if err != nil {
		return e.InternalServerError("failed to list sessions", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) dashboardGoalsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.GoalService == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	items, err := m.options.GoalService.ListGoals(e.Request.Context(), goals.ListGoalsInput{
		Scope:     e.Request.URL.Query().Get("scope"),
		ProfileID: m.options.Profile.Slug,
		SessionID: e.Request.URL.Query().Get("session_id"),
		Status:    e.Request.URL.Query().Get("status"),
		Limit:     50,
	})
	if err != nil {
		return e.InternalServerError("failed to list goals", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) dashboardJobsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.JobService == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	items, err := m.options.JobService.ListJobs(e.Request.Context(), m.options.Profile.Slug, 50)
	if err != nil {
		return e.InternalServerError("failed to list jobs", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) dashboardAdaptersAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.MessagingGateway == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": []any{}})
	}
	if err := m.options.MessagingGateway.EnsurePhaseOneAdapters(e.Request.Context(), m.options.Profile.Slug); err != nil {
		return e.InternalServerError("failed to initialize adapters", err)
	}
	items, err := m.options.MessagingGateway.ListAdapters(e.Request.Context(), m.options.Profile.Slug)
	if err != nil {
		return e.InternalServerError("failed to list adapters", err)
	}
	logs, err := m.options.MessagingGateway.ListLogs(e.Request.Context(), m.options.Profile.Slug, "", 10)
	if err != nil {
		return e.InternalServerError("failed to list adapter logs", err)
	}
	return e.JSON(http.StatusOK, map[string]any{
		"data":            items,
		"recent_logs":     logs,
		"platforms":       m.options.MessagingGateway.PlatformCatalog(),
		"webhook_ingress": "/gateway/webhooks/{adapterID}",
	})
}

func (m *Module) dashboardSecretsAPI(e *core.RequestEvent, _ *core.Record) error {
	items := make([]map[string]any, 0, len(m.options.LoadedConfig.Providers))
	for providerID, providerCfg := range m.options.LoadedConfig.Providers {
		items = append(items, map[string]any{
			"provider_id":           providerID,
			"auth_env":              providerCfg.Auth.Env,
			"credential_configured": strings.TrimSpace(providerCfg.Auth.Env) != "",
			"revealed":              false,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["provider_id"].(string) < items[j]["provider_id"].(string)
	})
	return e.JSON(http.StatusOK, map[string]any{"data": items})
}

func (m *Module) dashboardAnalyticsAPI(e *core.RequestEvent, _ *core.Record) error {
	if m.options.ObservabilityService == nil {
		return e.JSON(http.StatusOK, map[string]any{"data": map[string]any{}})
	}
	snapshot, err := m.options.ObservabilityService.Snapshot(e.Request.Context(), m.options.Profile.Slug)
	if err != nil {
		return e.InternalServerError("failed to build analytics snapshot", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"data": snapshot})
}

func (m *Module) streamRunEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	runID := e.Request.PathValue("runID")
	after := parseAfterSequence(e.Request.URL.Query().Get("after"))
	events, err := m.options.EventService.ListRunEvents(e.Request.Context(), runID, after)
	if err != nil {
		return e.InternalServerError("failed to load run events", err)
	}

	return m.streamEvents(e, events, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeRun(runID)
	})
}

func (m *Module) streamSessionEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	sessionID := e.Request.PathValue("sessionID")
	after := parseAfterSequence(e.Request.URL.Query().Get("after"))
	events, err := m.options.EventService.ListSessionEvents(e.Request.Context(), sessionID, after)
	if err != nil {
		return e.InternalServerError("failed to load session events", err)
	}

	return m.streamEvents(e, events, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeSession(sessionID)
	})
}

func (m *Module) streamStatusEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	snapshot, err := m.options.EventService.StatusSnapshot(e.Request.Context())
	if err != nil {
		return e.InternalServerError("failed to load status snapshot", err)
	}

	return m.streamEvents(e, []runtime.RunEvent{snapshot}, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeStatus()
	})
}

func (m *Module) webhookIngress(e *core.RequestEvent) error {
	if m.options.MessagingGateway == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "messaging gateway unavailable"})
	}
	adapterID := e.Request.PathValue("adapterID")
	record, err := m.options.MessagingGateway.GetAdapter(e.Request.Context(), adapterID)
	if err != nil {
		return e.NotFoundError("adapter not found", err)
	}
	if record.Platform != messaging.PlatformWebhook {
		return e.BadRequestError("adapter is not a webhook adapter", nil)
	}
	adapter, ok := m.options.MessagingGateway.Adapter(messaging.PlatformWebhook)
	if !ok {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "webhook adapter unavailable"})
	}
	normalizer, ok := adapter.(interface {
		NormalizeRequest(*http.Request, []byte) (messaging.InboundEvent, error)
	})
	if !ok {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "webhook normalizer unavailable"})
	}
	body, err := io.ReadAll(io.LimitReader(e.Request.Body, 1<<20))
	if err != nil {
		return e.BadRequestError("failed to read webhook body", err)
	}
	event, err := normalizer.NormalizeRequest(e.Request, body)
	if err != nil {
		return e.BadRequestError("invalid webhook payload", err)
	}
	event.ProfileID = record.ProfileID
	event.AdapterID = record.ID
	session, err := m.options.MessagingGateway.Ingest(e.Request.Context(), event)
	if err != nil {
		return e.InternalServerError("failed to ingest webhook event", err)
	}
	return e.JSON(http.StatusAccepted, map[string]any{
		"status":      "accepted",
		"session_id":  session.ID,
		"session_key": session.SessionKey,
	})
}

func (m *Module) streamEvents(
	e *core.RequestEvent,
	backlog []runtime.RunEvent,
	subscribe func() (<-chan runtime.RunEvent, func()),
) error {
	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-cache")
	e.Response.Header().Set("Connection", "keep-alive")

	flusher, ok := e.Response.(http.Flusher)
	if !ok {
		return e.InternalServerError("streaming unsupported", nil)
	}

	for _, event := range backlog {
		if err := writeSSEEvent(e.Response, event); err != nil {
			return err
		}
	}
	flusher.Flush()

	if e.Request.URL.Query().Get("once") == "1" {
		return nil
	}

	ch, unsubscribe := subscribe()
	defer unsubscribe()

	for {
		select {
		case <-e.Request.Context().Done():
			return nil
		case event := <-ch:
			if err := writeSSEEvent(e.Response, event); err != nil {
				return err
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, event runtime.RunEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload); err != nil {
		return err
	}
	return nil
}

func parseAfterSequence(value string) int {
	var after int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &after)
	return after
}

func (m *Module) processChatRun(run sessions.Run, input runtime.ExecuteRunInput) {
	result, err := m.options.Orchestrator.ProcessRun(context.Background(), run, input)
	if err != nil {
		if m.options.EventService != nil {
			_, _ = m.options.EventService.Append(context.Background(), runtime.AppendEventInput{
				ProfileID:  input.ProfileID,
				RunID:      run.ID,
				SessionID:  input.SessionID,
				Type:       "assistant.error",
				Payload:    map[string]any{"message": err.Error()},
				IsTerminal: false,
			})
		}
		return
	}

	_, _ = m.options.SessionService.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   input.ProfileID,
		SessionID:   input.SessionID,
		RunID:       result.Run.ID,
		Role:        "assistant",
		Content:     sessions.MessageContent{{Type: "output_text", Text: result.Response.OutputText}},
		VisibleText: result.Response.OutputText,
		Usage:       result.Response.Usage,
	})
}

func (m *Module) goalCreateSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.GoalService == nil {
		return e.InternalServerError("goal service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	scope := e.Request.PathValue("scope")
	sessionID := strings.TrimSpace(e.Request.FormValue("session_id"))
	_, err := m.options.GoalService.CreateGoal(e.Request.Context(), goals.CreateGoalInput{
		Scope:           scope,
		ProfileID:       m.options.Profile.Slug,
		SessionID:       sessionID,
		Title:           strings.TrimSpace(e.Request.FormValue("title")),
		Statement:       strings.TrimSpace(e.Request.FormValue("statement")),
		SuccessCriteria: strings.TrimSpace(e.Request.FormValue("success_criteria")),
		Priority:        fallbackString(strings.TrimSpace(e.Request.FormValue("priority")), "medium"),
		Status:          fallbackString(strings.TrimSpace(e.Request.FormValue("status")), goals.StatusActive),
	})
	if err != nil {
		return e.BadRequestError(err.Error(), err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/goals?session="+url.QueryEscape(sessionID))
}

func (m *Module) goalSaveSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.GoalService == nil {
		return e.InternalServerError("goal service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	scope := e.Request.PathValue("scope")
	goalID := e.Request.PathValue("goalID")
	sessionID := strings.TrimSpace(e.Request.FormValue("session_id"))
	_, err := m.options.GoalService.UpdateGoal(e.Request.Context(), scope, goalID, goals.UpdateGoalInput{
		Title:           strings.TrimSpace(e.Request.FormValue("title")),
		Statement:       strings.TrimSpace(e.Request.FormValue("statement")),
		SuccessCriteria: strings.TrimSpace(e.Request.FormValue("success_criteria")),
		Status:          strings.TrimSpace(e.Request.FormValue("status")),
		Priority:        strings.TrimSpace(e.Request.FormValue("priority")),
	})
	if err != nil {
		return e.BadRequestError(err.Error(), err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/goals?session="+url.QueryEscape(sessionID))
}

func (m *Module) goalEvaluateSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.GoalService == nil {
		return e.InternalServerError("goal service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	scope := e.Request.PathValue("scope")
	goalID := e.Request.PathValue("goalID")
	sessionID := strings.TrimSpace(e.Request.FormValue("session_id"))
	_, err := m.options.GoalService.EvaluateGoal(e.Request.Context(), scope, goalID, goals.EvaluateGoalInput{
		Status: strings.TrimSpace(e.Request.FormValue("status")),
		Evaluation: map[string]any{
			"outcome": strings.TrimSpace(e.Request.FormValue("outcome")),
			"summary": strings.TrimSpace(e.Request.FormValue("summary")),
		},
	})
	if err != nil {
		return e.BadRequestError(err.Error(), err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/goals?session="+url.QueryEscape(sessionID))
}

func (m *Module) goalClearSubmit(e *core.RequestEvent, _ *core.Record) error {
	if m.options.GoalService == nil {
		return e.InternalServerError("goal service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}
	scope := e.Request.PathValue("scope")
	goalID := e.Request.PathValue("goalID")
	sessionID := strings.TrimSpace(e.Request.FormValue("session_id"))
	_, err := m.options.GoalService.ClearGoal(e.Request.Context(), scope, goalID, goals.ClearGoalInput{})
	if err != nil {
		return e.BadRequestError(err.Error(), err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/goals?session="+url.QueryEscape(sessionID))
}

func (m *Module) withOperatorAuth(next func(*core.RequestEvent, *core.Record) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := m.requireLocalHost(e); err != nil {
			return err
		}
		applyLocalWebSafetyHeaders(e.Response)

		cookie, err := e.Request.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			return e.Redirect(http.StatusSeeOther, "/login")
		}

		record, err := e.App.FindAuthRecordByToken(cookie.Value, core.TokenTypeAuth)
		if err != nil {
			return e.Redirect(http.StatusSeeOther, "/login")
		}

		e.Auth = record
		return next(e, record)
	}
}

func applyLocalWebSafetyHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

func (m *Module) requireLocalHost(e *core.RequestEvent) error {
	if isAllowedHost(m.options.BindAddress, e.Request.Host) {
		return nil
	}
	return e.ForbiddenError("invalid host", nil)
}

func isAllowedHost(bindAddress, requestHost string) bool {
	allowed := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
		"[::1]":     {},
	}

	bindHost, bindPort, err := net.SplitHostPort(bindAddress)
	if err == nil {
		allowed[bindAddress] = struct{}{}
		allowed[bindHost] = struct{}{}
		if bindHost == "" {
			allowed["127.0.0.1:"+bindPort] = struct{}{}
			allowed["localhost:"+bindPort] = struct{}{}
		}
	}

	requestHost = strings.TrimSpace(requestHost)
	if requestHost == "" {
		return false
	}
	if _, ok := allowed[requestHost]; ok {
		return true
	}

	host, _, err := net.SplitHostPort(requestHost)
	if err == nil {
		_, ok := allowed[host]
		return ok
	}

	_, ok := allowed[requestHost]
	return ok
}

func validateCSRFCookie(e *core.RequestEvent) error {
	formToken := e.Request.FormValue("csrf")
	cookie, err := e.Request.Cookie(csrfCookieName)
	if err != nil {
		return err
	}
	if formToken == "" || formToken != cookie.Value {
		return fmt.Errorf("csrf token mismatch")
	}
	return nil
}

func ensureCSRFCookie(e *core.RequestEvent) (string, error) {
	if cookie, err := e.Request.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return token, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type chatPageData struct {
	AppName         string
	OperatorEmail   string
	CSRF            string
	Sessions        []sessions.Session
	ActiveSession   *sessions.Session
	Transcript      []sessions.Message
	SessionGoals    []goals.Goal
	ProfileGoals    []goals.Goal
	Models          []modelOption
	Toolsets        []string
	SelectedModel   string
	SelectedToolset string
	ActiveRunID     string
	StreamURL       string
	TranscriptURL   string
}

type modelOption struct {
	Ref   string
	Label string
}

var loginPageTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Login</title>
  <style>
    body { font-family: Georgia, serif; background: linear-gradient(135deg, #f3efe4, #dbe7f0); color: #1f2933; margin: 0; }
    main { max-width: 28rem; margin: 4rem auto; background: rgba(255,255,255,.92); padding: 2rem; border-radius: 1rem; box-shadow: 0 20px 60px rgba(15, 23, 42, .15); }
    label, input { display: block; width: 100%; }
    input { margin: .4rem 0 1rem; padding: .8rem; border: 1px solid #c7d2da; border-radius: .6rem; box-sizing: border-box; }
    button { background: #1f5c4a; color: #fff; border: 0; border-radius: .6rem; padding: .8rem 1rem; width: 100%; }
    .hint { color: #52606d; font-size: .95rem; }
    .error { background: #fff1f2; border: 1px solid #fecdd3; color: #9f1239; border-radius: .6rem; padding: .75rem .9rem; }
  </style>
</head>
<body>
  <main>
    <h1>{{.AppName}}</h1>
    <p class="hint">Sign in with the local operator account. Default bootstrap email: <strong>{{.DefaultOperatorEmail}}</strong></p>
    {{if .ErrorMessage}}<p class="error" role="alert">{{.ErrorMessage}}</p>{{end}}
    <form method="post" action="/login">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <label for="email">Email</label>
      <input id="email" name="email" type="email" required>
      <label for="password">Password</label>
      <input id="password" name="password" type="password" required>
      <button type="submit">Open Dashboard</button>
    </form>
  </main>
</body>
</html>`))

var dashboardPageTmpl = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Dashboard</title>
  <style>
    :root { --ink: #102a43; --muted: #627d98; --card: rgba(255,255,255,.88); --accent: #0f766e; --bg-a: #f6efe6; --bg-b: #d9e7ef; }
    body { margin: 0; font-family: "Trebuchet MS", sans-serif; color: var(--ink); background: radial-gradient(circle at top left, var(--bg-a), var(--bg-b)); }
    header, main { max-width: 64rem; margin: 0 auto; padding: 1.5rem; }
    header { display: flex; justify-content: space-between; align-items: center; }
    .brand { font-size: 1.8rem; font-weight: 700; letter-spacing: .04em; }
    .card-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr)); gap: 1rem; }
    .card, .panel { background: var(--card); border-radius: 1rem; padding: 1.25rem; box-shadow: 0 18px 40px rgba(16, 42, 67, .12); }
    .label { color: var(--muted); text-transform: uppercase; font-size: .78rem; letter-spacing: .08em; }
    .value { font-size: 1.7rem; margin-top: .4rem; }
    .panel { margin-top: 1rem; }
    button { background: var(--accent); color: #fff; border: 0; border-radius: .6rem; padding: .7rem 1rem; }
    a { color: var(--accent); }
  </style>
</head>
<body>
  <header>
    <div>
      <div class="brand">{{.AppName}}</div>
      <div>Signed in as {{.OperatorEmail}}</div>
    </div>
    <form method="post" action="/logout">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <button type="submit">Log Out</button>
    </form>
  </header>
  <main>
    <section class="card-grid">
      <article class="card">
        <div class="label">Profile</div>
        <div class="value">{{.ProfileSlug}}</div>
      </article>
      <article class="card">
        <div class="label">Providers</div>
        <div class="value">{{.ProviderCount}}</div>
      </article>
      <article class="card">
        <div class="label">Health</div>
        <div class="value">OK</div>
      </article>
      <article class="card">
        <div class="label">Pending Approvals</div>
        <div class="value">{{.PendingApprovals}}</div>
      </article>
      <article class="card">
        <div class="label">Kanban Boards</div>
        <div class="value">{{.KanbanBoards}}</div>
      </article>
      <article class="card">
        <div class="label">Active Queue Tasks</div>
        <div class="value">{{.ActiveQueueTasks}}</div>
      </article>
    </section>
    <section class="panel">
      <h2>Operator Console</h2>
      <p>Open the operator status page at <a href="/dashboard/status">/dashboard/status</a>, the browser chat at <a href="/chat">/chat</a>, and the sessions explorer at <a href="/dashboard/sessions">/dashboard/sessions</a>.</p>
      <p>Inspect run detail from <a href="/dashboard/runs/">/dashboard/runs/&lt;runID&gt;</a>, batch jobs at <a href="/dashboard/batches">/dashboard/batches</a>, cron jobs at <a href="/dashboard/jobs">/dashboard/jobs</a>, messaging adapters at <a href="/dashboard/adapters">/dashboard/adapters</a>, skills at <a href="/dashboard/skills">/dashboard/skills</a>, and logs at <a href="/dashboard/logs">/dashboard/logs</a>.</p>
      <p>Review approval requests at <a href="/dashboard/approvals">/dashboard/approvals</a>, inspect effective tool availability at <a href="/dashboard/tools">/dashboard/tools</a>, manage delegated work from <a href="/dashboard/kanban">/dashboard/kanban</a>, and adjust persistent goals at <a href="/dashboard/goals">/dashboard/goals</a>.</p>
      <p>Machine-readable endpoints: <a href="/health">/health</a>, <a href="/health/detailed">/health/detailed</a>, <a href="/api/version">/api/version</a>.</p>
    </section>
  </main>
</body>
</html>`))

var statusPageTmpl = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Glaucus Status</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 42rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Status</h1>
    <p>Operator: {{.OperatorEmail}}</p>
    <p>Bind address: {{.BindAddress}}</p>
    <p>Loaded provider entries: {{.ProviderCount}}</p>
    <p>Scheduler enabled: {{.Scheduler.Enabled}}</p>
    <p>Scheduler poll interval: {{.Scheduler.PollInterval}}</p>
    <p>Scheduler dispatched jobs: {{.Scheduler.DispatchedJobs}}</p>
    <p>Scheduler last error: {{if .Scheduler.LastError}}{{.Scheduler.LastError}}{{else}}none{{end}}</p>
  </section>
</body>
</html>`))

var chatPageTmpl = template.Must(template.New("chat").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Chat</title>
  <style>
    :root { --ink: #1d2733; --muted: #61788a; --paper: rgba(255,255,255,.9); --line: #d6e0e8; --accent: #0d6b5f; --bg-a: #f8ead8; --bg-b: #dceaf0; }
    body { margin: 0; font-family: "Trebuchet MS", sans-serif; color: var(--ink); background: linear-gradient(145deg, var(--bg-a), var(--bg-b)); }
    header { display: flex; justify-content: space-between; align-items: center; max-width: 78rem; margin: 0 auto; padding: 1.25rem 1.5rem; }
    header a { color: var(--accent); text-decoration: none; }
    main { max-width: 78rem; margin: 0 auto; padding: 0 1.5rem 1.5rem; display: grid; grid-template-columns: 20rem 1fr; gap: 1rem; }
    .panel { background: var(--paper); border-radius: 1rem; box-shadow: 0 20px 45px rgba(29,39,51,.10); }
    .sessions { padding: 1rem; }
    .session-list { display: grid; gap: .6rem; margin-top: 1rem; }
    .session-item { display: block; padding: .8rem .9rem; border-radius: .8rem; text-decoration: none; color: inherit; background: rgba(13,107,95,.06); }
    .session-item.active { background: var(--accent); color: white; }
    .session-title { font-weight: 700; }
    .session-meta { font-size: .85rem; opacity: .8; margin-top: .25rem; }
    .chat { padding: 1rem; display: grid; gap: 1rem; }
    .toolbar { display: grid; grid-template-columns: repeat(2, minmax(0,1fr)); gap: .8rem; }
    .toolbar label { display: grid; gap: .35rem; font-size: .85rem; color: var(--muted); }
    select, textarea, button { font: inherit; }
    select, textarea { width: 100%; border: 1px solid var(--line); border-radius: .8rem; padding: .75rem; box-sizing: border-box; background: #fff; }
    textarea { min-height: 8rem; resize: vertical; }
    button { border: 0; border-radius: .8rem; background: var(--accent); color: white; padding: .85rem 1.1rem; cursor: pointer; }
    .transcript, .stream { border: 1px solid var(--line); border-radius: .9rem; background: #fff; padding: 1rem; }
    .transcript-list { display: grid; gap: .8rem; }
    .message { border-radius: .9rem; padding: .9rem 1rem; }
    .message.user { background: #eef6ff; }
    .message.assistant { background: #f3faf7; }
    .message .role { font-size: .78rem; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); margin-bottom: .35rem; }
    .stream pre { white-space: pre-wrap; margin: .75rem 0 0; font-family: Consolas, monospace; }
    .hint { color: var(--muted); font-size: .92rem; }
    .empty { color: var(--muted); font-style: italic; }
    @media (max-width: 860px) { main { grid-template-columns: 1fr; } .toolbar { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <header>
    <div>
      <div><a href="/dashboard">Dashboard</a></div>
      <h1>{{.AppName}} Chat</h1>
      <div class="hint">Signed in as {{.OperatorEmail}}</div>
    </div>
    <div class="hint">Run: {{if .ActiveRunID}}{{.ActiveRunID}}{{else}}idle{{end}}</div>
  </header>
  <main>
    <aside class="panel sessions">
      <strong>Sessions</strong>
      <div class="session-list">
        {{if .Sessions}}
          {{range .Sessions}}
            <a class="session-item{{if $.ActiveSession}}{{if eq $.ActiveSession.ID .ID}} active{{end}}{{end}}" href="/chat?session={{.ID}}">
              <div class="session-title">{{.Title}}</div>
              <div class="session-meta">{{.Status}} · {{.Source}}</div>
            </a>
          {{end}}
        {{else}}
          <div class="empty">Your first browser prompt will create the first session.</div>
        {{end}}
      </div>
      <div class="session-list">
        <strong>Goal Context</strong>
        {{range .SessionGoals}}
          <div class="session-item">
            <div class="session-title">{{.Title}}</div>
            <div class="session-meta">Session goal · {{.Status}} · {{.Priority}}</div>
          </div>
        {{end}}
        {{range .ProfileGoals}}
          <div class="session-item">
            <div class="session-title">{{.Title}}</div>
            <div class="session-meta">Profile goal · {{.Status}} · {{.Priority}}</div>
          </div>
        {{else}}
          {{if not .SessionGoals}}
            <div class="empty">No active goals yet.</div>
          {{end}}
        {{end}}
      </div>
    </aside>
    <section class="panel chat">
      <div>
        <h2>{{if .ActiveSession}}{{.ActiveSession.Title}}{{else}}Start a New Session{{end}}</h2>
        <div class="hint">{{if .ActiveSession}}Session {{.ActiveSession.ID}}{{else}}Choose a model, send a prompt, and watch the run stream update below.{{end}}</div>
      </div>
      <form method="post" action="/chat/send">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <input type="hidden" name="session_id" value="{{if .ActiveSession}}{{.ActiveSession.ID}}{{end}}">
        <div class="toolbar">
          <label>Model
            <select name="model">
              {{range .Models}}
                <option value="{{.Ref}}" {{if eq $.SelectedModel .Ref}}selected{{end}}>{{.Label}}</option>
              {{end}}
            </select>
          </label>
          <label>Toolset
            <select name="toolset">
              {{range .Toolsets}}
                <option value="{{.}}" {{if eq $.SelectedToolset .}}selected{{end}}>{{.}}</option>
              {{end}}
            </select>
          </label>
        </div>
        <label style="display:block; margin-top:1rem;">Prompt
          <textarea name="prompt" placeholder="Ask Glaucus to summarize a file, inspect the repo, or answer a question..." required></textarea>
        </label>
        <div style="margin-top:1rem;">
          <button type="submit">Send Prompt</button>
        </div>
      </form>
      <section class="stream">
        <strong>Tool Activity</strong>
        <div class="hint">Tool calls, approval requests, and results stream into this rail for the active run.</div>
        <pre id="tool-activity"></pre>
      </section>
      <section class="stream">
        <strong>Streaming Output</strong>
        <div class="hint">Run events feed the panel below while the transcript persists on the server.</div>
        <pre id="live-output"></pre>
      </section>
      <section class="transcript">
        <strong>Transcript</strong>
        <div id="transcript">{{template "transcript" .Transcript}}</div>
      </section>
    </section>
  </main>
  <script>
    (function () {
      const runId = {{printf "%q" .ActiveRunID}};
      if (!runId) return;

      const live = document.getElementById("live-output");
      const activity = document.getElementById("tool-activity");
      const transcript = document.getElementById("transcript");
      const source = new EventSource({{printf "%q" .StreamURL}});
      ["tool.started", "tool.completed", "tool.failed", "tool.approval_requested", "tool.approval_denied"].forEach(function (name) {
        source.addEventListener(name, function (event) {
          const payload = JSON.parse(event.data);
          const details = payload.payload || {};
          activity.textContent += "[" + name + "] " + JSON.stringify(details) + "\n";
        });
      });
      source.addEventListener("assistant.delta", function (event) {
        const payload = JSON.parse(event.data);
        live.textContent += (payload.payload && payload.payload.text) ? payload.payload.text : "";
      });
      source.addEventListener("assistant.completed", function (event) {
        const payload = JSON.parse(event.data);
        if (payload.payload && payload.payload.text && !live.textContent) {
          live.textContent = payload.payload.text;
        }
      });
      ["run.completed", "run.failed", "run.cancelled"].forEach(function (name) {
        source.addEventListener(name, function () {
          fetch({{printf "%q" .TranscriptURL}}, { credentials: "same-origin" })
            .then(function (response) { return response.text(); })
            .then(function (html) { transcript.innerHTML = html; });
          source.close();
        });
      });
    }());
  </script>
</body>
</html>
{{define "transcript"}}
  {{if .}}
    <div class="transcript-list">
      {{range .}}
        <article class="message {{.Role}}">
          <div class="role">{{.Role}}</div>
          <div>{{.VisibleText}}</div>
        </article>
      {{end}}
    </div>
  {{else}}
    <div class="empty">No messages yet. Send the first prompt to start the session.</div>
  {{end}}
{{end}}`))

func loginTemplate(data any) string {
	var sb strings.Builder
	_ = loginPageTmpl.Execute(&sb, data)
	return sb.String()
}

func dashboardTemplate(data any) string {
	var sb strings.Builder
	_ = dashboardPageTmpl.Execute(&sb, data)
	return sb.String()
}

func statusTemplate(data any) string {
	var sb strings.Builder
	_ = statusPageTmpl.Execute(&sb, data)
	return sb.String()
}

func chatTemplate(data any) string {
	var sb strings.Builder
	_ = chatPageTmpl.Execute(&sb, data)
	return sb.String()
}

func transcriptTemplate(messages []sessions.Message) string {
	var sb strings.Builder
	_ = chatPageTmpl.ExecuteTemplate(&sb, "transcript", messages)
	return sb.String()
}

func goalsTemplate(data goalsPageData) string {
	var sb strings.Builder
	_ = goalsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func (m *Module) modelOptions() []modelOption {
	options := make([]modelOption, 0, len(m.options.ProviderCatalog.Entries))
	for _, entry := range m.options.ProviderCatalog.Entries {
		options = append(options, modelOption{
			Ref:   entry.ProviderID + "/" + entry.ModelID,
			Label: entry.DisplayName + " · " + entry.ProviderID,
		})
	}
	sort.SliceStable(options, func(i, j int) bool {
		return options[i].Label < options[j].Label
	})
	return options
}

func (m *Module) toolsetOptions() []string {
	if m.options.ToolRegistry == nil {
		return []string{m.defaultToolset(), "read_only"}
	}
	names := m.options.ToolRegistry.ToolsetNames()
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		switch name {
		case tools.SurfaceWebChat, "safe", "read_only", "file", "terminal", "web", "browser":
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return []string{m.defaultToolset()}
	}
	return filtered
}

func (m *Module) defaultToolset() string {
	return tools.SurfaceWebChat
}

func (m *Module) resolveToolset(ctx context.Context, name string) tools.Resolution {
	if m.options.ToolRegistry == nil {
		return tools.Resolution{
			Surface:          tools.SurfaceWebChat,
			RequestedToolset: name,
		}
	}
	return m.options.ToolRegistry.Resolve(ctx, tools.ResolveRequest{
		Surface:          tools.SurfaceWebChat,
		RequestedToolset: name,
		ProfileRoot:      m.options.Profile.Root,
		WorkingDirectory: m.options.Profile.Root,
	})
}

func splitModelRef(modelRef string, cfg config.Config) (string, string) {
	trimmed := strings.TrimSpace(modelRef)
	if trimmed == "" {
		return cfg.Model.DefaultProvider, cfg.Model.DefaultModel
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return cfg.Model.DefaultProvider, cfg.Model.DefaultModel
	}
	return parts[0], parts[1]
}

func defaultModelRef(cfg config.Config) string {
	if cfg.Model.DefaultProvider == "" || cfg.Model.DefaultModel == "" {
		return ""
	}
	return cfg.Model.DefaultProvider + "/" + cfg.Model.DefaultModel
}

func deriveSessionTitle(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) <= 48 {
		return prompt
	}
	return strings.TrimSpace(prompt[:48]) + "..."
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

type approvalsPageData struct {
	AppName  string
	CSRF     string
	Requests []approvals.Request
}

type toolsPageData struct {
	AppName  string
	WebChat  tools.Resolution
	WebAdmin tools.Resolution
}

type kanbanPageData struct {
	AppName         string
	CSRF            string
	Boards          []kanbanBoardView
	Models          []modelOption
	SelectedModel   string
	Toolsets        []string
	SelectedToolset string
	StatusOptions   []string
	PriorityOptions []string
}

type goalsPageData struct {
	AppName           string
	CSRF              string
	Sessions          []sessions.Session
	SelectedSessionID string
	SessionGoals      []goals.Goal
	ProfileGoals      []goals.Goal
}

type kanbanBoardView struct {
	Board kanban.Board
	Tasks []kanbanTaskView
}

type kanbanTaskView struct {
	Task     kanban.Task
	Comments []kanban.Comment
}

type adaptersPageData struct {
	AppName         string
	CSRF            string
	Items           []messaging.AdapterRecord
	Logs            []messaging.LogRecord
	PlatformCatalog []messaging.PlatformDefinition
}

func (m *Module) approvalsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.ApprovalService == nil {
		return e.InternalServerError("approval service unavailable", nil)
	}
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create approval form", err)
	}
	requests, err := m.options.ApprovalService.ListRecent(e.Request.Context(), m.options.Profile.Slug, 50)
	if err != nil {
		return e.InternalServerError("failed to load approvals", err)
	}
	return e.HTML(http.StatusOK, approvalsTemplate(approvalsPageData{
		AppName:  m.options.AppName,
		CSRF:     csrfToken,
		Requests: requests,
	}))
}

func (m *Module) approvalDecisionSubmit(e *core.RequestEvent, operator *core.Record) error {
	if m.options.ApprovalService == nil {
		return e.InternalServerError("approval service unavailable", nil)
	}
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	approvalID := e.Request.PathValue("approvalID")
	action := strings.TrimSpace(e.Request.FormValue("decision_action"))
	decision := "deny"
	scope := "blocked"
	switch action {
	case "allow_once":
		decision = "allow"
		scope = "once"
	case "allow_session":
		decision = "allow"
		scope = "session"
	case "allow_permanent":
		decision = "allow"
		scope = "permanent"
	}
	if _, err := m.options.ApprovalService.Decide(e.Request.Context(), approvalID, decision, scope, operator.Email()); err != nil {
		return e.InternalServerError("failed to save approval decision", err)
	}
	return e.Redirect(http.StatusSeeOther, "/dashboard/approvals")
}

func (m *Module) toolsPage(e *core.RequestEvent, _ *core.Record) error {
	if m.options.ToolRegistry == nil {
		return e.InternalServerError("tool registry unavailable", nil)
	}
	return e.HTML(http.StatusOK, toolsTemplate(toolsPageData{
		AppName:  m.options.AppName,
		WebChat:  m.options.ToolRegistry.Resolve(e.Request.Context(), tools.ResolveRequest{Surface: tools.SurfaceWebChat, ProfileRoot: m.options.Profile.Root, WorkingDirectory: m.options.Profile.Root}),
		WebAdmin: m.options.ToolRegistry.Resolve(e.Request.Context(), tools.ResolveRequest{Surface: tools.SurfaceWebAdmin, ProfileRoot: m.options.Profile.Root, WorkingDirectory: m.options.Profile.Root}),
	}))
}

func (m *Module) pendingApprovalCount(ctx context.Context) int {
	if m.options.ApprovalService == nil {
		return 0
	}
	requests, err := m.options.ApprovalService.ListPending(ctx, m.options.Profile.Slug)
	if err != nil {
		return 0
	}
	return len(requests)
}

func (m *Module) kanbanBoardCount(ctx context.Context) int {
	if m.options.KanbanService == nil {
		return 0
	}
	items, err := m.options.KanbanService.ListBoards(ctx, m.options.Profile.Slug, 100)
	if err != nil {
		return 0
	}
	return len(items)
}

func (m *Module) kanbanActiveTaskCount(ctx context.Context) int {
	if m.options.KanbanService == nil {
		return 0
	}
	items, err := m.options.KanbanService.ListActiveTasks(ctx, m.options.Profile.Slug, 100)
	if err != nil {
		return 0
	}
	return len(items)
}

func (m *Module) schedulerStatus() jobs.SchedulerStatus {
	if m.options.Scheduler == nil {
		return jobs.SchedulerStatus{}
	}
	return m.options.Scheduler.Status()
}

func parseToolPrompt(prompt string) (*tools.Invocation, error) {
	trimmed := strings.TrimSpace(prompt)
	if !strings.HasPrefix(trimmed, "/tool ") {
		return nil, nil
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/tool "))
	if body == "" {
		return nil, fmt.Errorf("tool prompt must include a tool name")
	}

	parts := strings.SplitN(body, " ", 2)
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return nil, fmt.Errorf("tool prompt must include a tool name")
	}

	args := map[string]any{}
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		if err := json.Unmarshal([]byte(parts[1]), &args); err != nil {
			return nil, fmt.Errorf("tool arguments must be valid JSON: %w", err)
		}
	}

	return &tools.Invocation{
		Name:      name,
		Arguments: args,
	}, nil
}

var approvalsPageTmpl = template.Must(template.New("approvals").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Approvals</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 68rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .request { border: 1px solid #d9e2ec; border-radius: .85rem; padding: 1rem; margin-top: 1rem; }
    .meta { color: #52606d; font-size: .9rem; }
    form { display: flex; gap: .5rem; flex-wrap: wrap; margin-top: .75rem; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
    .deny { background: #b42318; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Approvals Queue</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    {{if .Requests}}
      {{range .Requests}}
        <article class="request">
          <strong>{{.ToolName}}</strong>
          <div class="meta">Decision: {{if .Decision}}{{.Decision}}{{else}}pending{{end}} · Scope: {{if .Scope}}{{.Scope}}{{else}}pending{{end}}</div>
          <pre>{{index .Request "summary"}}</pre>
          {{if eq .Decision "pending"}}
            <form method="post" action="/dashboard/approvals/{{.ID}}/decision">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <button type="submit" name="decision_action" value="allow_once">Allow Once</button>
              <button type="submit" name="decision_action" value="allow_session">Allow Session</button>
              <button type="submit" name="decision_action" value="allow_permanent">Allow Permanent</button>
              <button type="submit" class="deny" name="decision_action" value="deny">Deny</button>
            </form>
          {{end}}
        </article>
      {{end}}
    {{else}}
      <p>No approval requests yet.</p>
    {{end}}
  </section>
</body>
</html>`))

var toolsPageTmpl = template.Must(template.New("tools").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Tools</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 68rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .tool { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
    .meta { color: #52606d; font-size: .9rem; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Tool Availability</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <h2>Web Chat</h2>
    {{range .WebChat.EnabledTools}}
      <article class="tool"><strong>{{.Definition.Name}}</strong><div class="meta">{{.Definition.Description}}</div></article>
    {{end}}
    {{range .WebChat.UnavailableTools}}
      <article class="tool"><strong>{{.Definition.Name}}</strong><div class="meta">{{.Availability.Reason}}</div></article>
    {{end}}
    <h2>Web Admin</h2>
    {{range .WebAdmin.EnabledTools}}
      <article class="tool"><strong>{{.Definition.Name}}</strong><div class="meta">{{.Definition.Description}}</div></article>
    {{end}}
    {{range .WebAdmin.UnavailableTools}}
      <article class="tool"><strong>{{.Definition.Name}}</strong><div class="meta">{{.Availability.Reason}}</div></article>
    {{end}}
  </section>
</body>
</html>`))

func approvalsTemplate(data approvalsPageData) string {
	var sb strings.Builder
	_ = approvalsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func toolsTemplate(data toolsPageData) string {
	var sb strings.Builder
	_ = toolsPageTmpl.Execute(&sb, data)
	return sb.String()
}

var kanbanPageTmpl = template.Must(template.New("kanban").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Kanban</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 84rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .board { border: 1px solid #d9e2ec; border-radius: .95rem; padding: 1rem; margin-top: 1rem; }
    .task { border: 1px solid #bcccdc; border-radius: .85rem; padding: .9rem; margin-top: .9rem; background: #f8fbfc; }
    .comment { border-left: 3px solid #0f766e; padding-left: .65rem; margin-top: .65rem; color: #334e68; }
    .grid { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr)); }
    .row { display: flex; gap: .75rem; flex-wrap: wrap; align-items: center; }
    form { display: grid; gap: .65rem; margin-top: .75rem; }
    input, textarea, select, button { font: inherit; }
    input, textarea, select { width: 100%; padding: .65rem .8rem; border: 1px solid #d9e2ec; border-radius: .65rem; box-sizing: border-box; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
    .subtle { background: #486581; }
    .danger { background: #b42318; }
    .meta { color: #52606d; font-size: .95rem; }
    code { background: #eef2f6; padding: .1rem .35rem; border-radius: .4rem; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Kanban Queue</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <div class="grid">
      <form method="post" action="/dashboard/kanban/boards">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <h2>Create Board</h2>
        <input type="text" name="name" placeholder="Board name" required>
        <textarea name="description" rows="3" placeholder="Why this board exists"></textarea>
        <input type="number" name="wip_limit" min="0" placeholder="WIP limit">
        <button type="submit">Create Board</button>
      </form>
      <form method="post" action="/dashboard/kanban/tasks">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <h2>Create Task</h2>
        <select name="board_id" required>
          <option value="">Select board</option>
          {{range .Boards}}
            <option value="{{.Board.ID}}">{{.Board.Name}}</option>
          {{end}}
        </select>
        <input type="text" name="title" placeholder="Task title" required>
        <textarea name="description" rows="3" placeholder="Task detail"></textarea>
        <textarea name="delegation_prompt" rows="3" placeholder="Delegation prompt used when dispatching"></textarea>
        <div class="row">
          <input type="text" name="assignee" placeholder="Assignee">
          <input type="text" name="parent_run_id" placeholder="Optional parent run id">
        </div>
        <div class="row">
          <select name="status">
            {{range .StatusOptions}}
              <option value="{{.}}">{{.}}</option>
            {{end}}
          </select>
          <select name="priority">
            {{range .PriorityOptions}}
              <option value="{{.}}">{{.}}</option>
            {{end}}
          </select>
          <input type="number" name="position" min="0" placeholder="Position">
        </div>
        <button type="submit">Create Task</button>
      </form>
    </div>
    {{range .Boards}}
      <article class="board">
        <h2>{{.Board.Name}}</h2>
        <div class="meta">Slug: {{.Board.Slug}} | Status: {{.Board.Status}} | Owner: {{if .Board.Owner}}{{.Board.Owner}}{{else}}unassigned{{end}} | WIP: {{.Board.WIPLimit}}</div>
        <p>{{if .Board.Description}}{{.Board.Description}}{{else}}No board description yet.{{end}}</p>
        {{range .Tasks}}
          <article class="task">
            {{$task := .Task}}
            <strong>{{.Task.Title}}</strong>
            <div class="meta">Status: {{.Task.Status}} | Queue: {{.Task.QueueState}} | Priority: {{.Task.Priority}} | Retry: {{.Task.RetryCount}} | Assignee: {{if .Task.Assignee}}{{.Task.Assignee}}{{else}}unassigned{{end}}</div>
            <div class="meta">Session: {{if .Task.SessionID}}<code>{{.Task.SessionID}}</code>{{else}}none{{end}} | Latest run: {{if .Task.LatestRunID}}<a href="/dashboard/runs/{{.Task.LatestRunID}}">{{.Task.LatestRunID}}</a>{{else}}none{{end}}</div>
            <div class="meta">Parent run: {{if .Task.ParentRunID}}<code>{{.Task.ParentRunID}}</code>{{else}}none{{end}} | Last error: {{if .Task.LastError}}{{.Task.LastError}}{{else}}none{{end}}</div>
            <p>{{if .Task.Description}}{{.Task.Description}}{{else}}No task description yet.{{end}}</p>
            <form method="post" action="/dashboard/kanban/tasks/{{.Task.ID}}/save">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <textarea name="description" rows="3">{{.Task.Description}}</textarea>
              <textarea name="delegation_prompt" rows="3">{{.Task.DelegationPrompt}}</textarea>
              <div class="row">
                <select name="status">
                  {{range $.StatusOptions}}
                    <option value="{{.}}" {{if eq . $task.Status}}selected{{end}}>{{.}}</option>
                  {{end}}
                </select>
                <select name="priority">
                  {{range $.PriorityOptions}}
                    <option value="{{.}}" {{if eq . $task.Priority}}selected{{end}}>{{.}}</option>
                  {{end}}
                </select>
                <input type="number" name="position" min="0" value="{{.Task.Position}}">
              </div>
              <div class="row">
                <input type="text" name="assignee" value="{{.Task.Assignee}}" placeholder="Assignee">
                <input type="text" name="parent_run_id" value="{{.Task.ParentRunID}}" placeholder="Parent run id">
              </div>
              <button type="submit" class="subtle">Save Task</button>
            </form>
            <form method="post" action="/dashboard/kanban/tasks/{{.Task.ID}}/dispatch">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="parent_run_id" value="{{.Task.ParentRunID}}">
              <textarea name="delegation_prompt" rows="2" placeholder="Optional override prompt">{{.Task.DelegationPrompt}}</textarea>
              <div class="row">
                <select name="model">
                  {{range $.Models}}
                    <option value="{{.Ref}}" {{if eq .Ref $.SelectedModel}}selected{{end}}>{{.Label}}</option>
                  {{end}}
                </select>
                <select name="toolset">
                  {{range $.Toolsets}}
                    <option value="{{.}}" {{if eq . $.SelectedToolset}}selected{{end}}>{{.}}</option>
                  {{end}}
                </select>
              </div>
              <div class="row">
                <button type="submit">Dispatch</button>
                <button type="submit" formaction="/dashboard/kanban/tasks/{{.Task.ID}}/retry" class="subtle">Retry</button>
                <button type="submit" formaction="/dashboard/kanban/tasks/{{.Task.ID}}/cancel" class="danger">Cancel</button>
              </div>
            </form>
            <form method="post" action="/dashboard/kanban/tasks/{{.Task.ID}}/comments">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <textarea name="comment" rows="2" placeholder="Add a note or operator handoff"></textarea>
              <button type="submit" class="subtle">Add Comment</button>
            </form>
            <h3>Comments</h3>
            {{range .Comments}}
              <div class="comment">
                <strong>{{.Author}}</strong> | {{.Kind}}{{if .RunID}} | run <a href="/dashboard/runs/{{.RunID}}">{{.RunID}}</a>{{end}}
                <div>{{.Body}}</div>
              </div>
            {{else}}
              <p class="meta">No comments yet.</p>
            {{end}}
          </article>
        {{else}}
          <p class="meta">No tasks on this board yet.</p>
        {{end}}
      </article>
    {{else}}
      <p>No boards yet. Create one to start dispatching delegated work.</p>
    {{end}}
  </section>
</body>
</html>`))

func kanbanTemplate(data kanbanPageData) string {
	var sb strings.Builder
	_ = kanbanPageTmpl.Execute(&sb, data)
	return sb.String()
}

var sessionsPageTmpl = template.Must(template.New("sessions").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Sessions</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 72rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
    form { display: flex; gap: .5rem; }
    input, button { font: inherit; }
    input { flex: 1; padding: .6rem .75rem; border: 1px solid #d9e2ec; border-radius: .6rem; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Sessions</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <form method="get" action="/dashboard/sessions">
      <input type="search" name="q" value="{{.Query}}" placeholder="Search sessions and messages">
      <button type="submit">Search</button>
    </form>
    <h2>Recent Sessions</h2>
    {{range .List}}
      <article class="item">
        <strong>{{.Title}}</strong>
        <div>Status: {{.Status}} · Source: {{.Source}}</div>
        <div><a href="/chat?session={{.ID}}">Open chat</a> · <a href="/dashboard/goals?session={{.ID}}">View goals</a></div>
      </article>
    {{else}}
      <p>No sessions yet.</p>
    {{end}}
    <h2>Search Results</h2>
    {{range .Search}}
      <article class="item">
        <strong>{{.Title}}</strong>
        <div>{{.Snippet}}</div>
        <div><a href="/chat?session={{.SessionID}}">Open session</a></div>
      </article>
    {{else}}
      <p>No search results.</p>
    {{end}}
  </section>
</body>
</html>`))

var goalsPageTmpl = template.Must(template.New("goals").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Goals</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 78rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .grid { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(18rem, 1fr)); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .9rem; margin-top: .85rem; }
    .meta { color: #52606d; font-size: .95rem; }
    form { display: grid; gap: .6rem; margin-top: .6rem; }
    input, textarea, select, button { font: inherit; }
    input, textarea, select { width: 100%; padding: .65rem .8rem; border: 1px solid #d9e2ec; border-radius: .65rem; box-sizing: border-box; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
    .subtle { background: #486581; }
    .danger { background: #b42318; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Persistent Goals</h1>
    <p><a href="/dashboard">Back to dashboard</a> · {{if .SelectedSessionID}}<a href="/chat?session={{.SelectedSessionID}}">Open session chat</a>{{end}}</p>
    <div class="grid">
      <form method="get" action="/dashboard/goals">
        <h2>Session Focus</h2>
        <select name="session">
          <option value="">Profile-only view</option>
          {{range .Sessions}}
            <option value="{{.ID}}" {{if eq $.SelectedSessionID .ID}}selected{{end}}>{{.Title}}</option>
          {{end}}
        </select>
        <button type="submit" class="subtle">Load Goals</button>
      </form>
      <form method="post" action="/dashboard/goals/profile">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <h2>Create Profile Goal</h2>
        <input type="text" name="title" placeholder="Goal title" required>
        <textarea name="statement" rows="3" placeholder="What should remain true?" required></textarea>
        <textarea name="success_criteria" rows="2" placeholder="How do we know it is done?"></textarea>
        <select name="priority">
          <option value="high">high</option>
          <option value="medium" selected>medium</option>
          <option value="low">low</option>
        </select>
        <button type="submit">Create Profile Goal</button>
      </form>
      <form method="post" action="/dashboard/goals/session">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <h2>Create Session Goal</h2>
        <select name="session_id" required>
          <option value="">Select session</option>
          {{range .Sessions}}
            <option value="{{.ID}}" {{if eq $.SelectedSessionID .ID}}selected{{end}}>{{.Title}}</option>
          {{end}}
        </select>
        <input type="text" name="title" placeholder="Goal title" required>
        <textarea name="statement" rows="3" placeholder="What should this session accomplish?" required></textarea>
        <textarea name="success_criteria" rows="2" placeholder="Definition of done"></textarea>
        <select name="priority">
          <option value="high">high</option>
          <option value="medium" selected>medium</option>
          <option value="low">low</option>
        </select>
        <button type="submit">Create Session Goal</button>
      </form>
    </div>
    <div class="grid">
      <section>
        <h2>Profile Goals</h2>
        {{range .ProfileGoals}}
          <article class="item">
            <strong>{{.Title}}</strong>
            <div class="meta">{{.Status}} · {{.Priority}}</div>
            <p>{{.Statement}}</p>
            <form method="post" action="/dashboard/goals/profile/{{.ID}}/save">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <input type="text" name="title" value="{{.Title}}">
              <textarea name="statement" rows="3">{{.Statement}}</textarea>
              <textarea name="success_criteria" rows="2">{{.SuccessCriteria}}</textarea>
              <div class="grid">
                <select name="status">
                  <option value="active" {{if eq .Status "active"}}selected{{end}}>active</option>
                  <option value="in_review" {{if eq .Status "in_review"}}selected{{end}}>in_review</option>
                  <option value="satisfied" {{if eq .Status "satisfied"}}selected{{end}}>satisfied</option>
                  <option value="blocked" {{if eq .Status "blocked"}}selected{{end}}>blocked</option>
                </select>
                <select name="priority">
                  <option value="high" {{if eq .Priority "high"}}selected{{end}}>high</option>
                  <option value="medium" {{if eq .Priority "medium"}}selected{{end}}>medium</option>
                  <option value="low" {{if eq .Priority "low"}}selected{{end}}>low</option>
                </select>
              </div>
              <button type="submit" class="subtle">Save Goal</button>
            </form>
            <form method="post" action="/dashboard/goals/profile/{{.ID}}/evaluate">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <input type="text" name="outcome" placeholder="Outcome (met, partial, blocked)">
              <textarea name="summary" rows="2" placeholder="Evaluation summary"></textarea>
              <select name="status">
                <option value="active">active</option>
                <option value="in_review">in_review</option>
                <option value="satisfied">satisfied</option>
                <option value="blocked">blocked</option>
              </select>
              <button type="submit">Record Evaluation</button>
            </form>
            <form method="post" action="/dashboard/goals/profile/{{.ID}}/clear">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <button type="submit" class="danger">Clear Goal</button>
            </form>
          </article>
        {{else}}
          <p>No active profile goals.</p>
        {{end}}
      </section>
      <section>
        <h2>Session Goals</h2>
        {{range .SessionGoals}}
          <article class="item">
            <strong>{{.Title}}</strong>
            <div class="meta">{{.Status}} · {{.Priority}}</div>
            <p>{{.Statement}}</p>
            <form method="post" action="/dashboard/goals/session/{{.ID}}/save">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <input type="text" name="title" value="{{.Title}}">
              <textarea name="statement" rows="3">{{.Statement}}</textarea>
              <textarea name="success_criteria" rows="2">{{.SuccessCriteria}}</textarea>
              <div class="grid">
                <select name="status">
                  <option value="active" {{if eq .Status "active"}}selected{{end}}>active</option>
                  <option value="in_review" {{if eq .Status "in_review"}}selected{{end}}>in_review</option>
                  <option value="satisfied" {{if eq .Status "satisfied"}}selected{{end}}>satisfied</option>
                  <option value="blocked" {{if eq .Status "blocked"}}selected{{end}}>blocked</option>
                </select>
                <select name="priority">
                  <option value="high" {{if eq .Priority "high"}}selected{{end}}>high</option>
                  <option value="medium" {{if eq .Priority "medium"}}selected{{end}}>medium</option>
                  <option value="low" {{if eq .Priority "low"}}selected{{end}}>low</option>
                </select>
              </div>
              <button type="submit" class="subtle">Save Goal</button>
            </form>
            <form method="post" action="/dashboard/goals/session/{{.ID}}/evaluate">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <input type="text" name="outcome" placeholder="Outcome (met, partial, blocked)">
              <textarea name="summary" rows="2" placeholder="Evaluation summary"></textarea>
              <select name="status">
                <option value="active">active</option>
                <option value="in_review">in_review</option>
                <option value="satisfied">satisfied</option>
                <option value="blocked">blocked</option>
              </select>
              <button type="submit">Record Evaluation</button>
            </form>
            <form method="post" action="/dashboard/goals/session/{{.ID}}/clear">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <input type="hidden" name="session_id" value="{{$.SelectedSessionID}}">
              <button type="submit" class="danger">Clear Goal</button>
            </form>
          </article>
        {{else}}
          <p>{{if .SelectedSessionID}}No active session goals.{{else}}Select a session to inspect session-scoped goals.{{end}}</p>
        {{end}}
      </section>
    </div>
  </section>
</body>
</html>`))

var runDetailPageTmpl = template.Must(template.New("run-detail").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Run Detail</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 72rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .event { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
    pre { white-space: pre-wrap; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Run Detail</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <p>Run: {{.Run.ID}}</p>
    <p>Status: {{.Run.Status}}</p>
    <p>Trigger: {{.Run.TriggerSource}}</p>
    <p>Error: {{if .Run.ErrorMessage}}{{.Run.ErrorMessage}}{{else}}none{{end}}</p>
    <h2>Events</h2>
    {{range .Events}}
      <article class="event">
        <strong>{{.Type}}</strong>
        <div>Sequence {{.Sequence}} · {{.Timestamp}}</div>
        <pre>{{printf "%v" .Payload}}</pre>
      </article>
    {{else}}
      <p>No events recorded.</p>
    {{end}}
  </section>
</body>
</html>`))

var jobsPageTmpl = template.Must(template.New("jobs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Jobs</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 72rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
    form { display: inline-flex; gap: .5rem; margin-top: .5rem; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Cron Jobs</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    {{range .Jobs}}
      <article class="item">
        <strong>{{.Name}}</strong>
        <div>{{.ScheduleKind}} · {{.ScheduleValue}} · enabled={{.Enabled}}</div>
        <div><a href="/dashboard/jobs?job={{.ID}}">History</a></div>
      </article>
    {{else}}
      <p>No jobs configured yet.</p>
    {{end}}
    <h2>Selected Job History</h2>
    {{range .History}}
      <article class="item">
        <strong>{{.Status}}</strong>
        <div>Run {{if .RunID}}{{.RunID}}{{else}}not linked{{end}}</div>
        <div>{{.OutputExcerpt}}</div>
      </article>
    {{else}}
      <p>Select a job to inspect run history.</p>
    {{end}}
  </section>
</body>
</html>`))

var batchesPageTmpl = template.Must(template.New("batches").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Batch Jobs</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 78rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .grid { display: grid; gap: 1rem; grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr)); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
    form { display: grid; gap: .65rem; }
    input, textarea, button { font: inherit; }
    input, textarea { width: 100%; padding: .65rem .8rem; border: 1px solid #d9e2ec; border-radius: .65rem; box-sizing: border-box; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Batch Jobs</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <div class="grid">
      <form method="post" action="/dashboard/batches">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <h2>Create Batch</h2>
        <input type="text" name="name" placeholder="Batch name" required>
        <input type="text" name="toolset" value="safe" placeholder="Toolset">
        <textarea name="prompts" rows="8" placeholder="One prompt per line" required></textarea>
        <button type="submit">Create Batch Job</button>
      </form>
      <section>
        <h2>Current Jobs</h2>
        {{range .Jobs}}
          <article class="item">
            <strong>{{.Name}}</strong>
            <div>Status: {{.Status}} · Items: {{.CompletedCount}}/{{.ItemCount}} completed · Failed: {{.FailedCount}}</div>
            <div>Model: {{.ModelID}} · Toolset: {{.Toolset}}</div>
            <div><a href="/dashboard/batches?job={{.ID}}">Inspect attempts</a></div>
            <form method="post" action="/dashboard/batches/{{.ID}}/run">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <button type="submit">Run / Resume</button>
            </form>
            <form method="post" action="/dashboard/batches/{{.ID}}/export">
              <input type="hidden" name="csrf" value="{{$.CSRF}}">
              <button type="submit">Export Trajectory</button>
            </form>
          </article>
        {{else}}
          <p>No batch jobs yet.</p>
        {{end}}
      </section>
    </div>
    <h2>Selected Attempt History</h2>
    {{range .Attempts}}
      <article class="item">
        <strong>{{.ItemID}}</strong>
        <div>Status: {{.Status}} · Run: {{if .RunID}}{{.RunID}}{{else}}not started{{end}}</div>
        <div>{{.Prompt}}</div>
        <div>{{if .OutputText}}{{.OutputText}}{{else}}{{.ErrorMessage}}{{end}}</div>
      </article>
    {{else}}
      <p>Select a batch job to inspect attempts.</p>
    {{end}}
  </section>
</body>
</html>`))

var adaptersPageTmpl = template.Must(template.New("adapters").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Messaging Adapters</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 76rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: 1rem; margin-top: .9rem; }
    .log { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; background: #f8fbfc; }
    .meta { color: #52606d; font-size: .95rem; }
    form { display: grid; gap: .65rem; margin-top: .75rem; }
    textarea, input, button { font: inherit; }
    textarea, input[type="text"] { width: 100%; padding: .65rem .8rem; border: 1px solid #d9e2ec; border-radius: .65rem; }
    .row { display: flex; gap: .75rem; flex-wrap: wrap; align-items: center; }
    button { border: 0; border-radius: .65rem; padding: .65rem .9rem; background: #0f766e; color: white; }
    code { background: #eef2f6; padding: .1rem .35rem; border-radius: .4rem; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Messaging Adapters</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <p>Webhook ingress format posts to <code>/gateway/webhooks/&lt;adapterID&gt;</code> with a JSON body containing <code>actor</code>, <code>chat_id</code>, <code>thread_id</code>, <code>text</code>, optional <code>attachments</code>, and optional <code>metadata</code>.</p>
    <h2>Configured Adapters</h2>
    {{range .Items}}
      <article class="item">
        <strong>{{.Platform}}</strong>
        <div class="meta">Status: {{.Status}} · Auth: {{if .AuthMode}}{{.AuthMode}}{{else}}unset{{end}} · Enabled: {{.Enabled}} · Last error: {{if .LastError}}{{.LastError}}{{else}}none{{end}}</div>
        <div class="meta">Allowlist: {{if .Allowlist}}{{range .Allowlist}}{{.}} {{end}}{{else}}none{{end}}</div>
        <form method="post" action="/dashboard/adapters/{{.ID}}/save">
          <input type="hidden" name="csrf" value="{{$.CSRF}}">
          <div class="row">
            <label><input type="checkbox" name="enabled" {{if .Enabled}}checked{{end}}> enabled</label>
            <input type="text" name="status" value="{{.Status}}" placeholder="status">
            <input type="text" name="auth_mode" value="{{.AuthMode}}" placeholder="auth mode">
          </div>
          <textarea name="allowlist" rows="3" placeholder="one identity or CIDR per line">{{range .Allowlist}}{{.}}
{{end}}</textarea>
          <div class="row">
            <button type="submit">Save</button>
          </div>
        </form>
        <form method="post" action="/dashboard/adapters/{{.ID}}/reconnect">
          <input type="hidden" name="csrf" value="{{$.CSRF}}">
          <button type="submit">Reconnect / Probe Health</button>
        </form>
      </article>
    {{else}}
      <p>No messaging adapters registered yet.</p>
    {{end}}
    <h2>Platform Catalog</h2>
    {{range .PlatformCatalog}}
      <article class="item">
        <strong>{{.Name}}</strong>
        <div class="meta">Phase {{.Phase}} · Auth placeholders: {{range .AuthPlaceholders}}{{.}} {{end}}</div>
      </article>
    {{end}}
    <h2>Recent Logs</h2>
    {{range .Logs}}
      <article class="log">
        <strong>{{.Platform}}</strong>
        <div class="meta">{{.Direction}} · {{.Status}} · Session {{if .SessionKey}}{{.SessionKey}}{{else}}n/a{{end}}</div>
        <div>{{if .Summary}}{{.Summary}}{{else}}no summary{{end}}</div>
        <div class="meta">{{if .ErrorMessage}}{{.ErrorMessage}}{{else}}no error{{end}}</div>
      </article>
    {{else}}
      <p>No adapter logs yet.</p>
    {{end}}
  </section>
</body>
</html>`))

var skillsPageTmpl = template.Must(template.New("skills").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Skills</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 72rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    .item { border: 1px solid #d9e2ec; border-radius: .85rem; padding: .8rem; margin-top: .75rem; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Skills</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    {{range .Skills}}
      <article class="item">
        <strong>{{.Name}}</strong>
        <div>Slug: {{.Slug}} · State: {{.State}} · Trust: {{.TrustLevel}}</div>
        <div>Path: {{.RootPath}}/{{.EntryFile}}</div>
      </article>
    {{else}}
      <p>No skills registered yet.</p>
    {{end}}
  </section>
</body>
</html>`))

var logsPageTmpl = template.Must(template.New("logs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Logs</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 72rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
    pre { white-space: pre-wrap; background: #102a43; color: #f0f4f8; padding: 1rem; border-radius: .85rem; }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Logs</h1>
    <p><a href="/dashboard">Back to dashboard</a></p>
    <p>Files: {{range .Files}}<a href="/dashboard/logs?file={{.}}">{{.}}</a> {{else}}none{{end}}</p>
    <h2>{{if .Current}}{{.Current}}{{else}}No log selected{{end}}</h2>
    <pre>{{if .Content}}{{.Content}}{{else}}No log content yet.{{end}}</pre>
  </section>
</body>
</html>`))

func sessionsTemplate(data any) string {
	var sb strings.Builder
	_ = sessionsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func runDetailTemplate(data any) string {
	var sb strings.Builder
	_ = runDetailPageTmpl.Execute(&sb, data)
	return sb.String()
}

func jobsTemplate(data any) string {
	var sb strings.Builder
	_ = jobsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func batchesTemplate(data any) string {
	var sb strings.Builder
	_ = batchesPageTmpl.Execute(&sb, data)
	return sb.String()
}

func adaptersTemplate(data adaptersPageData) string {
	var sb strings.Builder
	_ = adaptersPageTmpl.Execute(&sb, data)
	return sb.String()
}

func skillsTemplate(data any) string {
	var sb strings.Builder
	_ = skillsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func logsTemplate(data any) string {
	var sb strings.Builder
	_ = logsPageTmpl.Execute(&sb, data)
	return sb.String()
}

func splitAllowlist(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	items := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func firstValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseBatchPrompts(raw string) []batchsvc.Item {
	lines := strings.Split(raw, "\n")
	items := make([]batchsvc.Item, 0, len(lines))
	for index, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			items = append(items, batchsvc.Item{
				ID:     fmt.Sprintf("dashboard-%03d", index+1),
				Prompt: trimmed,
			})
		}
	}
	return items
}

func (m *Module) promptGoals(ctx context.Context, sessionID string) ([]goals.Goal, []goals.Goal, error) {
	if m.options.GoalService == nil {
		return nil, nil, nil
	}
	return m.options.GoalService.ListActiveGoals(ctx, m.options.Profile.Slug, sessionID)
}

func (m *Module) batchExecutor() batchsvc.RuntimeExecutor {
	return batchsvc.RuntimeExecutor{
		Profile:       m.options.Profile,
		Config:        m.options.LoadedConfig,
		Sessions:      m.options.SessionService,
		GoalService:   m.options.GoalService,
		PromptBuilder: m.options.PromptBuilder,
		Orchestrator:  m.options.Orchestrator,
		ToolRegistry:  m.options.ToolRegistry,
	}
}
