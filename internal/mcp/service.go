package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionServers = "mcp_servers"

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

type ToolDescriptor struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	InputSchema     map[string]any `json:"input_schema,omitempty"`
	Toolsets        []string       `json:"toolsets,omitempty"`
	AllowedSurfaces []string       `json:"allowed_surfaces,omitempty"`
}

type Server struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Command         string            `json:"command"`
	Args            []string          `json:"args,omitempty"`
	Status          string            `json:"status"`
	HealthReason    string            `json:"health_reason,omitempty"`
	AdvertisedTools []ToolDescriptor  `json:"advertised_tools,omitempty"`
	ExposedTools    []string          `json:"exposed_tools,omitempty"`
	DeniedTools     map[string]string `json:"denied_tools,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func (s *Service) Reconcile(ctx context.Context, cfg config.Config, registry *tools.Registry) error {
	if s == nil || s.app == nil {
		return nil
	}

	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	seenExposed := map[string]string{}
	for _, name := range names {
		serverCfg := cfg.MCPServers[name]
		record, err := s.findOrCreateRecord(name)
		if err != nil {
			return err
		}

		server := Server{
			ID:              record.Id,
			Name:            name,
			Command:         strings.TrimSpace(serverCfg.Command),
			Args:            append([]string{}, serverCfg.Args...),
			Status:          "available",
			AdvertisedTools: make([]ToolDescriptor, 0, len(serverCfg.Tools)),
			ExposedTools:    []string{},
			DeniedTools:     map[string]string{},
			CreatedAt:       record.GetDateTime("created").Time(),
			UpdatedAt:       record.GetDateTime("updated").Time(),
		}
		if server.Command == "" {
			server.Status = "degraded"
			server.HealthReason = "command is not configured"
		}

		for _, toolCfg := range serverCfg.Tools {
			descriptor := ToolDescriptor{
				Name:            strings.TrimSpace(toolCfg.Name),
				Description:     strings.TrimSpace(toolCfg.Description),
				InputSchema:     cloneMap(toolCfg.InputSchema),
				Toolsets:        append([]string{}, toolCfg.Toolsets...),
				AllowedSurfaces: append([]string{}, toolCfg.AllowedSurfaces...),
			}
			server.AdvertisedTools = append(server.AdvertisedTools, descriptor)

			denyReason := validateToolPolicy(descriptor, registry, seenExposed)
			if denyReason != "" {
				server.DeniedTools[descriptor.Name] = denyReason
				continue
			}

			if registry != nil {
				registry.Register(newRemoteTool(server.Name, server.Status, server.HealthReason, toolCfg))
				for _, toolsetName := range fallbackToolsets(toolCfg.Toolsets) {
					registry.AppendToolToToolset(toolsetName, descriptor.Name)
				}
			}
			server.ExposedTools = append(server.ExposedTools, descriptor.Name)
			seenExposed[descriptor.Name] = server.Name
		}

		record.Set("name", server.Name)
		record.Set("command", server.Command)
		if err := setJSON(record, "args_json", server.Args); err != nil {
			return err
		}
		record.Set("status", server.Status)
		record.Set("health_reason", server.HealthReason)
		if err := setJSON(record, "advertised_tools_json", server.AdvertisedTools); err != nil {
			return err
		}
		if err := setJSON(record, "exposed_tools_json", server.ExposedTools); err != nil {
			return err
		}
		if err := setJSON(record, "denied_tools_json", server.DeniedTools); err != nil {
			return err
		}
		if err := s.app.SaveWithContext(ctx, record); err != nil {
			return fmt.Errorf("save mcp server %s: %w", server.Name, err)
		}
	}

	return nil
}

func (s *Service) ListServers(ctx context.Context, limit int) ([]Server, error) {
	if s == nil || s.app == nil {
		return nil, nil
	}
	records, err := s.app.FindRecordsByFilter(CollectionServers, "id != ''", "name", limit, 0)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	servers := make([]Server, 0, len(records))
	for _, record := range records {
		server, err := serverFromRecord(record)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	_ = ctx
	return servers, nil
}

func (s *Service) findOrCreateRecord(name string) (*core.Record, error) {
	record, err := s.app.FindFirstRecordByFilter(CollectionServers, "name = {:name}", dbx.Params{"name": name})
	if err == nil && record != nil {
		return record, nil
	}

	collection, findErr := s.app.FindCollectionByNameOrId(CollectionServers)
	if findErr != nil {
		return nil, fmt.Errorf("find mcp servers collection: %w", findErr)
	}
	record = core.NewRecord(collection)
	record.Set("name", name)
	return record, nil
}

type remoteTool struct {
	serverName    string
	serverStatus  string
	healthReason  string
	definition    tools.ToolDefinition
	allowedBySurf map[string]struct{}
}

func newRemoteTool(serverName, serverStatus, healthReason string, cfg config.MCPToolConfig) tools.Tool {
	allowed := map[string]struct{}{}
	for _, surface := range cfg.AllowedSurfaces {
		trimmed := strings.TrimSpace(surface)
		if trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return &remoteTool{
		serverName:   serverName,
		serverStatus: serverStatus,
		healthReason: healthReason,
		definition: tools.ToolDefinition{
			Name:        strings.TrimSpace(cfg.Name),
			Description: fallbackString(strings.TrimSpace(cfg.Description), "MCP-provided tool"),
			JSONSchema:  cloneMap(cfg.InputSchema),
			Toolsets:    fallbackToolsets(cfg.Toolsets),
			Flags: tools.ToolFlags{
				Interactive:       cfg.Interactive,
				ApprovalSensitive: cfg.ApprovalSensitive,
				ReadOnly:          cfg.ReadOnly,
				PlatformScoped:    cfg.PlatformScoped,
			},
			Concurrency:  "serialized",
			DisplayGroup: "mcp:" + serverName,
		},
		allowedBySurf: allowed,
	}
}

func (t *remoteTool) Definition() tools.ToolDefinition {
	return t.definition
}

func (t *remoteTool) CheckAvailability(_ context.Context, req tools.AvailabilityRequest) tools.AvailabilityResult {
	if t.serverStatus != "available" {
		reason := fallbackString(t.healthReason, "mcp server is unavailable")
		return tools.AvailabilityResult{Available: false, Reason: reason}
	}
	if len(t.allowedBySurf) > 0 {
		if _, ok := t.allowedBySurf[req.Surface]; !ok {
			return tools.AvailabilityResult{Available: false, Reason: "tool is blocked on this surface by MCP policy"}
		}
	}
	return tools.AvailabilityResult{Available: true}
}

func (t *remoteTool) Execute(context.Context, tools.ToolRequest) tools.ToolResult {
	return tools.ToolResult{
		Status:      tools.StatusRecoverableError,
		DisplayText: fmt.Sprintf("MCP tool %q from server %q is registered, but remote execution is not configured in this slice.", t.definition.Name, t.serverName),
	}
}

func validateToolPolicy(tool ToolDescriptor, registry *tools.Registry, seen map[string]string) string {
	if !toolNamePattern.MatchString(tool.Name) {
		return "tool name must match ^[a-z][a-z0-9_]{1,63}$"
	}
	if tool.Description == "" {
		return "tool description is required"
	}
	if owner, ok := seen[tool.Name]; ok {
		return fmt.Sprintf("tool name already exposed by %s", owner)
	}
	if registry != nil {
		if _, exists := registry.Tool(tool.Name); exists {
			return "tool name conflicts with an existing registered tool"
		}
	}
	return ""
}

func serverFromRecord(record *core.Record) (Server, error) {
	server := Server{
		ID:           record.Id,
		Name:         record.GetString("name"),
		Command:      record.GetString("command"),
		Status:       record.GetString("status"),
		HealthReason: record.GetString("health_reason"),
		CreatedAt:    record.GetDateTime("created").Time(),
		UpdatedAt:    record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "args_json", &server.Args); err != nil {
		return Server{}, err
	}
	if err := decodeJSONField(record, "advertised_tools_json", &server.AdvertisedTools); err != nil {
		return Server{}, err
	}
	if err := decodeJSONField(record, "exposed_tools_json", &server.ExposedTools); err != nil {
		return Server{}, err
	}
	if err := decodeJSONField(record, "denied_tools_json", &server.DeniedTools); err != nil {
		return Server{}, err
	}
	return server, nil
}

func cloneMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return map[string]any{}
	}
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func fallbackToolsets(toolsets []string) []string {
	filtered := make([]string, 0, len(toolsets))
	for _, toolset := range toolsets {
		trimmed := strings.TrimSpace(toolset)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return []string{"safe"}
	}
	return filtered
}

func fallbackString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func setJSON(record *core.Record, field string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", field, err)
	}
	record.Set(field, string(raw))
	return nil
}

func decodeJSONField(record *core.Record, field string, target any) error {
	raw := record.GetString(field)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}
