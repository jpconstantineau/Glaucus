package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/approvals"
	"github.com/jpconstantineau/Glaucus/internal/config"
	exportsvc "github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
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
	JobService              *jobs.Service
	SearchService           *search.Service
	SkillsService           *skills.Service
	ExportService           *exportsvc.Service
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
	rg.GET("/login", m.loginPage)
	rg.POST("/login", m.loginSubmit)
	rg.POST("/logout", m.withOperatorAuth(m.logoutSubmit))
	rg.GET("/dashboard", m.withOperatorAuth(m.dashboardShell))
	rg.GET("/dashboard/status", m.withOperatorAuth(m.dashboardStatusPage))
	rg.GET("/dashboard/sessions", m.withOperatorAuth(m.sessionsPage))
	rg.GET("/dashboard/runs/{runID}", m.withOperatorAuth(m.runDetailPage))
	rg.GET("/dashboard/jobs", m.withOperatorAuth(m.jobsPage))
	rg.POST("/dashboard/jobs/{jobID}/{action}", m.withOperatorAuth(m.jobActionSubmit))
	rg.GET("/dashboard/skills", m.withOperatorAuth(m.skillsPage))
	rg.POST("/dashboard/skills/{slug}/{action}", m.withOperatorAuth(m.skillActionSubmit))
	rg.GET("/dashboard/logs", m.withOperatorAuth(m.logsPage))
	rg.GET("/dashboard/approvals", m.withOperatorAuth(m.approvalsPage))
	rg.POST("/dashboard/approvals/{approvalID}/decision", m.withOperatorAuth(m.approvalDecisionSubmit))
	rg.GET("/dashboard/tools", m.withOperatorAuth(m.toolsPage))
	rg.GET("/chat", m.withOperatorAuth(m.chatPage))
	rg.GET("/chat/transcript", m.withOperatorAuth(m.chatTranscript))
	rg.POST("/chat/send", m.withOperatorAuth(m.chatSend))
	rg.GET("/health/detailed", m.withOperatorAuth(m.detailedHealth))
	rg.GET("/api/version", m.withOperatorAuth(m.versionInfo))
	rg.GET("/api/dashboard/runs/{runID}/stream", m.withOperatorAuth(m.streamRunEvents))
	rg.GET("/api/dashboard/sessions/{sessionID}/stream", m.withOperatorAuth(m.streamSessionEvents))
	rg.GET("/api/dashboard/status/stream", m.withOperatorAuth(m.streamStatusEvents))

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

func (m *Module) loginPage(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create login form", err)
	}

	data := struct {
		AppName              string
		CSRF                 string
		DefaultOperatorEmail string
	}{
		AppName:              m.options.AppName,
		CSRF:                 csrfToken,
		DefaultOperatorEmail: m.options.DefaultOperatorEmail,
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
		return e.BadRequestError("email and password are required", nil)
	}

	record, err := e.App.FindAuthRecordByEmail("operators", email)
	if err != nil || !record.ValidatePassword(password) {
		return e.UnauthorizedError("invalid credentials", err)
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
		CSRF             string
	}{
		AppName:          m.options.AppName,
		OperatorEmail:    operator.Email(),
		ProfileSlug:      m.options.Profile.Slug,
		ProviderCount:    len(m.options.ProviderCatalog.Entries),
		PendingApprovals: m.pendingApprovalCount(e.Request.Context()),
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
		promptDoc, err = m.options.PromptBuilder.Build(runtime.PromptBuildInput{
			Profile:         m.options.Profile,
			Session:         session,
			ToolBehavior:    "Use toolset " + fallbackString(toolsetRef, m.defaultToolset()) + " unless no tools are needed.",
			ProjectContext:  "Current profile root: " + m.options.Profile.Root,
			PlatformHint:    "This turn originated from the browser chat surface.",
			ProviderOverlay: "Prefer the selected provider/model unless a deterministic fallback is required.",
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

func (m *Module) versionInfo(e *core.RequestEvent, _ *core.Record) error {
	return e.JSON(http.StatusOK, map[string]any{
		"name":     m.options.AppName,
		"version":  m.options.Version,
		"commit":   m.options.Commit,
		"built_at": m.options.BuiltAt,
	})
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

func (m *Module) withOperatorAuth(next func(*core.RequestEvent, *core.Record) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := m.requireLocalHost(e); err != nil {
			return err
		}

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
  </style>
</head>
<body>
  <main>
    <h1>{{.AppName}}</h1>
    <p class="hint">Sign in with the local operator account. Default bootstrap email: <strong>{{.DefaultOperatorEmail}}</strong></p>
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
    </section>
    <section class="panel">
      <h2>Operator Console</h2>
      <p>Open the operator status page at <a href="/dashboard/status">/dashboard/status</a>, the browser chat at <a href="/chat">/chat</a>, and the sessions explorer at <a href="/dashboard/sessions">/dashboard/sessions</a>.</p>
      <p>Inspect run detail from <a href="/dashboard/runs/">/dashboard/runs/&lt;runID&gt;</a>, cron jobs at <a href="/dashboard/jobs">/dashboard/jobs</a>, skills at <a href="/dashboard/skills">/dashboard/skills</a>, and logs at <a href="/dashboard/logs">/dashboard/logs</a>.</p>
      <p>Review approval requests at <a href="/dashboard/approvals">/dashboard/approvals</a> and inspect effective tool availability at <a href="/dashboard/tools">/dashboard/tools</a>.</p>
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
        <div><a href="/chat?session={{.ID}}">Open chat</a></div>
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
