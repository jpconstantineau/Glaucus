package tools

import (
	"context"
	"sort"
	"strings"
)

type ResultStatus string

const (
	StatusSuccess          ResultStatus = "success"
	StatusRecoverableError ResultStatus = "recoverable_error"
	StatusFatalError       ResultStatus = "fatal_error"
	StatusValidationError  ResultStatus = "validation_error"
	StatusApprovalRequired ResultStatus = "approval_required"
	StatusApprovalDenied   ResultStatus = "approval_denied"

	SurfaceWebChat       = "web_chat"
	SurfaceWebAdmin      = "web_admin"
	SurfaceAPIDefault    = "api_default"
	SurfaceGateway       = "gateway_default"
	SurfaceBackgroundJob = "cron"
)

type ToolFlags struct {
	Interactive       bool `json:"interactive"`
	ApprovalSensitive bool `json:"approval_sensitive"`
	ReadOnly          bool `json:"read_only"`
	PlatformScoped    bool `json:"platform_scoped"`
}

type ToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	JSONSchema   map[string]any `json:"json_schema"`
	Toolsets     []string       `json:"toolsets"`
	Flags        ToolFlags      `json:"flags"`
	Concurrency  string         `json:"concurrency"`
	DisplayGroup string         `json:"display_group,omitempty"`
}

type AvailabilityRequest struct {
	Surface          string
	ProfileRoot      string
	WorkingDirectory string
	Browser          BrowserBackend
}

type AvailabilityResult struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type ToolRequest struct {
	ProfileID        string
	SessionID        string
	RunID            string
	Surface          string
	ProfileRoot      string
	WorkingDirectory string
	Arguments        map[string]any
}

type ToolTiming struct {
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
}

type ToolResult struct {
	Status      ResultStatus   `json:"status"`
	Payload     any            `json:"payload,omitempty"`
	DisplayText string         `json:"display_text,omitempty"`
	Diagnostics map[string]any `json:"diagnostics,omitempty"`
	Timing      ToolTiming     `json:"timing,omitempty"`
}

type Tool interface {
	Definition() ToolDefinition
	CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult
	Execute(ctx context.Context, req ToolRequest) ToolResult
}

type BrowserBackend interface {
	Name() string
	Healthy(ctx context.Context) error
}

type Toolset struct {
	Name        string
	Description string
	Includes    []string
	Tools       []string
}

type ResolveRequest struct {
	Surface          string
	RequestedToolset string
	ExplicitTools    []string
	ExplicitDisables []string
	ProfileRoot      string
	WorkingDirectory string
	Browser          BrowserBackend
}

type ResolvedTool struct {
	Definition   ToolDefinition     `json:"definition"`
	Availability AvailabilityResult `json:"availability"`
}

type Resolution struct {
	Surface          string            `json:"surface"`
	RequestedToolset string            `json:"requested_toolset"`
	ExpandedToolsets []string          `json:"expanded_toolsets"`
	EnabledTools     []ResolvedTool    `json:"enabled_tools"`
	UnavailableTools []ResolvedTool    `json:"unavailable_tools"`
	DisabledTools    map[string]string `json:"disabled_tools,omitempty"`
	Availability     map[string]string `json:"availability,omitempty"`
	ToolNames        []string          `json:"tool_names"`
	ToolsetNames     []string          `json:"toolset_names"`
}

type Registry struct {
	tools           map[string]Tool
	toolsets        map[string]Toolset
	surfaceDefaults map[string]string
}

func NewRegistry() *Registry {
	r := &Registry{
		tools:    map[string]Tool{},
		toolsets: map[string]Toolset{},
		surfaceDefaults: map[string]string{
			SurfaceWebChat:       SurfaceWebChat,
			SurfaceWebAdmin:      SurfaceWebAdmin,
			SurfaceAPIDefault:    SurfaceAPIDefault,
			SurfaceGateway:       SurfaceGateway,
			SurfaceBackgroundJob: "safe",
		},
	}

	for _, toolset := range defaultToolsets() {
		r.AddToolset(toolset)
	}

	return r
}

func (r *Registry) Register(tool Tool) {
	if tool == nil {
		return
	}
	r.tools[tool.Definition().Name] = tool
}

func (r *Registry) AddToolset(toolset Toolset) {
	if strings.TrimSpace(toolset.Name) == "" {
		return
	}
	r.toolsets[toolset.Name] = toolset
}

func (r *Registry) ToolsetNames() []string {
	names := make([]string, 0, len(r.toolsets))
	for name := range r.toolsets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Resolve(ctx context.Context, req ResolveRequest) Resolution {
	requested := strings.TrimSpace(req.RequestedToolset)
	if requested == "" {
		requested = r.surfaceDefaults[req.Surface]
	}
	if requested == "" {
		requested = "safe"
	}

	expanded := make([]string, 0, 8)
	seenToolsets := map[string]struct{}{}
	selectedTools := map[string]struct{}{}
	for _, name := range req.ExplicitTools {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			selectedTools[trimmed] = struct{}{}
		}
	}

	var expandToolset func(name string)
	expandToolset = func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seenToolsets[name]; ok {
			return
		}
		seenToolsets[name] = struct{}{}
		expanded = append(expanded, name)

		toolset, ok := r.toolsets[name]
		if !ok {
			return
		}
		for _, include := range toolset.Includes {
			expandToolset(include)
		}
		for _, toolName := range toolset.Tools {
			if trimmed := strings.TrimSpace(toolName); trimmed != "" {
				selectedTools[trimmed] = struct{}{}
			}
		}
	}

	expandToolset(requested)

	disabled := map[string]string{}
	for _, name := range req.ExplicitDisables {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		delete(selectedTools, trimmed)
		disabled[trimmed] = "disabled explicitly"
	}

	names := make([]string, 0, len(selectedTools))
	for name := range selectedTools {
		names = append(names, name)
	}
	sort.Strings(names)

	resolution := Resolution{
		Surface:          req.Surface,
		RequestedToolset: requested,
		ExpandedToolsets: expanded,
		DisabledTools:    disabled,
		Availability:     map[string]string{},
		ToolsetNames:     expanded,
	}

	availabilityReq := AvailabilityRequest{
		Surface:          req.Surface,
		ProfileRoot:      req.ProfileRoot,
		WorkingDirectory: req.WorkingDirectory,
		Browser:          req.Browser,
	}

	for _, name := range names {
		tool, ok := r.tools[name]
		if !ok {
			resolution.Availability[name] = "tool is not registered"
			continue
		}
		availability := tool.CheckAvailability(ctx, availabilityReq)
		resolved := ResolvedTool{
			Definition:   tool.Definition(),
			Availability: availability,
		}
		if availability.Available {
			resolution.EnabledTools = append(resolution.EnabledTools, resolved)
			resolution.ToolNames = append(resolution.ToolNames, name)
			continue
		}
		reason := availability.Reason
		if reason == "" {
			reason = "tool is unavailable"
		}
		resolution.Availability[name] = reason
		resolution.UnavailableTools = append(resolution.UnavailableTools, resolved)
	}

	return resolution
}

func (r *Registry) Tool(name string) (Tool, bool) {
	tool, ok := r.tools[strings.TrimSpace(name)]
	return tool, ok
}

func defaultToolsets() []Toolset {
	return []Toolset{
		{Name: "safe", Description: "Safe baseline toolset", Includes: []string{"read_only"}},
		{Name: "read_only", Description: "Read-only local inspection tools", Tools: []string{"read_file", "search_files", "web_extract"}},
		{Name: "file", Description: "Local file tools", Tools: []string{"read_file", "write_file", "patch", "search_files"}},
		{Name: "terminal", Description: "Terminal and process tools", Tools: []string{"terminal", "process"}},
		{Name: "web", Description: "Web search and extraction tools", Tools: []string{"web_search", "web_extract"}},
		{Name: "browser", Description: "Browser-backed tools", Tools: []string{"browser_navigate", "browser_snapshot"}},
		{Name: "messaging", Description: "Messaging tools", Tools: []string{"send_message"}},
		{Name: "skills", Description: "Skill inspection and management tools", Tools: []string{"skills_list", "skill_view", "skill_manage"}},
		{Name: "todo", Description: "Todo tools", Tools: []string{"todo"}},
		{Name: "session_search", Description: "Session search tools", Tools: []string{"session_search"}},
		{Name: "cronjob", Description: "Scheduled job tools", Tools: []string{"cronjob"}},
		{Name: "delegation", Description: "Delegation tools", Tools: []string{"delegate_task"}},
		{Name: SurfaceWebChat, Description: "Browser chat default toolset", Includes: []string{"safe", "file", "terminal", "web", "browser", "messaging", "skills", "todo", "session_search"}},
		{Name: SurfaceWebAdmin, Description: "Dashboard admin default toolset", Includes: []string{"read_only", "file", "terminal", "web", "browser", "skills", "cronjob"}},
		{Name: SurfaceAPIDefault, Description: "API default toolset", Includes: []string{"safe", "web"}},
		{Name: SurfaceGateway, Description: "Gateway default toolset", Includes: []string{"safe", "messaging"}},
	}
}
