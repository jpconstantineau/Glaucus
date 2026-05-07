package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalogReadsRepoManagedManifests(t *testing.T) {
	dir := t.TempDir()

	manifest := []byte("providerId: ollama-local\ndisplayName: Local Ollama\nfamily: ollama\nbaseURL: http://127.0.0.1:11434/v1\ndialect: openai-chat\nmodels:\n  - providerModelId: llama3.2\n    displayName: Llama 3.2\n    capabilities: [chat, tools]\n    lifecycleStatus: preview\n    limits:\n      contextWindow: 8192\n      maxOutputTokens: 2048\n")
	if err := os.WriteFile(filepath.Join(dir, "ollama-local.yaml"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	catalog, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	if len(catalog.Entries) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(catalog.Entries))
	}

	entry := catalog.Entries[0]
	if entry.ProviderID != "ollama-local" {
		t.Fatalf("unexpected provider id %q", entry.ProviderID)
	}
	if entry.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("unexpected base url %q", entry.BaseURL)
	}
}

func TestLoadCatalogFailsOnMalformedManifest(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("providerId: broken\nmodels: ["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, err := LoadCatalog(dir); err == nil {
		t.Fatal("expected malformed manifest to fail")
	}
}
