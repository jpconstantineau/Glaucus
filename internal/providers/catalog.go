package providers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	ProviderID  string            `yaml:"providerId"`
	DisplayName string            `yaml:"displayName"`
	Family      string            `yaml:"family"`
	BaseURL     string            `yaml:"baseURL"`
	Dialect     string            `yaml:"dialect"`
	Headers     map[string]string `yaml:"headers"`
	Models      []ManifestModel   `yaml:"models"`
}

type ManifestModel struct {
	ProviderModelID string              `yaml:"providerModelId"`
	DisplayName     string              `yaml:"displayName"`
	Capabilities    []string            `yaml:"capabilities"`
	LifecycleStatus string              `yaml:"lifecycleStatus"`
	Limits          ManifestLimits      `yaml:"limits"`
	RequiredHeaders []HeaderRequirement `yaml:"requiredHeaders"`
}

type ManifestLimits struct {
	ContextWindow   int `yaml:"contextWindow"`
	MaxOutputTokens int `yaml:"maxOutputTokens"`
}

type HeaderRequirement struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type CatalogEntry struct {
	ProviderID      string
	ModelID         string
	DisplayName     string
	ProviderFamily  string
	Capabilities    []string
	LifecycleStatus string
	BaseURL         string
	Dialect         string
	Limits          ManifestLimits
	RequiredHeaders []HeaderRequirement
	DefaultHeaders  map[string]string
}

type Catalog struct {
	Entries []CatalogEntry
}

func LoadCatalog(dir string) (Catalog, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return Catalog{}, fmt.Errorf("list provider manifests: %w", err)
	}
	if len(files) == 0 {
		return Catalog{}, fmt.Errorf("no provider manifests found in %s", dir)
	}

	sort.Strings(files)

	entries := make([]CatalogEntry, 0, len(files)*2)
	seen := map[string]struct{}{}

	for _, path := range files {
		manifest, err := loadManifest(path)
		if err != nil {
			return Catalog{}, err
		}

		for _, model := range manifest.Models {
			key := manifest.ProviderID + "/" + model.ProviderModelID
			if _, exists := seen[key]; exists {
				return Catalog{}, fmt.Errorf("duplicate provider model %s", key)
			}
			seen[key] = struct{}{}

			entries = append(entries, CatalogEntry{
				ProviderID:      manifest.ProviderID,
				ModelID:         model.ProviderModelID,
				DisplayName:     model.DisplayName,
				ProviderFamily:  manifest.Family,
				Capabilities:    append([]string{}, model.Capabilities...),
				LifecycleStatus: model.LifecycleStatus,
				BaseURL:         manifest.BaseURL,
				Dialect:         manifest.Dialect,
				Limits:          model.Limits,
				RequiredHeaders: append([]HeaderRequirement{}, model.RequiredHeaders...),
				DefaultHeaders:  cloneHeaders(manifest.Headers),
			})
		}
	}

	return Catalog{Entries: entries}, nil
}

func loadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read provider manifest %s: %w", path, err)
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse provider manifest %s: %w", path, err)
	}

	if err := validateManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("validate provider manifest %s: %w", path, err)
	}

	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.ProviderID == "" {
		return errors.New("providerId is required")
	}
	if manifest.BaseURL == "" {
		return errors.New("baseURL is required")
	}
	if manifest.Dialect == "" {
		return errors.New("dialect is required")
	}
	if len(manifest.Models) == 0 {
		return errors.New("at least one model is required")
	}

	for _, model := range manifest.Models {
		if model.ProviderModelID == "" {
			return errors.New("models.providerModelId is required")
		}
		if model.DisplayName == "" {
			return fmt.Errorf("model %s displayName is required", model.ProviderModelID)
		}
		if model.LifecycleStatus == "" {
			return fmt.Errorf("model %s lifecycleStatus is required", model.ProviderModelID)
		}
	}

	return nil
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}
