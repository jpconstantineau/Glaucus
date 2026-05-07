package tools

import (
	"context"
	"testing"
)

func TestTodoMemoryAndSessionSearchTools(t *testing.T) {
	todo := &fakeTodoManager{items: []map[string]any{{"text": "Inspect repo"}}}
	memory := &fakeMemoryManager{}
	search := &fakeSessionSearchManager{}

	todoTool := TodoTool{manager: todo}
	memoryTool := MemoryTool{manager: memory}
	searchTool := SessionSearchTool{manager: search}

	todoResult := todoTool.Execute(context.Background(), ToolRequest{
		SessionID: "session_1",
		Arguments: map[string]any{
			"action": "add",
			"item":   map[string]any{"text": "Write tests"},
		},
	})
	if todoResult.Status != StatusSuccess {
		t.Fatalf("expected todo tool success, got %s", todoResult.Status)
	}

	memoryResult := memoryTool.Execute(context.Background(), ToolRequest{
		ProfileID:   "default",
		ProfileRoot: t.TempDir(),
		Arguments: map[string]any{
			"action":  "write",
			"slug":    "project-notes",
			"title":   "Project Notes",
			"content": "remember this",
		},
	})
	if memoryResult.Status != StatusSuccess {
		t.Fatalf("expected memory tool success, got %s", memoryResult.Status)
	}

	searchResult := searchTool.Execute(context.Background(), ToolRequest{
		ProfileID: "default",
		Arguments: map[string]any{
			"query": "roadmap",
		},
	})
	if searchResult.Status != StatusSuccess {
		t.Fatalf("expected session search success, got %s", searchResult.Status)
	}
}

type fakeTodoManager struct {
	items []map[string]any
}

func (f *fakeTodoManager) GetSessionTodos(ctx context.Context, sessionID string) ([]map[string]any, error) {
	_ = ctx
	_ = sessionID
	return f.items, nil
}

func (f *fakeTodoManager) ReplaceSessionTodos(ctx context.Context, sessionID string, items []map[string]any) ([]map[string]any, error) {
	_ = ctx
	_ = sessionID
	f.items = items
	return items, nil
}

type fakeMemoryManager struct{}

func (f *fakeMemoryManager) ListMemoryDocuments(ctx context.Context, profileID string, limit int) (any, error) {
	_ = ctx
	_ = profileID
	_ = limit
	return []map[string]any{{"slug": "project-notes"}}, nil
}

func (f *fakeMemoryManager) ViewMemoryDocument(ctx context.Context, profileID, slug string) (any, string, error) {
	_ = ctx
	_ = profileID
	return map[string]any{"slug": slug}, "content", nil
}

func (f *fakeMemoryManager) WriteMemoryDocument(ctx context.Context, input MemoryWriteInput) (any, error) {
	_ = ctx
	return map[string]any{"slug": input.Slug}, nil
}

type fakeSessionSearchManager struct{}

func (f *fakeSessionSearchManager) SearchSessions(ctx context.Context, profileID, query string, limit int) (any, error) {
	_ = ctx
	_ = profileID
	_ = limit
	return []map[string]any{{"query": query}}, nil
}
