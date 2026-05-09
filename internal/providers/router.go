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
	AuxiliarySlot        string
	CredentialIndex      int
}

type FallbackTarget struct {
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	Dialect         string `json:"dialect"`
	CredentialIndex int    `json:"credential_index,omitempty"`
	RetryOnly       bool   `json:"retry_only,omitempty"`
}

type Resolution struct {
	ProviderID       string                     `json:"provider_id"`
	ModelID          string                     `json:"model_id"`
	BaseURL          string                     `json:"base_url"`
	Dialect          string                     `json:"dialect"`
	CredentialSource string                     `json:"credential_source,omitempty"`
	CredentialPool   string                     `json:"credential_pool,omitempty"`
	CredentialIndex  int                        `json:"credential_index,omitempty"`
	Headers          map[string]string          `json:"headers,omitempty"`
	RoutingPolicy    config.RoutingPolicyConfig `json:"routing_policy"`
	FallbackPlan     []FallbackTarget           `json:"fallback_plan,omitempty"`
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
	input = r.applyAuxiliaryDefaults(input)
	entry, err := r.selectEntry(input)
	if err != nil {
		return Resolution{}, err
	}

	headers := cloneHeaders(entry.DefaultHeaders)
	credentialSource := ""
	credentialPool := ""
	credentialIndex := input.CredentialIndex
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
		} else if providerCfg.CredentialPool != "" {
			credentialPool = providerCfg.CredentialPool
			pool := r.config.CredentialPools[providerCfg.CredentialPool]
			if credentialIndex < 0 || credentialIndex >= len(pool.Credentials) {
				credentialIndex = 0
			}
			if len(pool.Credentials) > 0 {
				source := pool.Credentials[credentialIndex]
				credential := strings.TrimSpace(os.Getenv(source.Env))
				if credential != "" {
					applyAuthHeader(headers, entry.Dialect, credential)
					credentialSource = fmt.Sprintf("credential_pool:%s[%d]", providerCfg.CredentialPool, credentialIndex)
				}
			}
		}
	}

	fallbackPlan := make([]FallbackTarget, 0)
	if credentialPool != "" {
		pool := r.config.CredentialPools[credentialPool]
		for idx := credentialIndex + 1; idx < len(pool.Credentials); idx++ {
			fallbackPlan = append(fallbackPlan, FallbackTarget{
				ProviderID:      entry.ProviderID,
				ModelID:         entry.ModelID,
				Dialect:         entry.Dialect,
				CredentialIndex: idx,
				RetryOnly:       true,
			})
		}
	}
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
		CredentialPool:   credentialPool,
		CredentialIndex:  credentialIndex,
		Headers:          headers,
		RoutingPolicy:    r.config.Routing,
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
		ProviderID:      resolution.ProviderID,
		ModelID:         resolution.ModelID,
		Dialect:         resolution.Dialect,
		CredentialIndex: resolution.CredentialIndex,
	}}
	candidates = append(candidates, resolution.FallbackPlan...)

	attempts := make([]AttemptRecord, 0, len(candidates))
	lastRetryable := true
	for idx, candidate := range candidates {
		if candidate.RetryOnly && !lastRetryable {
			continue
		}
		started := time.Now().UTC()
		candidateResolution, err := r.Resolve(ResolutionInput{
			ProviderID:           candidate.ProviderID,
			ModelID:              candidate.ModelID,
			RequiredCapabilities: input.RequiredCapabilities,
			AuxiliarySlot:        input.AuxiliarySlot,
			CredentialIndex:      candidate.CredentialIndex,
		})
		if err != nil {
			lastRetryable = false
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
		lastRetryable = isRetryable(execErr)
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
	if providerID == "" && modelID == "" {
		providerID = strings.TrimSpace(r.config.Model.DefaultProvider)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(r.config.Model.DefaultModel)
	}
	requiredCapabilities := append([]string{}, input.RequiredCapabilities...)
	requiredCapabilities = append(requiredCapabilities, r.config.Routing.RequiredCapabilities...)
	candidates := make([]CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if providerID != "" && entry.ProviderID != providerID {
			continue
		}
		if modelID != "" && entry.ModelID != modelID {
			continue
		}
		if containsString(r.config.Routing.DeniedProviders, entry.ProviderID) {
			continue
		}
		if !supportsCapabilities(entry.Capabilities, requiredCapabilities) {
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 && modelID != "" {
		for _, entry := range entries {
			if providerID != "" && entry.ProviderID != providerID {
				continue
			}
			if containsString(r.config.Routing.DeniedProviders, entry.ProviderID) {
				continue
			}
			if !supportsCapabilities(entry.Capabilities, requiredCapabilities) {
				continue
			}
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		for _, entry := range entries {
			if containsString(r.config.Routing.DeniedProviders, entry.ProviderID) {
				continue
			}
			if !supportsCapabilities(entry.Capabilities, requiredCapabilities) {
				continue
			}
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return CatalogEntry{}, fmt.Errorf("no provider/model found for provider=%q model=%q", providerID, modelID)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return r.compareEntries(candidates[i], candidates[j], providerID, modelID)
	})
	return candidates[0], nil
}

func (r *Router) orderedFallbacks(primary CatalogEntry, required []string) []CatalogEntry {
	candidates := make([]CatalogEntry, 0, len(r.catalog.Entries))
	for _, entry := range r.catalog.Entries {
		if entry.ProviderID == primary.ProviderID && entry.ModelID == primary.ModelID {
			continue
		}
		if containsString(r.config.Routing.DeniedProviders, entry.ProviderID) {
			continue
		}
		if !supportsCapabilities(entry.Capabilities, required) {
			continue
		}
		candidates = append(candidates, entry)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return r.compareEntries(candidates[i], candidates[j], primary.ProviderID, primary.ModelID)
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
		return nil, &ProviderError{Message: fmt.Sprintf("execute provider request: %v", err), Retryable: true}
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}
	if res.StatusCode >= 400 {
		return nil, &ProviderError{
			StatusCode: res.StatusCode,
			Message:    fmt.Sprintf("provider returned %d: %s", res.StatusCode, strings.TrimSpace(string(payload))),
			Retryable:  res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500,
		}
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

type ProviderError struct {
	StatusCode int
	Message    string
	Retryable  bool
}

func (e *ProviderError) Error() string {
	return e.Message
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

func (r *Router) applyAuxiliaryDefaults(input ResolutionInput) ResolutionInput {
	if strings.TrimSpace(input.AuxiliarySlot) == "" {
		return input
	}
	slotCfg, ok := r.config.AuxiliaryRouting[input.AuxiliarySlot]
	if !ok {
		return input
	}
	if input.ProviderID == "" && strings.TrimSpace(slotCfg.ProviderID) != "" {
		input.ProviderID = slotCfg.ProviderID
	}
	if input.ModelID == "" && strings.TrimSpace(slotCfg.ModelID) != "" {
		input.ModelID = slotCfg.ModelID
	}
	return input
}

func (r *Router) compareEntries(left, right CatalogEntry, providerID, modelID string) bool {
	leftScore := r.entryScore(left, providerID, modelID)
	rightScore := r.entryScore(right, providerID, modelID)
	if leftScore != rightScore {
		return leftScore < rightScore
	}
	if left.ProviderID == right.ProviderID {
		return left.ModelID < right.ModelID
	}
	return left.ProviderID < right.ProviderID
}

func (r *Router) entryScore(entry CatalogEntry, providerID, modelID string) int {
	score := 100
	if providerID != "" && entry.ProviderID == providerID {
		score -= 50
	}
	if modelID != "" && entry.ModelID == modelID {
		score -= 30
	}
	if idx := indexOf(r.config.Routing.PreferredProviders, entry.ProviderID); idx >= 0 {
		score -= 20 - min(idx, 19)
	}
	if idx := indexOf(r.config.Routing.FallbackOrder, entry.ProviderID); idx >= 0 {
		score -= 10 - min(idx, 9)
	}
	return score
}

func containsString(values []string, target string) bool {
	return indexOf(values, target) >= 0
}

func indexOf(values []string, target string) int {
	for idx, value := range values {
		if value == target {
			return idx
		}
	}
	return -1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
