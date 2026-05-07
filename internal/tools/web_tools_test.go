package tools

import (
	"context"
	"errors"
	"testing"
)

type stubWebBackend struct{}

func (stubWebBackend) Search(context.Context, string) ([]SearchResult, error) {
	return []SearchResult{{Title: "Example", URL: "https://example.com", Snippet: "Snippet"}}, nil
}

func (stubWebBackend) Extract(context.Context, string) (ExtractResult, error) {
	return ExtractResult{URL: "https://example.com", Title: "Example", Text: "Example body"}, nil
}

type stubBrowserBackend struct {
	err error
}

func (b stubBrowserBackend) Name() string { return "stub-browser" }
func (b stubBrowserBackend) Healthy(context.Context) error {
	return b.err
}
func (b stubBrowserBackend) Navigate(context.Context, string) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}
func (b stubBrowserBackend) Snapshot(context.Context, string) (map[string]any, error) {
	return map[string]any{"nodes": 1}, nil
}

func TestWebToolsExecuteAgainstConfiguredBackend(t *testing.T) {
	search := WebSearchTool{backend: stubWebBackend{}}
	result := search.Execute(context.Background(), ToolRequest{
		Arguments: map[string]any{"query": "example"},
	})
	if result.Status != StatusSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}

	extract := WebExtractTool{backend: stubWebBackend{}}
	extractResult := extract.Execute(context.Background(), ToolRequest{
		Arguments: map[string]any{"url": "https://example.com"},
	})
	if extractResult.Status != StatusSuccess {
		t.Fatalf("expected extract success, got %s", extractResult.Status)
	}
}

func TestBrowserToolsRespectBackendHealth(t *testing.T) {
	navigate := BrowserNavigateTool{backend: stubBrowserBackend{err: errors.New("offline")}}
	availability := navigate.CheckAvailability(context.Background(), AvailabilityRequest{})
	if availability.Available {
		t.Fatal("expected unhealthy browser backend to be unavailable")
	}
}
