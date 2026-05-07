package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type BootstrapOptions struct {
	BaseDir string
	Slug    string
}

type ActiveProfile struct {
	Slug string
	Root string
}

func ResolveRoot(baseDir, slug string) (ActiveProfile, error) {
	if slug == "" {
		return ActiveProfile{}, errors.New("profile slug is required")
	}
	if strings.Contains(slug, "..") {
		return ActiveProfile{}, errors.New("profile slug may not contain traversal segments")
	}

	root := filepath.Join(baseDir, slug)
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return ActiveProfile{}, fmt.Errorf("resolve profile root: %w", err)
	}

	return ActiveProfile{
		Slug: slug,
		Root: absoluteRoot,
	}, nil
}

func Bootstrap(opts BootstrapOptions) (ActiveProfile, error) {
	profile, err := ResolveRoot(opts.BaseDir, opts.Slug)
	if err != nil {
		return ActiveProfile{}, err
	}

	dirs := []string{
		profile.Root,
		filepath.Join(profile.Root, "memories"),
		filepath.Join(profile.Root, "skills"),
		filepath.Join(profile.Root, "cron"),
		filepath.Join(profile.Root, "exports"),
		filepath.Join(profile.Root, "logs"),
		filepath.Join(profile.Root, "home"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ActiveProfile{}, fmt.Errorf("create profile directory %s: %w", dir, err)
		}
	}

	files := map[string]string{
		filepath.Join(profile.Root, "config.yaml"):           defaultConfigYAML(),
		filepath.Join(profile.Root, ".env"):                  "",
		filepath.Join(profile.Root, "SOUL.md"):               "# Glaucus\n",
		filepath.Join(profile.Root, "memories", "MEMORY.md"): "# Memory\n",
		filepath.Join(profile.Root, "memories", "USER.md"):   "# User\n",
	}

	for path, content := range files {
		if err := ensureFile(path, content); err != nil {
			return ActiveProfile{}, err
		}
	}

	return profile, nil
}

func ResolveOwnedPath(root string, relative string) (string, error) {
	if root == "" {
		return "", errors.New("profile root is required")
	}

	joined := filepath.Join(root, relative)
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	absoluteTarget, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolve target: %w", err)
	}

	rel, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("resolve target relative path: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes active profile root", relative)
	}

	return absoluteTarget, nil
}

func ensureFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func defaultConfigYAML() string {
	return `model:
  defaultProvider: ollama-local
  defaultModel: llama3.2:3b

providers:
  ollama-local:
    baseURL: http://127.0.0.1:11434/v1
    dialect: openai-chat

web:
  bindAddress: 127.0.0.1:8090

pocketbase:
  dataDir: pb_data
`
}
