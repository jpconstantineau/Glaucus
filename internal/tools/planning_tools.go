package tools

import (
	"context"
	"fmt"
	"strings"
)

type TodoManager interface {
	GetSessionTodos(context.Context, string) ([]map[string]any, error)
	ReplaceSessionTodos(context.Context, string, []map[string]any) ([]map[string]any, error)
}

type MemoryManager interface {
	ListMemoryDocuments(context.Context, string, int) (any, error)
	ViewMemoryDocument(context.Context, string, string) (any, string, error)
	WriteMemoryDocument(context.Context, MemoryWriteInput) (any, error)
}

type SessionSearchManager interface {
	SearchSessions(context.Context, string, string, int) (any, error)
}

type MemoryWriteInput struct {
	ProfileID    string
	ProfileRoot  string
	Slug         string
	Title        string
	RelativePath string
	Content      string
}

func RegisterPlanningTools(registry *Registry, todo TodoManager, memory MemoryManager, search SessionSearchManager) {
	if registry == nil {
		return
	}
	if todo != nil {
		registry.Register(TodoTool{manager: todo})
	}
	if memory != nil {
		registry.Register(MemoryTool{manager: memory})
	}
	if search != nil {
		registry.Register(SessionSearchTool{manager: search})
	}
}

type TodoTool struct {
	manager TodoManager
}

func (t TodoTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "todo",
		Description: "Inspect and update session-scoped todo state.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string"},
				"session_id": map[string]any{"type": "string"},
				"item":       map[string]any{"type": "object"},
				"items":      map[string]any{"type": "array"},
			},
			"required": []string{"action"},
		},
		Toolsets:     []string{"todo"},
		Concurrency:  "single-flight",
		DisplayGroup: "planning",
	}
}

func (t TodoTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "todo manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t TodoTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	if t.manager == nil {
		return ToolResult{Status: StatusFatalError, DisplayText: "todo manager is unavailable"}
	}
	sessionID := fallbackString(stringArg(req.Arguments, "session_id"), req.SessionID)
	if sessionID == "" {
		return ToolResult{Status: StatusValidationError, DisplayText: "session_id is required"}
	}
	action := strings.ToLower(stringArg(req.Arguments, "action"))
	switch action {
	case "list":
		items, err := t.manager.GetSessionTodos(ctx, sessionID)
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"items": items}, DisplayText: fmt.Sprintf("Loaded %d todo items.", len(items))}
	case "replace":
		items := todoItemsArg(req.Arguments, "items")
		updated, err := t.manager.ReplaceSessionTodos(ctx, sessionID, items)
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"items": updated}, DisplayText: fmt.Sprintf("Saved %d todo items.", len(updated))}
	case "add":
		items, err := t.manager.GetSessionTodos(ctx, sessionID)
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		item := mapArg(req.Arguments, "item")
		if item == nil {
			return ToolResult{Status: StatusValidationError, DisplayText: "item is required"}
		}
		items = append(items, item)
		updated, err := t.manager.ReplaceSessionTodos(ctx, sessionID, items)
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"items": updated}, DisplayText: fmt.Sprintf("Added todo item. Total items: %d.", len(updated))}
	case "clear":
		updated, err := t.manager.ReplaceSessionTodos(ctx, sessionID, nil)
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"items": updated}, DisplayText: "Cleared session todos."}
	default:
		return ToolResult{Status: StatusValidationError, DisplayText: fmt.Sprintf("unsupported action %q", action)}
	}
}

type MemoryTool struct {
	manager MemoryManager
}

func (t MemoryTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "memory",
		Description: "List, inspect, and write durable markdown memories.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string"},
				"slug":    map[string]any{"type": "string"},
				"title":   map[string]any{"type": "string"},
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"action"},
		},
		Toolsets:     []string{"todo"},
		Flags:        ToolFlags{ApprovalSensitive: true},
		Concurrency:  "single-flight",
		DisplayGroup: "planning",
	}
}

func (t MemoryTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "memory manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t MemoryTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	if t.manager == nil {
		return ToolResult{Status: StatusFatalError, DisplayText: "memory manager is unavailable"}
	}
	action := strings.ToLower(stringArg(req.Arguments, "action"))
	switch action {
	case "list":
		items, err := t.manager.ListMemoryDocuments(ctx, req.ProfileID, intArg(req.Arguments, "limit", 20))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"documents": items}, DisplayText: fmt.Sprintf("Loaded %d memory documents.", sliceLen(items))}
	case "view":
		doc, content, err := t.manager.ViewMemoryDocument(ctx, req.ProfileID, stringArg(req.Arguments, "slug"))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"document": doc, "content": content}, DisplayText: "Loaded memory document."}
	case "write":
		doc, err := t.manager.WriteMemoryDocument(ctx, MemoryWriteInput{
			ProfileID:    req.ProfileID,
			ProfileRoot:  req.ProfileRoot,
			Slug:         stringArg(req.Arguments, "slug"),
			Title:        stringArg(req.Arguments, "title"),
			RelativePath: stringArg(req.Arguments, "path"),
			Content:      stringArg(req.Arguments, "content"),
		})
		if err != nil {
			return ToolResult{Status: StatusValidationError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"document": doc}, DisplayText: "Saved memory document."}
	default:
		return ToolResult{Status: StatusValidationError, DisplayText: fmt.Sprintf("unsupported action %q", action)}
	}
}

type SessionSearchTool struct {
	manager SessionSearchManager
}

func (t SessionSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "session_search",
		Description: "Search persisted sessions and messages through the SQLite FTS index.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
			},
			"required": []string{"query"},
		},
		Toolsets:     []string{"session_search"},
		Flags:        ToolFlags{ReadOnly: true},
		Concurrency:  "shared-read",
		DisplayGroup: "planning",
	}
}

func (t SessionSearchTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "search manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t SessionSearchTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	if t.manager == nil {
		return ToolResult{Status: StatusFatalError, DisplayText: "search manager is unavailable"}
	}
	query := stringArg(req.Arguments, "query")
	results, err := t.manager.SearchSessions(ctx, req.ProfileID, query, intArg(req.Arguments, "limit", 10))
	if err != nil {
		return ToolResult{Status: StatusValidationError, DisplayText: err.Error()}
	}
	return ToolResult{Status: StatusSuccess, Payload: map[string]any{"results": results}, DisplayText: fmt.Sprintf("Found %d matching session results.", sliceLen(results))}
}

func todoItemsArg(args map[string]any, key string) []map[string]any {
	if args == nil {
		return nil
	}
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, item)
	}
	return items
}
