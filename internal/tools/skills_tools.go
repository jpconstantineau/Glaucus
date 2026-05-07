package tools

import (
	"context"
	"fmt"
	"strings"
)

type SkillsManager interface {
	ListSkills(context.Context, string, int) (any, error)
	ViewSkill(context.Context, string, string) (any, string, error)
	ManageSkill(context.Context, SkillsManageInput) (any, error)
}

type SkillsManageInput struct {
	Action      string
	ProfileID   string
	ProfileRoot string
	Slug        string
	Name        string
	SourcePath  string
	SourceURL   string
	State       string
	TrustLevel  string
}

func RegisterSkillsTools(registry *Registry, manager SkillsManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(SkillsListTool{manager: manager})
	registry.Register(SkillViewTool{manager: manager})
	registry.Register(SkillManageTool{manager: manager})
}

type SkillsListTool struct {
	manager SkillsManager
}

func (t SkillsListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "skills_list",
		Description: "List durable skills and their lifecycle metadata.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer"},
			},
		},
		Toolsets:     []string{"skills"},
		Flags:        ToolFlags{ReadOnly: true},
		Concurrency:  "shared-read",
		DisplayGroup: "planning",
	}
}

func (t SkillsListTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "skills manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t SkillsListTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	items, err := t.manager.ListSkills(ctx, req.ProfileID, intArg(req.Arguments, "limit", 50))
	if err != nil {
		return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
	}
	return ToolResult{Status: StatusSuccess, Payload: map[string]any{"skills": items}, DisplayText: fmt.Sprintf("Loaded %d skills.", sliceLen(items))}
}

type SkillViewTool struct {
	manager SkillsManager
}

func (t SkillViewTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "skill_view",
		Description: "Inspect a skill body and metadata.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug": map[string]any{"type": "string"},
			},
			"required": []string{"slug"},
		},
		Toolsets:     []string{"skills"},
		Flags:        ToolFlags{ReadOnly: true},
		Concurrency:  "shared-read",
		DisplayGroup: "planning",
	}
}

func (t SkillViewTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "skills manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t SkillViewTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	skill, content, err := t.manager.ViewSkill(ctx, req.ProfileID, stringArg(req.Arguments, "slug"))
	if err != nil {
		return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
	}
	return ToolResult{Status: StatusSuccess, Payload: map[string]any{"skill": skill, "content": content}, DisplayText: "Loaded skill."}
}

type SkillManageTool struct {
	manager SkillsManager
}

func (t SkillManageTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "skill_manage",
		Description: "Install, update, pin, archive, and otherwise manage skill lifecycle.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string"},
				"slug":        map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"source_path": map[string]any{"type": "string"},
				"source_url":  map[string]any{"type": "string"},
				"state":       map[string]any{"type": "string"},
				"trust_level": map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
		Toolsets:     []string{"skills"},
		Flags:        ToolFlags{ApprovalSensitive: true},
		Concurrency:  "single-flight",
		DisplayGroup: "planning",
	}
}

func (t SkillManageTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "skills manager is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t SkillManageTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	action := strings.ToLower(stringArg(req.Arguments, "action"))
	if action == "" {
		return ToolResult{Status: StatusValidationError, DisplayText: "action is required"}
	}
	skill, err := t.manager.ManageSkill(ctx, SkillsManageInput{
		Action:      action,
		ProfileID:   req.ProfileID,
		ProfileRoot: req.ProfileRoot,
		Slug:        stringArg(req.Arguments, "slug"),
		Name:        stringArg(req.Arguments, "name"),
		SourcePath:  stringArg(req.Arguments, "source_path"),
		SourceURL:   stringArg(req.Arguments, "source_url"),
		State:       stringArg(req.Arguments, "state"),
		TrustLevel:  stringArg(req.Arguments, "trust_level"),
	})
	if err != nil {
		return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
	}
	return ToolResult{Status: StatusSuccess, Payload: map[string]any{"skill": skill}, DisplayText: fmt.Sprintf("Completed skill action %q.", action)}
}
