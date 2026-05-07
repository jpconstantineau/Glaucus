package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesPrecedenceFlagsOverYAMLOverEnvOverDefaults(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("GLAUCUS_MODEL_DEFAULT_PROVIDER=env-provider\nGLAUCUS_WEB_BIND_ADDRESS=127.0.0.1:9000\n"), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	yaml := []byte("model:\n  defaultProvider: yaml-provider\n  defaultModel: yaml-model\nweb:\n  bindAddress: 127.0.0.1:7000\npocketbase:\n  dataDir: yaml-data\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), yaml, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := Load(root, FlagOverrides{
		ModelDefaultProvider: "flag-provider",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if loaded.Config.Model.DefaultProvider != "flag-provider" {
		t.Fatalf("expected flag provider, got %q", loaded.Config.Model.DefaultProvider)
	}
	if loaded.Config.Model.DefaultModel != "yaml-model" {
		t.Fatalf("expected yaml model, got %q", loaded.Config.Model.DefaultModel)
	}
	if loaded.Config.Web.BindAddress != "127.0.0.1:7000" {
		t.Fatalf("expected yaml bind address, got %q", loaded.Config.Web.BindAddress)
	}
	if loaded.Config.PocketBase.DataDir != "yaml-data" {
		t.Fatalf("expected yaml data dir, got %q", loaded.Config.PocketBase.DataDir)
	}
}

func TestLoadRejectsMalformedDotEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("oops"), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	if _, err := Load(root, FlagOverrides{}); err == nil {
		t.Fatal("expected load to fail for malformed env file")
	}
}
