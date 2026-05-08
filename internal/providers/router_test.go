package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
)

func TestRouterResolveAndFallbackPlan(t *testing.T) {
	t.Setenv("GLAUCUS_OPENAI_KEY", "top-secret")

	catalog := Catalog{Entries: []CatalogEntry{
		{ProviderID: "openrouter", ModelID: "openai/gpt-4.1-mini", Dialect: "openai-chat", BaseURL: "https://openrouter.ai/api/v1", Capabilities: []string{"chat"}},
		{ProviderID: "selfhosted-openai", ModelID: "local-general", Dialect: "openai-chat", BaseURL: "http://localhost:4000/v1", Capabilities: []string{"chat"}},
	}}
	cfg := config.Default()
	cfg.Model.DefaultProvider = "openrouter"
	cfg.Model.DefaultModel = "openai/gpt-4.1-mini"
	cfg.Providers["openrouter"] = config.ProviderConfig{
		Auth: config.ProviderAuth{Env: "GLAUCUS_OPENAI_KEY"},
	}

	router := NewRouter(catalog, cfg)
	resolution, err := router.Resolve(ResolutionInput{RequiredCapabilities: []string{"chat"}})
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	if resolution.ProviderID != "openrouter" || resolution.ModelID != "openai/gpt-4.1-mini" {
		t.Fatalf("unexpected resolution: %#v", resolution)
	}
	if resolution.Headers["Authorization"] != "Bearer top-secret" {
		t.Fatalf("expected auth header, got %#v", resolution.Headers)
	}
	if len(resolution.FallbackPlan) != 1 || resolution.FallbackPlan[0].ProviderID != "selfhosted-openai" {
		t.Fatalf("unexpected fallback plan: %#v", resolution.FallbackPlan)
	}
}

func TestRouterResolveUsesCredentialPoolsAndRoutingPolicy(t *testing.T) {
	t.Setenv("GLAUCUS_KEY_A", "pool-a")
	t.Setenv("GLAUCUS_KEY_B", "pool-b")

	catalog := Catalog{Entries: []CatalogEntry{
		{ProviderID: "provider-a", ModelID: "model-a", Dialect: "openai-chat", BaseURL: "https://provider-a.example/v1", Capabilities: []string{"chat"}},
		{ProviderID: "provider-b", ModelID: "model-b", Dialect: "openai-chat", BaseURL: "https://provider-b.example/v1", Capabilities: []string{"chat"}},
	}}
	cfg := config.Default()
	cfg.Model.DefaultProvider = "provider-b"
	cfg.Model.DefaultModel = "model-b"
	cfg.Providers["provider-a"] = config.ProviderConfig{CredentialPool: "primary-pool"}
	cfg.CredentialPools["primary-pool"] = config.CredentialPoolConfig{
		Credentials: []config.CredentialSourceConfig{
			{Env: "GLAUCUS_KEY_A"},
			{Env: "GLAUCUS_KEY_B"},
		},
		Rotation:           "retry",
		InheritToSubagents: true,
	}
	cfg.Routing.PreferredProviders = []string{"provider-a"}
	cfg.Routing.DeniedProviders = []string{"provider-b"}

	router := NewRouter(catalog, cfg)
	resolution, err := router.Resolve(ResolutionInput{RequiredCapabilities: []string{"chat"}})
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}

	if resolution.ProviderID != "provider-a" || resolution.CredentialPool != "primary-pool" || resolution.CredentialIndex != 0 {
		t.Fatalf("unexpected pooled resolution: %#v", resolution)
	}
	if resolution.CredentialSource != "credential_pool:primary-pool[0]" {
		t.Fatalf("unexpected credential source: %s", resolution.CredentialSource)
	}
	if resolution.Headers["Authorization"] != "Bearer pool-a" {
		t.Fatalf("expected pooled auth header, got %#v", resolution.Headers)
	}
	if len(resolution.FallbackPlan) != 1 || !resolution.FallbackPlan[0].RetryOnly || resolution.FallbackPlan[0].CredentialIndex != 1 {
		t.Fatalf("expected retry-only credential rotation fallback, got %#v", resolution.FallbackPlan)
	}
}

func TestOpenAIChatAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["model"] != "model-chat" {
			t.Fatalf("unexpected model payload: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "model-chat",
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "chat result"},
			}},
			"usage": map[string]any{"total_tokens": 12},
		})
	}))
	defer server.Close()

	router := NewRouter(Catalog{}, config.Default())
	resolution := Resolution{ProviderID: "p1", ModelID: "model-chat", BaseURL: server.URL, Dialect: "openai-chat"}
	response, err := router.Execute(context.Background(), resolution, NormalizedRequest{
		Messages: []RequestMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("execute chat request: %v", err)
	}
	if response.OutputText != "chat result" {
		t.Fatalf("unexpected chat output: %#v", response)
	}
}

func TestOpenAIResponsesAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":       "model-responses",
			"output_text": "responses result",
			"usage":       map[string]any{"total_tokens": 8},
		})
	}))
	defer server.Close()

	router := NewRouter(Catalog{}, config.Default())
	resolution := Resolution{ProviderID: "p2", ModelID: "model-responses", BaseURL: server.URL, Dialect: "openai-responses"}
	response, err := router.Execute(context.Background(), resolution, NormalizedRequest{
		System:   "You are helpful.",
		Messages: []RequestMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("execute responses request: %v", err)
	}
	if response.OutputText != "responses result" {
		t.Fatalf("unexpected responses output: %#v", response)
	}
}

func TestAnthropicMessagesAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-secret" {
			t.Fatalf("expected anthropic auth header, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":       "claude-test",
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "anthropic result",
			}},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Providers["anthropic"] = config.ProviderConfig{Auth: config.ProviderAuth{Env: "GLAUCUS_ANTHROPIC_KEY"}}
	t.Setenv("GLAUCUS_ANTHROPIC_KEY", "anthropic-secret")

	router := NewRouter(Catalog{}, cfg)
	resolution := Resolution{
		ProviderID: "anthropic",
		ModelID:    "claude-test",
		BaseURL:    server.URL,
		Dialect:    "anthropic-messages",
		Headers:    map[string]string{},
	}
	applyAuthHeader(resolution.Headers, resolution.Dialect, "anthropic-secret")
	response, err := router.Execute(context.Background(), resolution, NormalizedRequest{
		System:   "Be concise.",
		Messages: []RequestMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("execute anthropic request: %v", err)
	}
	if response.OutputText != "anthropic result" {
		t.Fatalf("unexpected anthropic output: %#v", response)
	}
}
