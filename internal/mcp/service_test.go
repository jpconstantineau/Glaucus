package mcp

import (
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/pocketbase/core"
)

func TestReconcileRegistersPolicyFilteredTools(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	registry := tools.NewRegistry()
	tools.RegisterCatalogDefaults(registry)
	tools.RegisterFileTools(registry)

	cfg := config.Default()
	cfg.MCPServers["filesystem"] = config.MCPServerConfig{
		Command: "npx",
		Args:    []string{"@modelcontextprotocol/server-filesystem"},
		Tools: []config.MCPToolConfig{
			{Name: "custom_lookup", Description: "Lookup project metadata", Toolsets: []string{"safe"}, AllowedSurfaces: []string{tools.SurfaceWebChat}, ReadOnly: true},
			{Name: "read_file", Description: "Conflicts with builtin"},
		},
	}

	service := NewService(app)
	if err := service.Reconcile(t.Context(), cfg, registry); err != nil {
		t.Fatalf("reconcile mcp config: %v", err)
	}

	if _, ok := registry.Tool("custom_lookup"); !ok {
		t.Fatal("expected custom_lookup to be registered")
	}
	serverList, err := service.ListServers(t.Context(), 10)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(serverList) != 1 {
		t.Fatalf("expected one mcp server record, got %d", len(serverList))
	}
	if len(serverList[0].ExposedTools) != 1 || serverList[0].ExposedTools[0] != "custom_lookup" {
		t.Fatalf("unexpected exposed tools: %#v", serverList[0].ExposedTools)
	}
	if serverList[0].DeniedTools["read_file"] == "" {
		t.Fatalf("expected denied reason for read_file collision, got %#v", serverList[0].DeniedTools)
	}

	resolution := registry.Resolve(t.Context(), tools.ResolveRequest{
		Surface:          tools.SurfaceWebChat,
		RequestedToolset: "safe",
	})
	found := false
	for _, tool := range resolution.EnabledTools {
		if tool.Definition.Name == "custom_lookup" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected custom_lookup in tool resolution, got %#v", resolution.EnabledTools)
	}

	adminResolution := registry.Resolve(t.Context(), tools.ResolveRequest{
		Surface:          tools.SurfaceWebAdmin,
		RequestedToolset: "safe",
	})
	found = false
	for _, tool := range adminResolution.UnavailableTools {
		if tool.Definition.Name == "custom_lookup" && tool.Availability.Reason != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected custom_lookup to be unavailable on admin surface, got %#v", adminResolution.UnavailableTools)
	}
}
