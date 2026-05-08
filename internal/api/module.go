package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

const schemaVersion = "2026-05-01"

type Options struct {
	Profile         profile.ActiveProfile
	Config          config.Config
	ProviderCatalog providers.Catalog
	Router          *providers.Router
	SessionService  *sessions.Service
	JobService      *jobs.Service
	EventService    *runtime.EventService
	PromptBuilder   *runtime.PromptBuilder
	ToolRegistry    *tools.Registry
	Orchestrator    *runtime.Orchestrator
}

type Module struct {
	options Options
}

func Register(app core.App, options Options) *Module {
	module := &Module{options: options}
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		module.BindRoutes(se.Router)
		return se.Next()
	})
	return module
}

func (m *Module) BindRoutes(rg *router.Router[*core.RequestEvent]) {
	rg.POST("/v1/chat/completions", m.withBearerAuth(m.chatCompletions))
	rg.POST("/v1/responses", m.withBearerAuth(m.responsesCreate))
	rg.GET("/v1/responses/{responseID}", m.withBearerAuth(m.responsesGet))
	rg.DELETE("/v1/responses/{responseID}", m.withBearerAuth(m.responsesDelete))
	rg.POST("/v1/runs", m.withBearerAuth(m.runsCreate))
	rg.GET("/v1/runs/{runID}", m.withBearerAuth(m.runsGet))
	rg.GET("/v1/runs/{runID}/events", m.withBearerAuth(m.runsEvents))
	rg.POST("/v1/runs/{runID}/stop", m.withBearerAuth(m.runsStop))
	rg.GET("/v1/jobs", m.withBearerAuth(m.jobsList))
	rg.POST("/v1/jobs", m.withBearerAuth(m.jobsCreate))
	rg.GET("/v1/jobs/{jobID}", m.withBearerAuth(m.jobsGet))
	rg.PATCH("/v1/jobs/{jobID}", m.withBearerAuth(m.jobsPatch))
	rg.POST("/v1/jobs/{jobID}/pause", m.withBearerAuth(m.jobsPause))
	rg.POST("/v1/jobs/{jobID}/resume", m.withBearerAuth(m.jobsResume))
	rg.POST("/v1/jobs/{jobID}/run", m.withBearerAuth(m.jobsRun))
	rg.DELETE("/v1/jobs/{jobID}", m.withBearerAuth(m.jobsDelete))
	rg.GET("/v1/models", m.withBearerAuth(m.modelsList))
	rg.GET("/v1/capabilities", m.withBearerAuth(m.capabilitiesGet))
}

type apiHandler func(*core.RequestEvent) error

func (m *Module) withBearerAuth(next apiHandler) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if len(m.options.Config.API.BearerTokens) == 0 {
			return m.writeError(e, http.StatusServiceUnavailable, "api bearer auth is not configured")
		}

		token := strings.TrimSpace(strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer "))
		if token == "" {
			return m.writeError(e, http.StatusUnauthorized, "missing bearer token")
		}
		for _, candidate := range m.options.Config.API.BearerTokens {
			if token == candidate {
				e.Response.Header().Set("X-Glaucus-Schema-Version", schemaVersion)
				return next(e)
			}
		}
		return m.writeError(e, http.StatusUnauthorized, "invalid bearer token")
	}
}

type chatCompletionsRequest struct {
	Model       string                     `json:"model"`
	Messages    []providers.RequestMessage `json:"messages"`
	MaxTokens   int                        `json:"max_tokens"`
	Temperature float64                    `json:"temperature"`
	Stream      bool                       `json:"stream"`
}

func (m *Module) chatCompletions(e *core.RequestEvent) error {
	var req chatCompletionsRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return m.writeError(e, http.StatusBadRequest, "invalid request body")
	}
	if len(req.Messages) == 0 {
		return m.writeError(e, http.StatusBadRequest, "messages are required")
	}

	normalized := providers.NormalizedRequest{
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	response, resolution, _, err := m.options.Router.ExecuteWithFallback(
		e.Request.Context(),
		providers.ResolutionInput{ModelID: req.Model, RequiredCapabilities: []string{"chat"}},
		normalized,
	)
	if err != nil {
		return m.writeError(e, http.StatusBadGateway, err.Error())
	}

	payload := map[string]any{
		"id":      "chatcmpl_" + responseID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resolution.ModelID,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": response.OutputText,
			},
			"finish_reason": fallbackString(response.StopReason, "stop"),
		}},
		"usage": response.Usage,
	}
	if req.Stream {
		return writeTextStream(e, "chat.completion.chunk", chunkText(response.OutputText), func(chunk string, index int, final bool) map[string]any {
			if final {
				return map[string]any{
					"id":      payload["id"],
					"object":  "chat.completion.chunk",
					"created": payload["created"],
					"model":   resolution.ModelID,
					"choices": []map[string]any{{
						"index":         0,
						"delta":         map[string]any{},
						"finish_reason": fallbackString(response.StopReason, "stop"),
					}},
				}
			}
			return map[string]any{
				"id":      payload["id"],
				"object":  "chat.completion.chunk",
				"created": payload["created"],
				"model":   resolution.ModelID,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{"content": chunk},
				}},
			}
		})
	}
	return e.JSON(http.StatusOK, payload)
}

type responseInputItem struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type responsesRequest struct {
	Model        string              `json:"model"`
	Instructions string              `json:"instructions"`
	Stream       bool                `json:"stream"`
	Input        []responseInputItem `json:"input"`
}

func (m *Module) responsesCreate(e *core.RequestEvent) error {
	var req responsesRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return m.writeError(e, http.StatusBadRequest, "invalid request body")
	}
	if len(req.Input) == 0 {
		return m.writeError(e, http.StatusBadRequest, "input is required")
	}

	session, userMessage, normalized, err := m.createResponseSession(e.Request.Context(), req)
	if err != nil {
		return m.writeError(e, http.StatusBadRequest, err.Error())
	}

	result, err := m.options.Orchestrator.Execute(e.Request.Context(), runtime.ExecuteRunInput{
		ProfileID:        m.options.Profile.Slug,
		SessionID:        session.ID,
		TriggerSource:    "api.responses",
		UserMessageID:    userMessage.ID,
		Surface:          "api",
		Actor:            "bearer",
		ApprovalMode:     "manual",
		Request:          normalized,
		Resolution:       providers.ResolutionInput{ModelID: req.Model, RequiredCapabilities: []string{"chat"}},
		WorkingDirectory: m.options.Profile.Root,
	})
	if err != nil {
		return m.writeError(e, http.StatusBadGateway, err.Error())
	}

	assistantMessage, err := m.options.SessionService.CreateMessage(e.Request.Context(), sessions.CreateMessageInput{
		ProfileID:   m.options.Profile.Slug,
		SessionID:   session.ID,
		RunID:       result.Run.ID,
		Role:        "assistant",
		Content:     sessions.MessageContent{{Type: "output_text", Text: result.Response.OutputText}},
		VisibleText: result.Response.OutputText,
		Usage:       result.Response.Usage,
	})
	if err != nil {
		return m.writeError(e, http.StatusInternalServerError, err.Error())
	}

	payload := renderStoredResponse(result.Run.ID, result.Response.Model, assistantMessage)
	if req.Stream {
		return writeTextStream(e, "response.output_text.delta", chunkText(result.Response.OutputText), func(chunk string, index int, final bool) map[string]any {
			if final {
				return map[string]any{
					"type":     "response.completed",
					"response": payload,
				}
			}
			return map[string]any{
				"type":  "response.output_text.delta",
				"id":    result.Run.ID,
				"delta": chunk,
			}
		})
	}
	return e.JSON(http.StatusOK, payload)
}

func (m *Module) responsesGet(e *core.RequestEvent) error {
	responseID := e.Request.PathValue("responseID")
	run, assistantMessage, err := m.loadStoredResponse(e.Request.Context(), responseID)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, err.Error())
	}
	return e.JSON(http.StatusOK, renderStoredResponse(run.ID, resolvedModel(run), assistantMessage))
}

func (m *Module) responsesDelete(e *core.RequestEvent) error {
	responseID := e.Request.PathValue("responseID")
	if err := m.options.SessionService.DeleteRun(e.Request.Context(), responseID); err != nil {
		return m.writeError(e, http.StatusNotFound, err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{
		"id":      responseID,
		"object":  "response.deleted",
		"deleted": true,
	})
}

type runsRequest struct {
	Model        string                     `json:"model"`
	Instructions string                     `json:"instructions"`
	Messages     []providers.RequestMessage `json:"messages"`
}

func (m *Module) runsCreate(e *core.RequestEvent) error {
	var req runsRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return m.writeError(e, http.StatusBadRequest, "invalid request body")
	}
	if len(req.Messages) == 0 {
		return m.writeError(e, http.StatusBadRequest, "messages are required")
	}

	session, userMessage, normalized, err := m.createSessionFromMessages(e.Request.Context(), "api.runs", req.Model, req.Messages, req.Instructions)
	if err != nil {
		return m.writeError(e, http.StatusBadRequest, err.Error())
	}

	result, err := m.options.Orchestrator.Execute(e.Request.Context(), runtime.ExecuteRunInput{
		ProfileID:        m.options.Profile.Slug,
		SessionID:        session.ID,
		TriggerSource:    "api.runs",
		UserMessageID:    userMessage.ID,
		Surface:          "api",
		Actor:            "bearer",
		ApprovalMode:     "manual",
		Request:          normalized,
		Resolution:       providers.ResolutionInput{ModelID: req.Model, RequiredCapabilities: []string{"chat"}},
		WorkingDirectory: m.options.Profile.Root,
	})
	if err != nil {
		return m.writeError(e, http.StatusBadGateway, err.Error())
	}

	var assistant *sessions.Message
	if result.Response.OutputText != "" {
		message, createErr := m.options.SessionService.CreateMessage(e.Request.Context(), sessions.CreateMessageInput{
			ProfileID:   m.options.Profile.Slug,
			SessionID:   session.ID,
			RunID:       result.Run.ID,
			Role:        "assistant",
			Content:     sessions.MessageContent{{Type: "output_text", Text: result.Response.OutputText}},
			VisibleText: result.Response.OutputText,
			Usage:       result.Response.Usage,
		})
		if createErr != nil {
			return m.writeError(e, http.StatusInternalServerError, createErr.Error())
		}
		assistant = &message
	}

	return e.JSON(http.StatusOK, renderRunResource(result.Run, assistant))
}

func (m *Module) runsGet(e *core.RequestEvent) error {
	runID := e.Request.PathValue("runID")
	run, err := m.options.SessionService.GetRun(e.Request.Context(), runID)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "run not found")
	}
	var assistant *sessions.Message
	messages, _ := m.options.SessionService.ListMessagesByRun(e.Request.Context(), runID)
	for _, message := range messages {
		if message.Role == "assistant" {
			assistant = &message
			break
		}
	}
	return e.JSON(http.StatusOK, renderRunResource(run, assistant))
}

func (m *Module) runsEvents(e *core.RequestEvent) error {
	if m.options.EventService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "event service unavailable")
	}
	runID := e.Request.PathValue("runID")
	after, _ := strconv.Atoi(strings.TrimSpace(e.Request.URL.Query().Get("after")))
	events, err := m.options.EventService.ListRunEvents(e.Request.Context(), runID, after)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "run not found")
	}
	if strings.Contains(e.Request.Header.Get("Accept"), "application/json") {
		return e.JSON(http.StatusOK, map[string]any{"data": events})
	}

	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-cache")
	e.Response.Header().Set("Connection", "keep-alive")
	for _, event := range events {
		payload, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(e.Response, "event: %s\ndata: %s\n\n", event.Type, payload); err != nil {
			return err
		}
	}
	if len(events) == 0 || e.Request.URL.Query().Get("once") == "1" || events[len(events)-1].IsTerminal {
		return nil
	}

	ch, unsubscribe := m.options.EventService.SubscribeRun(runID)
	defer unsubscribe()
	select {
	case <-e.Request.Context().Done():
		return nil
	case event := <-ch:
		payload, _ := json.Marshal(event)
		_, err := fmt.Fprintf(e.Response, "event: %s\ndata: %s\n\n", event.Type, payload)
		return err
	}
}

func (m *Module) runsStop(e *core.RequestEvent) error {
	runID := e.Request.PathValue("runID")
	run, err := m.options.Orchestrator.CancelRun(e.Request.Context(), runID)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "run not found")
	}
	return e.JSON(http.StatusOK, renderRunResource(run, nil))
}

type jobsRequest struct {
	Name              string         `json:"name"`
	Prompt            string         `json:"prompt"`
	ScheduleKind      string         `json:"schedule_kind"`
	ScheduleValue     string         `json:"schedule_value"`
	Timezone          string         `json:"timezone"`
	Enabled           *bool          `json:"enabled"`
	CWD               string         `json:"cwd"`
	DeliveryTarget    map[string]any `json:"delivery_target"`
	ToolsetOverrides  map[string]any `json:"toolset_overrides"`
	ProviderOverrides map[string]any `json:"provider_overrides"`
}

func (m *Module) jobsList(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	items, err := m.options.JobService.ListJobs(e.Request.Context(), m.options.Profile.Slug, 100)
	if err != nil {
		return m.writeError(e, http.StatusInternalServerError, err.Error())
	}
	data := make([]map[string]any, 0, len(items))
	for _, item := range items {
		data = append(data, renderJob(item, nil))
	}
	return e.JSON(http.StatusOK, map[string]any{"data": data})
}

func (m *Module) jobsCreate(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	var req jobsRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return m.writeError(e, http.StatusBadRequest, "invalid request body")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	job, err := m.options.JobService.CreateJob(e.Request.Context(), jobs.CreateJobInput{
		ProfileID:         m.options.Profile.Slug,
		Name:              req.Name,
		Prompt:            req.Prompt,
		ScheduleKind:      fallbackString(req.ScheduleKind, "interval"),
		ScheduleValue:     fallbackString(req.ScheduleValue, "1h"),
		Timezone:          req.Timezone,
		Enabled:           enabled,
		CWD:               req.CWD,
		DeliveryTarget:    req.DeliveryTarget,
		ToolsetOverrides:  req.ToolsetOverrides,
		ProviderOverrides: req.ProviderOverrides,
	})
	if err != nil {
		return m.writeError(e, http.StatusBadRequest, err.Error())
	}
	return e.JSON(http.StatusOK, renderJob(job, nil))
}

func (m *Module) jobsGet(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	jobID := e.Request.PathValue("jobID")
	job, err := m.options.JobService.GetJob(e.Request.Context(), jobID)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "job not found")
	}
	history, _ := m.options.JobService.ListRuns(e.Request.Context(), jobID, 25)
	return e.JSON(http.StatusOK, renderJob(job, history))
}

func (m *Module) jobsPatch(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	var req jobsRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return m.writeError(e, http.StatusBadRequest, "invalid request body")
	}
	job, err := m.options.JobService.UpdateJob(e.Request.Context(), e.Request.PathValue("jobID"), jobs.UpdateJobInput{
		Name:              req.Name,
		Prompt:            req.Prompt,
		ScheduleKind:      req.ScheduleKind,
		ScheduleValue:     req.ScheduleValue,
		Timezone:          req.Timezone,
		Enabled:           req.Enabled,
		CWD:               req.CWD,
		DeliveryTarget:    req.DeliveryTarget,
		ToolsetOverrides:  req.ToolsetOverrides,
		ProviderOverrides: req.ProviderOverrides,
	})
	if err != nil {
		return m.writeError(e, http.StatusBadRequest, err.Error())
	}
	return e.JSON(http.StatusOK, renderJob(job, nil))
}

func (m *Module) jobsPause(e *core.RequestEvent) error {
	return m.jobStateAction(e, m.options.JobService.PauseJob)
}

func (m *Module) jobsResume(e *core.RequestEvent) error {
	return m.jobStateAction(e, m.options.JobService.ResumeJob)
}

func (m *Module) jobsRun(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	jobID := e.Request.PathValue("jobID")
	job, err := m.options.JobService.GetJob(e.Request.Context(), jobID)
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "job not found")
	}

	now := time.Now().UTC()
	if m.options.PromptBuilder == nil || m.options.Orchestrator == nil {
		run, recordErr := m.options.JobService.RecordRun(e.Request.Context(), jobs.RecordRunInput{
			ProfileID:    m.options.Profile.Slug,
			JobID:        job.ID,
			Status:       jobs.JobStatusQueued,
			ScheduledFor: now,
		})
		if recordErr != nil {
			return m.writeError(e, http.StatusInternalServerError, recordErr.Error())
		}
		return e.JSON(http.StatusOK, map[string]any{"job_id": job.ID, "status": run.Status, "run_id": run.RunID})
	}

	executor := jobs.RuntimeExecutor{
		Profile:       m.options.Profile,
		Config:        m.options.Config,
		Sessions:      m.options.SessionService,
		PromptBuilder: m.options.PromptBuilder,
		Orchestrator:  m.options.Orchestrator,
		ToolRegistry:  m.options.ToolRegistry,
	}
	result, execErr := executor.ExecuteJob(e.Request.Context(), job)
	status := result.Status
	if status == "" {
		status = jobs.JobStatusFailed
	}
	jobRun, recordErr := m.options.JobService.RecordRun(e.Request.Context(), jobs.RecordRunInput{
		ProfileID:     m.options.Profile.Slug,
		JobID:         job.ID,
		RunID:         result.RunID,
		Status:        status,
		ScheduledFor:  now,
		StartedAt:     now,
		EndedAt:       time.Now().UTC(),
		OutputExcerpt: result.OutputText,
		ErrorMessage:  errorString(execErr),
	})
	if recordErr != nil {
		return m.writeError(e, http.StatusInternalServerError, recordErr.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{
		"job_id":     job.ID,
		"session_id": result.SessionID,
		"run_id":     result.RunID,
		"status":     jobRun.Status,
		"output":     result.OutputText,
	})
}

func (m *Module) jobsDelete(e *core.RequestEvent) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	jobID := e.Request.PathValue("jobID")
	if err := m.options.JobService.DeleteJob(e.Request.Context(), jobID); err != nil {
		return m.writeError(e, http.StatusNotFound, "job not found")
	}
	return e.JSON(http.StatusOK, map[string]any{"id": jobID, "deleted": true})
}

func (m *Module) modelsList(e *core.RequestEvent) error {
	data := make([]map[string]any, 0, len(m.options.ProviderCatalog.Entries))
	for _, entry := range m.options.ProviderCatalog.Entries {
		data = append(data, map[string]any{
			"id":           entry.ModelID,
			"object":       "model",
			"owned_by":     entry.ProviderID,
			"capabilities": entry.Capabilities,
			"lifecycle":    entry.LifecycleStatus,
		})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (m *Module) capabilitiesGet(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, map[string]any{
		"object":         "capabilities",
		"schema_version": schemaVersion,
		"features": []string{
			"chat.completions",
			"responses",
			"responses.get",
			"responses.delete",
			"runs",
			"run.events",
			"run.stop",
			"jobs",
			"models.list",
		},
	})
}

func (m *Module) createResponseSession(ctx context.Context, req responsesRequest) (sessions.Session, sessions.Message, providers.NormalizedRequest, error) {
	requestMessages := make([]providers.RequestMessage, 0, len(req.Input))
	for _, item := range req.Input {
		text := flattenInputText(item.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		requestMessages = append(requestMessages, providers.RequestMessage{
			Role:    fallbackString(item.Role, "user"),
			Content: text,
		})
	}
	if len(requestMessages) == 0 {
		return sessions.Session{}, sessions.Message{}, providers.NormalizedRequest{}, fmt.Errorf("input must include at least one text item")
	}

	return m.createSessionFromMessages(ctx, "api.responses", req.Model, requestMessages, req.Instructions)
}

func (m *Module) createSessionFromMessages(ctx context.Context, source, model string, requestMessages []providers.RequestMessage, instructions string) (sessions.Session, sessions.Message, providers.NormalizedRequest, error) {
	title := "API response"
	if len(requestMessages) > 0 {
		title = summarizeTitle(requestMessages[0].Content)
	}

	session, err := m.options.SessionService.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID: m.options.Profile.Slug,
		Source:    source,
		Title:     title,
		Status:    "active",
		ModelSnapshot: map[string]any{
			"model": model,
		},
	})
	if err != nil {
		return sessions.Session{}, sessions.Message{}, providers.NormalizedRequest{}, fmt.Errorf("create response session: %w", err)
	}

	first := requestMessages[0]
	userMessage, err := m.options.SessionService.CreateMessage(ctx, sessions.CreateMessageInput{
		ProfileID:   m.options.Profile.Slug,
		SessionID:   session.ID,
		Role:        first.Role,
		Content:     sessions.MessageContent{{Type: "input_text", Text: first.Content}},
		VisibleText: first.Content,
	})
	if err != nil {
		return sessions.Session{}, sessions.Message{}, providers.NormalizedRequest{}, fmt.Errorf("create input message: %w", err)
	}

	for _, message := range requestMessages[1:] {
		if _, err := m.options.SessionService.CreateMessage(ctx, sessions.CreateMessageInput{
			ProfileID:   m.options.Profile.Slug,
			SessionID:   session.ID,
			Role:        message.Role,
			Content:     sessions.MessageContent{{Type: "input_text", Text: message.Content}},
			VisibleText: message.Content,
		}); err != nil {
			return sessions.Session{}, sessions.Message{}, providers.NormalizedRequest{}, fmt.Errorf("create input message: %w", err)
		}
	}

	return session, userMessage, providers.NormalizedRequest{
		System:   instructions,
		Messages: requestMessages,
	}, nil
}

func (m *Module) loadStoredResponse(ctx context.Context, responseID string) (sessions.Run, sessions.Message, error) {
	run, err := m.options.SessionService.GetRun(ctx, responseID)
	if err != nil {
		return sessions.Run{}, sessions.Message{}, fmt.Errorf("response not found")
	}
	messages, err := m.options.SessionService.ListMessagesByRun(ctx, responseID)
	if err != nil {
		return sessions.Run{}, sessions.Message{}, fmt.Errorf("response not found")
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			return run, message, nil
		}
	}
	return sessions.Run{}, sessions.Message{}, fmt.Errorf("response not found")
}

func renderStoredResponse(responseID, model string, message sessions.Message) map[string]any {
	return map[string]any{
		"id":          responseID,
		"object":      "response",
		"created_at":  message.CreatedAt.Unix(),
		"model":       model,
		"output_text": message.VisibleText,
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": message.VisibleText,
			}},
		}},
		"usage": message.Usage,
	}
}

func renderRunResource(run sessions.Run, assistant *sessions.Message) map[string]any {
	data := map[string]any{
		"id":             run.ID,
		"object":         "run",
		"session_id":     run.SessionID,
		"status":         run.Status,
		"trigger_source": run.TriggerSource,
		"started_at":     run.StartedAt,
		"ended_at":       run.EndedAt,
		"error_code":     run.ErrorCode,
		"error_message":  run.ErrorMessage,
		"request":        run.Request,
		"resolution":     run.ProviderResolution,
	}
	if assistant != nil {
		data["output_text"] = assistant.VisibleText
		data["usage"] = assistant.Usage
	}
	return data
}

func renderJob(job jobs.Job, history []jobs.JobRun) map[string]any {
	data := map[string]any{
		"id":                 job.ID,
		"name":               job.Name,
		"prompt":             job.Prompt,
		"schedule_kind":      job.ScheduleKind,
		"schedule_value":     job.ScheduleValue,
		"timezone":           job.Timezone,
		"enabled":            job.Enabled,
		"cwd":                job.CWD,
		"next_run_at":        job.NextRunAt,
		"last_run_at":        job.LastRunAt,
		"delivery_target":    job.DeliveryTarget,
		"toolset_overrides":  job.ToolsetOverrides,
		"provider_overrides": job.ProviderOverrides,
	}
	if history != nil {
		data["history"] = history
	}
	return data
}

func (m *Module) writeError(e *core.RequestEvent, status int, message string) error {
	return e.JSON(status, map[string]any{
		"error": map[string]any{
			"message": message,
		},
	})
}

func (m *Module) jobStateAction(e *core.RequestEvent, action func(context.Context, string) (jobs.Job, error)) error {
	if m.options.JobService == nil {
		return m.writeError(e, http.StatusServiceUnavailable, "job service unavailable")
	}
	job, err := action(e.Request.Context(), e.Request.PathValue("jobID"))
	if err != nil {
		return m.writeError(e, http.StatusNotFound, "job not found")
	}
	return e.JSON(http.StatusOK, renderJob(job, nil))
}

func writeTextStream(e *core.RequestEvent, eventName string, chunks []string, build func(chunk string, index int, final bool) map[string]any) error {
	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-cache")
	e.Response.Header().Set("Connection", "keep-alive")
	for index, chunk := range chunks {
		payload, _ := json.Marshal(build(chunk, index, false))
		if _, err := fmt.Fprintf(e.Response, "event: %s\ndata: %s\n\n", eventName, payload); err != nil {
			return err
		}
	}
	finalPayload, _ := json.Marshal(build("", len(chunks), true))
	if _, err := fmt.Fprintf(e.Response, "event: completed\ndata: %s\n\ndata: [DONE]\n\n", finalPayload); err != nil {
		return err
	}
	return nil
}

func chunkText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	words := strings.Fields(text)
	chunks := make([]string, 0, len(words))
	for _, word := range words {
		chunks = append(chunks, word+" ")
	}
	if len(chunks) > 0 {
		chunks[len(chunks)-1] = strings.TrimSpace(chunks[len(chunks)-1])
	}
	return chunks
}

func flattenInputText(items []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, item := range items {
		if item.Type == "input_text" && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func responseID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func summarizeTitle(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 48 {
		return text
	}
	return strings.TrimSpace(text[:48]) + "..."
}

func resolvedModel(run sessions.Run) string {
	if selected, ok := run.ProviderResolution["selected"].(map[string]any); ok {
		if model, ok := selected["model_id"].(string); ok && model != "" {
			return model
		}
	}
	return ""
}

func fallbackString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
