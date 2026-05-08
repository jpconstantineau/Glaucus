package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
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
			"models.list",
		},
	})
}

func (m *Module) createResponseSession(ctx context.Context, req responsesRequest) (sessions.Session, sessions.Message, providers.NormalizedRequest, error) {
	requestMessages := make([]providers.RequestMessage, 0, len(req.Input))
	title := "API response"
	for _, item := range req.Input {
		text := flattenInputText(item.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		requestMessages = append(requestMessages, providers.RequestMessage{
			Role:    fallbackString(item.Role, "user"),
			Content: text,
		})
		if title == "API response" && text != "" {
			title = summarizeTitle(text)
		}
	}
	if len(requestMessages) == 0 {
		return sessions.Session{}, sessions.Message{}, providers.NormalizedRequest{}, fmt.Errorf("input must include at least one text item")
	}

	session, err := m.options.SessionService.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID: m.options.Profile.Slug,
		Source:    "api.responses",
		Title:     title,
		Status:    "active",
		ModelSnapshot: map[string]any{
			"model": req.Model,
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
		System:   req.Instructions,
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

func (m *Module) writeError(e *core.RequestEvent, status int, message string) error {
	return e.JSON(status, map[string]any{
		"error": map[string]any{
			"message": message,
		},
	})
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
