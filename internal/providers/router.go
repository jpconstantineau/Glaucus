package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
)

type RequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type NormalizedRequest struct {
	System       string
	Messages     []RequestMessage
	MaxTokens    int
	Temperature  float64
	ToolChoice   string
	RequiredCaps []string
}

type NormalizedResponse struct {
	Model       string
	OutputText  string
	StopReason  string
	Usage       map[string]any
	RawResponse map[string]any
}

type ResolutionInput struct {
	ProviderID           string
	ModelID              string
	RequiredCapabilities []string
}

type FallbackTarget struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Dialect    string `json:"dialect"`
}

type Resolution struct {
	ProviderID       string            `json:"provider_id"`
	ModelID          string            `json:"model_id"`
	BaseURL          string            `json:"base_url"`
	Dialect          string            `json:"dialect"`
	CredentialSource string            `json:"credential_source,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	FallbackPlan     []FallbackTarget  `json:"fallback_plan,omitempty"`
}

type AttemptRecord struct {
	Index      int       `json:"index"`
	ProviderID string    `json:"provider_id"`
	ModelID    string    `json:"model_id"`
	Dialect    string    `json:"dialect"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

type Router struct {
	catalog    Catalog
	config     config.Config
	httpClient *http.Client
}

func NewRouter(catalog Catalog, cfg config.Config) *Router {
	return &Router{
		catalog: catalog,
		config:  cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (r *Router) Resolve(input ResolutionInput) (Resolution, error) {
	entry, err := r.selectEntry(input)
	if err != nil {
		return Resolution{}, err
	}

	headers := cloneHeaders(entry.DefaultHeaders)
	credentialSource := ""
	if providerCfg, ok := r.config.Providers[entry.ProviderID]; ok {
		if providerCfg.BaseURL != "" {
			entry.BaseURL = providerCfg.BaseURL
		}
		if providerCfg.Dialect != "" {
			entry.Dialect = providerCfg.Dialect
		}
		for key, value := range providerCfg.Headers {
			headers[key] = value
		}
		if providerCfg.Auth.Env != "" {
			credential := strings.TrimSpace(os.Getenv(providerCfg.Auth.Env))
			if credential != "" {
				applyAuthHeader(headers, entry.Dialect, credential)
				credentialSource = "env:" + providerCfg.Auth.Env
			}
		}
	}

	fallbackPlan := make([]FallbackTarget, 0)
	for _, candidate := range r.orderedFallbacks(entry, input.RequiredCapabilities) {
		fallbackPlan = append(fallbackPlan, FallbackTarget{
			ProviderID: candidate.ProviderID,
			ModelID:    candidate.ModelID,
			Dialect:    candidate.Dialect,
		})
	}

	return Resolution{
		ProviderID:       entry.ProviderID,
		ModelID:          entry.ModelID,
		BaseURL:          entry.BaseURL,
		Dialect:          entry.Dialect,
		CredentialSource: credentialSource,
		Headers:          headers,
		FallbackPlan:     fallbackPlan,
	}, nil
}

func (r *Router) Execute(ctx context.Context, resolution Resolution, request NormalizedRequest) (NormalizedResponse, error) {
	switch resolution.Dialect {
	case "openai-chat":
		return r.executeOpenAIChat(ctx, resolution, request)
	case "openai-responses":
		return r.executeOpenAIResponses(ctx, resolution, request)
	case "anthropic-messages":
		return r.executeAnthropicMessages(ctx, resolution, request)
	default:
		return NormalizedResponse{}, fmt.Errorf("unsupported provider dialect %q", resolution.Dialect)
	}
}

func (r *Router) ExecuteWithFallback(ctx context.Context, input ResolutionInput, request NormalizedRequest) (NormalizedResponse, Resolution, []AttemptRecord, error) {
	resolution, err := r.Resolve(input)
	if err != nil {
		return NormalizedResponse{}, Resolution{}, nil, err
	}

	candidates := []FallbackTarget{{
		ProviderID: resolution.ProviderID,
		ModelID:    resolution.ModelID,
		Dialect:    resolution.Dialect,
	}}
	candidates = append(candidates, resolution.FallbackPlan...)

	attempts := make([]AttemptRecord, 0, len(candidates))
	for idx, candidate := range candidates {
		started := time.Now().UTC()
		candidateResolution, err := r.Resolve(ResolutionInput{
			ProviderID:           candidate.ProviderID,
			ModelID:              candidate.ModelID,
			RequiredCapabilities: input.RequiredCapabilities,
		})
		if err != nil {
			attempts = append(attempts, AttemptRecord{
				Index:      idx + 1,
				ProviderID: candidate.ProviderID,
				ModelID:    candidate.ModelID,
				Dialect:    candidate.Dialect,
				StartedAt:  started,
				EndedAt:    time.Now().UTC(),
				Error:      err.Error(),
			})
			continue
		}

		response, execErr := r.Execute(ctx, candidateResolution, request)
		attempt := AttemptRecord{
			Index:      idx + 1,
			ProviderID: candidateResolution.ProviderID,
			ModelID:    candidateResolution.ModelID,
			Dialect:    candidateResolution.Dialect,
			StartedAt:  started,
			EndedAt:    time.Now().UTC(),
			Success:    execErr == nil,
		}
		if execErr != nil {
			attempt.Error = execErr.Error()
			attempts = append(attempts, attempt)
			continue
		}
		attempts = append(attempts, attempt)
		return response, candidateResolution, attempts, nil
	}

	return NormalizedResponse{}, resolution, attempts, errors.New("all provider attempts failed")
}

func (r *Router) selectEntry(input ResolutionInput) (CatalogEntry, error) {
	entries := r.catalog.Entries
	if len(entries) == 0 {
		return CatalogEntry{}, errors.New("provider catalog is empty")
	}

	providerID := strings.TrimSpace(input.ProviderID)
	modelID := strings.TrimSpace(input.ModelID)
	if providerID == "" {
		providerID = strings.TrimSpace(r.config.Model.DefaultProvider)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(r.config.Model.DefaultModel)
	}

	var candidate *CatalogEntry
	for _, entry := range entries {
		if providerID != "" && entry.ProviderID != providerID {
			continue
		}
		if modelID != "" && entry.ModelID != modelID {
			continue
		}
		if !supportsCapabilities(entry.Capabilities, input.RequiredCapabilities) {
			continue
		}
		entryCopy := entry
		candidate = &entryCopy
		break
	}

	if candidate == nil {
		for _, entry := range entries {
			if providerID != "" && entry.ProviderID != providerID {
				continue
			}
			if !supportsCapabilities(entry.Capabilities, input.RequiredCapabilities) {
				continue
			}
			entryCopy := entry
			candidate = &entryCopy
			break
		}
	}

	if candidate == nil {
		return CatalogEntry{}, fmt.Errorf("no provider/model found for provider=%q model=%q", providerID, modelID)
	}

	return *candidate, nil
}

func (r *Router) orderedFallbacks(primary CatalogEntry, required []string) []CatalogEntry {
	candidates := make([]CatalogEntry, 0, len(r.catalog.Entries))
	for _, entry := range r.catalog.Entries {
		if entry.ProviderID == primary.ProviderID && entry.ModelID == primary.ModelID {
			continue
		}
		if !supportsCapabilities(entry.Capabilities, required) {
			continue
		}
		candidates = append(candidates, entry)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].ProviderID == primary.ProviderID && candidates[j].ProviderID != primary.ProviderID {
			return true
		}
		if candidates[i].ProviderID != primary.ProviderID && candidates[j].ProviderID == primary.ProviderID {
			return false
		}
		if candidates[i].ProviderID == candidates[j].ProviderID {
			return candidates[i].ModelID < candidates[j].ModelID
		}
		return candidates[i].ProviderID < candidates[j].ProviderID
	})

	return candidates
}

func (r *Router) executeOpenAIChat(ctx context.Context, resolution Resolution, request NormalizedRequest) (NormalizedResponse, error) {
	body := map[string]any{
		"model":       resolution.ModelID,
		"messages":    request.Messages,
		"temperature": request.Temperature,
	}
	if request.MaxTokens > 0 {
		body["max_tokens"] = request.MaxTokens
	}
	if request.ToolChoice != "" {
		body["tool_choice"] = request.ToolChoice
	}

	var response struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	raw, err := r.doJSON(ctx, http.MethodPost, resolution, "/chat/completions", body, &response)
	if err != nil {
		return NormalizedResponse{}, err
	}
	if len(response.Choices) == 0 {
		return NormalizedResponse{}, errors.New("provider returned no chat choices")
	}

	return NormalizedResponse{
		Model:       fallbackString(response.Model, resolution.ModelID),
		OutputText:  response.Choices[0].Message.Content,
		StopReason:  response.Choices[0].FinishReason,
		Usage:       response.Usage,
		RawResponse: raw,
	}, nil
}

func (r *Router) executeOpenAIResponses(ctx context.Context, resolution Resolution, request NormalizedRequest) (NormalizedResponse, error) {
	input := make([]map[string]any, 0, len(request.Messages))
	for _, message := range request.Messages {
		input = append(input, map[string]any{
			"role": message.Role,
			"content": []map[string]any{{
				"type": "input_text",
				"text": message.Content,
			}},
		})
	}

	body := map[string]any{
		"model": resolution.ModelID,
		"input": input,
	}
	if request.System != "" {
		body["instructions"] = request.System
	}
	if request.MaxTokens > 0 {
		body["max_output_tokens"] = request.MaxTokens
	}

	var response struct {
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage map[string]any `json:"usage"`
	}
	raw, err := r.doJSON(ctx, http.MethodPost, resolution, "/responses", body, &response)
	if err != nil {
		return NormalizedResponse{}, err
	}

	outputText := strings.TrimSpace(response.OutputText)
	if outputText == "" {
		for _, item := range response.Output {
			for _, content := range item.Content {
				if content.Text != "" {
					outputText = content.Text
					break
				}
			}
			if outputText != "" {
				break
			}
		}
	}
	if outputText == "" {
		return NormalizedResponse{}, errors.New("provider returned no responses output text")
	}

	return NormalizedResponse{
		Model:       fallbackString(response.Model, resolution.ModelID),
		OutputText:  outputText,
		Usage:       response.Usage,
		RawResponse: raw,
	}, nil
}

func (r *Router) executeAnthropicMessages(ctx context.Context, resolution Resolution, request NormalizedRequest) (NormalizedResponse, error) {
	messages := make([]map[string]any, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" {
			continue
		}
		messages = append(messages, map[string]any{
			"role": message.Role,
			"content": []map[string]any{{
				"type": "text",
				"text": message.Content,
			}},
		})
	}

	body := map[string]any{
		"model":      resolution.ModelID,
		"messages":   messages,
		"max_tokens": max(256, request.MaxTokens),
	}
	if request.System != "" {
		body["system"] = request.System
	}

	var response struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage map[string]any `json:"usage"`
	}
	raw, err := r.doJSON(ctx, http.MethodPost, resolution, "/messages", body, &response)
	if err != nil {
		return NormalizedResponse{}, err
	}
	if len(response.Content) == 0 {
		return NormalizedResponse{}, errors.New("provider returned no anthropic content")
	}

	return NormalizedResponse{
		Model:       fallbackString(response.Model, resolution.ModelID),
		OutputText:  response.Content[0].Text,
		StopReason:  response.StopReason,
		Usage:       response.Usage,
		RawResponse: raw,
	}, nil
}

func (r *Router) doJSON(ctx context.Context, method string, resolution Resolution, path string, body any, target any) (map[string]any, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal provider request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(resolution.BaseURL, "/")+path, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("build provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range resolution.Headers {
		req.Header.Set(key, value)
	}

	res, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute provider request: %w", err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("provider returned %d: %s", res.StatusCode, strings.TrimSpace(string(payload)))
	}

	if err := json.Unmarshal(payload, target); err != nil {
		return nil, fmt.Errorf("decode provider response: %w", err)
	}

	raw := map[string]any{}
	_ = json.Unmarshal(payload, &raw)
	return raw, nil
}

func supportsCapabilities(available []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	availableSet := map[string]struct{}{}
	for _, item := range available {
		availableSet[item] = struct{}{}
	}
	for _, cap := range required {
		if _, ok := availableSet[cap]; !ok {
			return false
		}
	}
	return true
}

func applyAuthHeader(headers map[string]string, dialect string, credential string) {
	switch dialect {
	case "anthropic-messages":
		headers["x-api-key"] = credential
		if headers["anthropic-version"] == "" {
			headers["anthropic-version"] = "2023-06-01"
		}
	default:
		headers["Authorization"] = "Bearer " + credential
	}
}

func fallbackString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}
