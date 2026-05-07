package tools

import "context"

type CatalogTool struct {
	definition   ToolDefinition
	availability func(context.Context, AvailabilityRequest) AvailabilityResult
}

func NewCatalogTool(definition ToolDefinition, availability func(context.Context, AvailabilityRequest) AvailabilityResult) *CatalogTool {
	return &CatalogTool{
		definition:   definition,
		availability: availability,
	}
}

func (t *CatalogTool) Definition() ToolDefinition {
	return t.definition
}

func (t *CatalogTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	if t.availability == nil {
		return AvailabilityResult{Available: true}
	}
	return t.availability(ctx, req)
}

func (t *CatalogTool) Execute(context.Context, ToolRequest) ToolResult {
	return ToolResult{
		Status:      StatusRecoverableError,
		DisplayText: "tool execution is not yet configured",
	}
}
