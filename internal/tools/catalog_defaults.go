package tools

import (
	"context"
	"fmt"
)

func RegisterCatalogDefaults(registry *Registry) {
	if registry == nil {
		return
	}

	definitions := []ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read a UTF-8 text file from an approved local root.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "integer"},
					"limit":      map[string]any{"type": "integer"},
				},
				"required": []string{"path"},
			},
			Toolsets:     []string{"read_only", "file", "safe"},
			Flags:        ToolFlags{ReadOnly: true},
			Concurrency:  "shared-read",
			DisplayGroup: "file",
		},
		{
			Name:        "write_file",
			Description: "Write UTF-8 text to an approved local file path.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
			Toolsets:     []string{"file"},
			Flags:        ToolFlags{ApprovalSensitive: true},
			Concurrency:  "exclusive-write",
			DisplayGroup: "file",
		},
		{
			Name:        "patch",
			Description: "Patch a local UTF-8 text file by replacing a line range.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string"},
					"start_line":  map[string]any{"type": "integer"},
					"end_line":    map[string]any{"type": "integer"},
					"replacement": map[string]any{"type": "string"},
				},
				"required": []string{"path", "start_line", "end_line", "replacement"},
			},
			Toolsets:     []string{"file"},
			Flags:        ToolFlags{ApprovalSensitive: true},
			Concurrency:  "exclusive-write",
			DisplayGroup: "file",
		},
		{
			Name:        "search_files",
			Description: "Search approved local text files for a substring.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"path":  map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
				"required": []string{"query"},
			},
			Toolsets:     []string{"read_only", "file", "safe"},
			Flags:        ToolFlags{ReadOnly: true},
			Concurrency:  "shared-read",
			DisplayGroup: "file",
		},
		{
			Name:        "terminal",
			Description: "Run a foreground shell command with bounded execution metadata.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":    map[string]any{"type": "string"},
					"timeout_ms": map[string]any{"type": "integer"},
				},
				"required": []string{"command"},
			},
			Toolsets:     []string{"terminal"},
			Flags:        ToolFlags{ApprovalSensitive: true, PlatformScoped: true},
			Concurrency:  "single-flight",
			DisplayGroup: "process",
		},
		{
			Name:        "process",
			Description: "Run or inspect a background process using a durable logical handle.",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":     map[string]any{"type": "string"},
					"command":    map[string]any{"type": "string"},
					"handle":     map[string]any{"type": "string"},
					"timeout_ms": map[string]any{"type": "integer"},
				},
				"required": []string{"action"},
			},
			Toolsets:     []string{"terminal"},
			Flags:        ToolFlags{ApprovalSensitive: true, PlatformScoped: true},
			Concurrency:  "single-flight",
			DisplayGroup: "process",
		},
		{
			Name:        "web_search",
			Description: "Search the web through the configured extraction backend.",
			JSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []string{"query"},
			},
			Toolsets:     []string{"web"},
			Concurrency:  "network-bound",
			DisplayGroup: "web",
		},
		{
			Name:        "web_extract",
			Description: "Fetch and extract plain text content from a URL.",
			JSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"url": map[string]any{"type": "string"}},
				"required":   []string{"url"},
			},
			Toolsets:     []string{"web", "read_only", "safe"},
			Flags:        ToolFlags{ReadOnly: true},
			Concurrency:  "network-bound",
			DisplayGroup: "web",
		},
		{
			Name:        "browser_navigate",
			Description: "Navigate the configured browser backend to a target page.",
			JSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"url": map[string]any{"type": "string"}},
				"required":   []string{"url"},
			},
			Toolsets:     []string{"browser"},
			Flags:        ToolFlags{Interactive: true, PlatformScoped: true},
			Concurrency:  "single-flight",
			DisplayGroup: "browser",
		},
		{
			Name:        "browser_snapshot",
			Description: "Capture a normalized snapshot from the configured browser backend.",
			JSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"target": map[string]any{"type": "string"}},
			},
			Toolsets:     []string{"browser"},
			Flags:        ToolFlags{ReadOnly: true, PlatformScoped: true},
			Concurrency:  "single-flight",
			DisplayGroup: "browser",
		},
	}

	for _, definition := range definitions {
		definition := definition
		registry.Register(NewCatalogTool(definition, func(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
			_ = ctx
			if definition.DisplayGroup == "browser" {
				if req.Browser == nil {
					return AvailabilityResult{Available: false, Reason: "no browser backend is configured"}
				}
				if err := req.Browser.Healthy(context.Background()); err != nil {
					return AvailabilityResult{Available: false, Reason: fmt.Sprintf("browser backend unavailable: %v", err)}
				}
			}
			return AvailabilityResult{Available: true}
		}))
	}
}
