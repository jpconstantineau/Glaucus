package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapCreatesExpectedLayout(t *testing.T) {
	base := t.TempDir()

	active, err := Bootstrap(BootstrapOptions{
		BaseDir: base,
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	expectedPaths := []string{
		filepath.Join(active.Root, "config.yaml"),
		filepath.Join(active.Root, ".env"),
		filepath.Join(active.Root, "SOUL.md"),
		filepath.Join(active.Root, "memories", "MEMORY.md"),
		filepath.Join(active.Root, "memories", "USER.md"),
		filepath.Join(active.Root, "skills"),
		filepath.Join(active.Root, "cron"),
		filepath.Join(active.Root, "exports"),
		filepath.Join(active.Root, "logs"),
		filepath.Join(active.Root, "home"),
	}

	for _, path := range expectedPaths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	configPath := filepath.Join(active.Root, "config.yaml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	configText := string(configBytes)
	for _, expected := range []string{
		"defaultProvider: ollama-local",
		"defaultModel: llama3.2:3b",
		"baseURL: http://127.0.0.1:11434/v1",
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("expected generated config to contain %q, got:\n%s", expected, configText)
		}
	}
}

func TestResolveOwnedPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	if _, err := ResolveOwnedPath(root, filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestResolveOwnedPathAcceptsNestedFile(t *testing.T) {
	root := t.TempDir()

	target, err := ResolveOwnedPath(root, filepath.Join("memories", "MEMORY.md"))
	if err != nil {
		t.Fatalf("resolve owned path: %v", err)
	}

	if filepath.Dir(target) != filepath.Join(root, "memories") {
		t.Fatalf("unexpected resolved directory %s", filepath.Dir(target))
	}
}
