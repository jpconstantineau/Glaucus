package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionPlugins = "plugins"

type CategoryContract struct {
	Name            string   `json:"name"`
	DiscoveryPaths  []string `json:"discovery_paths"`
	Multiplicity    string   `json:"multiplicity"`
	EnablementRules string   `json:"enablement_rules"`
}

type Manifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Category        string         `json:"category"`
	Description     string         `json:"description"`
	EntryPoint      string         `json:"entryPoint"`
	ConfigSchema    map[string]any `json:"configSchema"`
	Trusted         *bool          `json:"trusted,omitempty"`
	EnableByDefault *bool          `json:"enableByDefault,omitempty"`
}

type Plugin struct {
	ID               string           `json:"id"`
	PluginID         string           `json:"plugin_id"`
	Name             string           `json:"name"`
	Version          string           `json:"version"`
	Category         string           `json:"category"`
	Description      string           `json:"description"`
	State            string           `json:"state"`
	TrustLevel       string           `json:"trust_level"`
	RootPath         string           `json:"root_path"`
	ManifestPath     string           `json:"manifest_path"`
	DiscoverySource  string           `json:"discovery_source"`
	QuarantineReason string           `json:"quarantine_reason,omitempty"`
	CategoryContract CategoryContract `json:"category_contract"`
	ConfigSchema     map[string]any   `json:"config_schema,omitempty"`
	Metadata         map[string]any   `json:"metadata,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func Contracts() map[string]CategoryContract {
	return map[string]CategoryContract{
		"general":                  {Name: "general", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "many", EnablementRules: "trusted manifests auto-enable"},
		"memory_backend":           {Name: "memory_backend", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "single", EnablementRules: "one enabled backend per profile"},
		"context_engine":           {Name: "context_engine", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "single", EnablementRules: "one enabled engine per profile"},
		"image_generation_backend": {Name: "image_generation_backend", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "many", EnablementRules: "trusted manifests auto-enable"},
		"messaging_adapter":        {Name: "messaging_adapter", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "many", EnablementRules: "trusted manifests auto-enable"},
		"dashboard_extension":      {Name: "dashboard_extension", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "many", EnablementRules: "trusted manifests auto-enable"},
		"model_provider":           {Name: "model_provider", DiscoveryPaths: []string{".agents/plugins", "plugins"}, Multiplicity: "many", EnablementRules: "trusted manifests auto-enable"},
	}
}

func (s *Service) Reconcile(ctx context.Context, profileRoot string, cfg config.PluginsConfig) error {
	if s == nil || s.app == nil {
		return nil
	}

	discovered, err := s.discover(profileRoot, cfg)
	if err != nil {
		return err
	}

	contracts := Contracts()
	enabledSingletons := map[string]string{}
	for i := range discovered {
		plugin := &discovered[i]
		contract, ok := contracts[plugin.Category]
		if !ok {
			plugin.State = "quarantined"
			plugin.QuarantineReason = "plugin category is not supported"
			continue
		}
		plugin.CategoryContract = contract
		if plugin.State == "quarantined" {
			continue
		}
		if contract.Multiplicity == "single" {
			if existing, ok := enabledSingletons[plugin.Category]; ok {
				plugin.State = "quarantined"
				plugin.QuarantineReason = "plugin category already claimed by " + existing
				continue
			}
			enabledSingletons[plugin.Category] = plugin.PluginID
		}
	}

	for _, plugin := range discovered {
		record, err := s.findOrCreateRecord(plugin.PluginID)
		if err != nil {
			return err
		}
		record.Set("plugin_id", plugin.PluginID)
		record.Set("name", plugin.Name)
		record.Set("version", plugin.Version)
		record.Set("category", plugin.Category)
		record.Set("description", plugin.Description)
		record.Set("state", plugin.State)
		record.Set("trust_level", plugin.TrustLevel)
		record.Set("root_path", filepath.ToSlash(plugin.RootPath))
		record.Set("manifest_path", filepath.ToSlash(plugin.ManifestPath))
		record.Set("discovery_source", plugin.DiscoverySource)
		record.Set("quarantine_reason", plugin.QuarantineReason)
		if err := setJSON(record, "category_contract_json", plugin.CategoryContract); err != nil {
			return err
		}
		if err := setJSON(record, "config_schema_json", plugin.ConfigSchema); err != nil {
			return err
		}
		if err := setJSON(record, "metadata_json", plugin.Metadata); err != nil {
			return err
		}
		if err := s.app.SaveWithContext(ctx, record); err != nil {
			return fmt.Errorf("save plugin %s: %w", plugin.PluginID, err)
		}
	}

	return nil
}

func (s *Service) ListPlugins(ctx context.Context, limit int) ([]Plugin, error) {
	if s == nil || s.app == nil {
		return nil, nil
	}
	records, err := s.app.FindRecordsByFilter(CollectionPlugins, "id != ''", "plugin_id", limit, 0)
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	result := make([]Plugin, 0, len(records))
	for _, record := range records {
		item, err := pluginFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	_ = ctx
	return result, nil
}

func (s *Service) discover(profileRoot string, cfg config.PluginsConfig) ([]Plugin, error) {
	type rootConfig struct {
		base   string
		source string
	}
	roots := make([]rootConfig, 0, len(cfg.RepoPaths)+len(cfg.ProfilePaths))
	for _, item := range cfg.RepoPaths {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			roots = append(roots, rootConfig{base: trimmed, source: "repo"})
		}
	}
	for _, item := range cfg.ProfilePaths {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			roots = append(roots, rootConfig{base: filepath.Join(profileRoot, trimmed), source: "profile"})
		}
	}

	discovered := []Plugin{}
	for _, root := range roots {
		rootPath, err := filepath.Abs(root.base)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin root %s: %w", root.base, err)
		}
		info, err := os.Stat(rootPath)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Base(path) != "plugin.json" || filepath.Base(filepath.Dir(path)) != ".codex-plugin" {
				return nil
			}
			item, err := loadPlugin(path, root.source)
			if err != nil {
				discovered = append(discovered, Plugin{
					PluginID:         filepath.ToSlash(strings.TrimSuffix(path, filepath.Join(".codex-plugin", "plugin.json"))),
					Name:             filepath.Base(filepath.Dir(filepath.Dir(path))),
					State:            "quarantined",
					TrustLevel:       "untrusted",
					RootPath:         filepath.Dir(filepath.Dir(path)),
					ManifestPath:     path,
					DiscoverySource:  root.source,
					QuarantineReason: err.Error(),
				})
				return nil
			}
			discovered = append(discovered, item)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk plugin root %s: %w", rootPath, err)
		}
	}

	sort.SliceStable(discovered, func(i, j int) bool {
		if discovered[i].Category == discovered[j].Category {
			return discovered[i].PluginID < discovered[j].PluginID
		}
		return discovered[i].Category < discovered[j].Category
	})
	return discovered, nil
}

func loadPlugin(manifestPath, source string) (Plugin, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Plugin{}, fmt.Errorf("read plugin manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Plugin{}, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return Plugin{}, fmt.Errorf("plugin id is required")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return Plugin{}, fmt.Errorf("plugin name is required")
	}
	if strings.TrimSpace(manifest.Category) == "" {
		return Plugin{}, fmt.Errorf("plugin category is required")
	}
	if len(manifest.ConfigSchema) == 0 {
		return Plugin{}, fmt.Errorf("config schema is required")
	}

	rootPath := filepath.Dir(filepath.Dir(manifestPath))
	trustLevel := "trusted"
	if source == "profile" {
		trustLevel = "local"
	}
	if manifest.Trusted != nil && !*manifest.Trusted {
		trustLevel = "untrusted"
	}

	state := "enabled"
	quarantineReason := ""
	if trustLevel == "untrusted" {
		state = "quarantined"
		quarantineReason = "plugin manifest is not trusted"
	}
	if manifest.EnableByDefault != nil && !*manifest.EnableByDefault && state != "quarantined" {
		state = "disabled"
	}

	return Plugin{
		PluginID:         strings.TrimSpace(manifest.ID),
		Name:             strings.TrimSpace(manifest.Name),
		Version:          strings.TrimSpace(manifest.Version),
		Category:         strings.TrimSpace(manifest.Category),
		Description:      strings.TrimSpace(manifest.Description),
		State:            state,
		TrustLevel:       trustLevel,
		RootPath:         rootPath,
		ManifestPath:     manifestPath,
		DiscoverySource:  source,
		QuarantineReason: quarantineReason,
		ConfigSchema:     manifest.ConfigSchema,
		Metadata: map[string]any{
			"entry_point": manifest.EntryPoint,
		},
	}, nil
}

func (s *Service) findOrCreateRecord(pluginID string) (*core.Record, error) {
	record, err := s.app.FindFirstRecordByFilter(CollectionPlugins, "plugin_id = {:plugin_id}", dbx.Params{"plugin_id": pluginID})
	if err == nil && record != nil {
		return record, nil
	}

	collection, findErr := s.app.FindCollectionByNameOrId(CollectionPlugins)
	if findErr != nil {
		return nil, fmt.Errorf("find plugins collection: %w", findErr)
	}
	record = core.NewRecord(collection)
	record.Set("plugin_id", pluginID)
	return record, nil
}

func pluginFromRecord(record *core.Record) (Plugin, error) {
	item := Plugin{
		ID:               record.Id,
		PluginID:         record.GetString("plugin_id"),
		Name:             record.GetString("name"),
		Version:          record.GetString("version"),
		Category:         record.GetString("category"),
		Description:      record.GetString("description"),
		State:            record.GetString("state"),
		TrustLevel:       record.GetString("trust_level"),
		RootPath:         record.GetString("root_path"),
		ManifestPath:     record.GetString("manifest_path"),
		DiscoverySource:  record.GetString("discovery_source"),
		QuarantineReason: record.GetString("quarantine_reason"),
		CreatedAt:        record.GetDateTime("created").Time(),
		UpdatedAt:        record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "category_contract_json", &item.CategoryContract); err != nil {
		return Plugin{}, err
	}
	if err := decodeJSONField(record, "config_schema_json", &item.ConfigSchema); err != nil {
		return Plugin{}, err
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Plugin{}, err
	}
	return item, nil
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
