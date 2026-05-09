package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/config"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestReconcileDiscoversPluginsAndQuarantinesInvalidOnes(t *testing.T) {
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

	repoRoot := t.TempDir()
	profileRoot := t.TempDir()
	writePluginManifest(t, filepath.Join(repoRoot, ".agents", "plugins", "dashboard-kit", ".codex-plugin", "plugin.json"), `{
  "id": "dashboard-kit",
  "name": "Dashboard Kit",
  "category": "dashboard_extension",
  "description": "Dashboard widgets",
  "entryPoint": "index.js",
  "configSchema": {"type":"object"}
}`)
	writePluginManifest(t, filepath.Join(profileRoot, "plugins", "memory-alt", ".codex-plugin", "plugin.json"), `{
  "id": "memory-alt",
  "name": "Memory Alt",
  "category": "memory_backend",
  "description": "Alt memory",
  "entryPoint": "index.js",
  "configSchema": {"type":"object"},
  "trusted": false
}`)

	service := NewService(app)
	if err := service.Reconcile(t.Context(), profileRoot, config.PluginsConfig{
		RepoPaths:    []string{filepath.Join(repoRoot, ".agents", "plugins")},
		ProfilePaths: []string{"plugins"},
	}); err != nil {
		t.Fatalf("reconcile plugins: %v", err)
	}

	plugins, err := service.ListPlugins(t.Context(), 10)
	if err != nil {
		t.Fatalf("list plugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}

	var enabledFound, quarantinedFound bool
	for _, item := range plugins {
		switch item.PluginID {
		case "dashboard-kit":
			enabledFound = item.State == "enabled" && item.CategoryContract.Name == "dashboard_extension"
		case "memory-alt":
			quarantinedFound = item.State == "quarantined" && item.QuarantineReason != ""
		}
	}
	if !enabledFound || !quarantinedFound {
		t.Fatalf("unexpected plugin states: %#v", plugins)
	}
}

func writePluginManifest(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
}
