package features

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionContracts = "feature_contracts"

type Contract struct {
	ID                string         `json:"id"`
	FeatureID         string         `json:"feature_id"`
	DisplayName       string         `json:"display_name"`
	State             string         `json:"state"`
	Gate              string         `json:"gate"`
	Description       string         `json:"description"`
	StorageContracts  []string       `json:"storage_contracts,omitempty"`
	OperatorSurfaces  []string       `json:"operator_surfaces,omitempty"`
	ExportCoverage    []string       `json:"export_coverage,omitempty"`
	MigrationCoverage []string       `json:"migration_coverage,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func DefaultContracts() []Contract {
	return []Contract{
		{
			FeatureID:         "persistent_goals",
			DisplayName:       "Persistent Goals",
			State:             "gated",
			Gate:              "slice:9-goals",
			Description:       "Session and profile goals stay disabled until slice 9 ships the durable storage and lifecycle APIs.",
			StorageContracts:  []string{"session_goals", "profile_goals"},
			OperatorSurfaces:  []string{"dashboard", "api", "runtime"},
			ExportCoverage:    []string{"goal state", "goal evaluations"},
			MigrationCoverage: []string{"future PocketBase migrations must preserve goal state"},
		},
		{
			FeatureID:         "kanban_queue",
			DisplayName:       "Kanban and Multi-Agent Queue",
			State:             "gated",
			Gate:              "slice:8-kanban",
			Description:       "Kanban queues stay behind a stable contract until slice 8 delivers boards, tasks, and delegated run orchestration.",
			StorageContracts:  []string{"kanban_boards", "kanban_tasks", "kanban_comments"},
			OperatorSurfaces:  []string{"dashboard", "runtime"},
			ExportCoverage:    []string{"task metadata", "task-to-run links"},
			MigrationCoverage: []string{"kanban collections must be migration-safe before enablement"},
		},
		{
			FeatureID:         "batch_processing",
			DisplayName:       "Batch Processing and Trajectories",
			State:             "gated",
			Gate:              "slice:10-batch",
			Description:       "Batch launch and trajectory export stay disabled until resumable execution and machine-readable output contracts are implemented.",
			StorageContracts:  []string{"batch_jobs", "batch_attempts", "trajectory_exports"},
			OperatorSurfaces:  []string{"dashboard", "api", "cli"},
			ExportCoverage:    []string{"jsonl trajectories", "batch result manifests"},
			MigrationCoverage: []string{"batch resumes must survive migrations"},
		},
		{
			FeatureID:         "dashboard_themes",
			DisplayName:       "Themes and Personalization",
			State:             "gated",
			Gate:              "config_only",
			Description:       "Named themes and branding stay behind configuration contracts until operator-facing customization is implemented.",
			StorageContracts:  []string{"profile presentation settings"},
			OperatorSurfaces:  []string{"dashboard", "cli"},
			ExportCoverage:    []string{"theme tokens", "branding text"},
			MigrationCoverage: []string{"theme settings must remain forward compatible"},
		},
		{
			FeatureID:         "backup_migration_coverage",
			DisplayName:       "Backup, Export, and Migration Coverage",
			State:             "guarded",
			Gate:              "explicit_secret_opt_in",
			Description:       "Backups remain restricted to safe metadata and artifacts until future slices add explicit secret export permissions and full migration coverage.",
			StorageContracts:  []string{"PocketBase records", "markdown memories", "skills artifacts", "profile metadata"},
			OperatorSurfaces:  []string{"dashboard", "cli", "exports"},
			ExportCoverage:    []string{"safe config metadata", "durable records", "artifact manifests"},
			MigrationCoverage: []string{"secret material remains excluded without explicit opt-in"},
		},
	}
}

func (s *Service) Reconcile(ctx context.Context) error {
	if s == nil || s.app == nil {
		return nil
	}
	for _, contract := range DefaultContracts() {
		record, err := s.findOrCreateRecord(contract.FeatureID)
		if err != nil {
			return err
		}
		record.Set("feature_id", contract.FeatureID)
		record.Set("display_name", contract.DisplayName)
		record.Set("state", contract.State)
		record.Set("gate", contract.Gate)
		record.Set("description", contract.Description)
		if err := setJSON(record, "storage_contracts_json", contract.StorageContracts); err != nil {
			return err
		}
		if err := setJSON(record, "operator_surfaces_json", contract.OperatorSurfaces); err != nil {
			return err
		}
		if err := setJSON(record, "export_coverage_json", contract.ExportCoverage); err != nil {
			return err
		}
		if err := setJSON(record, "migration_coverage_json", contract.MigrationCoverage); err != nil {
			return err
		}
		if err := setJSON(record, "metadata_json", contract.Metadata); err != nil {
			return err
		}
		if err := s.app.SaveWithContext(ctx, record); err != nil {
			return fmt.Errorf("save feature contract %s: %w", contract.FeatureID, err)
		}
	}
	return nil
}

func (s *Service) ListContracts(ctx context.Context, limit int) ([]Contract, error) {
	if s == nil || s.app == nil {
		return nil, nil
	}
	records, err := s.app.FindRecordsByFilter(CollectionContracts, "id != ''", "feature_id", limit, 0)
	if err != nil {
		return nil, fmt.Errorf("list feature contracts: %w", err)
	}
	result := make([]Contract, 0, len(records))
	for _, record := range records {
		item, err := contractFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	_ = ctx
	return result, nil
}

func (s *Service) findOrCreateRecord(featureID string) (*core.Record, error) {
	record, err := s.app.FindFirstRecordByFilter(CollectionContracts, "feature_id = {:feature_id}", dbx.Params{"feature_id": featureID})
	if err == nil && record != nil {
		return record, nil
	}

	collection, findErr := s.app.FindCollectionByNameOrId(CollectionContracts)
	if findErr != nil {
		return nil, fmt.Errorf("find feature contracts collection: %w", findErr)
	}
	record = core.NewRecord(collection)
	record.Set("feature_id", featureID)
	return record, nil
}

func contractFromRecord(record *core.Record) (Contract, error) {
	item := Contract{
		ID:          record.Id,
		FeatureID:   record.GetString("feature_id"),
		DisplayName: record.GetString("display_name"),
		State:       record.GetString("state"),
		Gate:        record.GetString("gate"),
		Description: record.GetString("description"),
		CreatedAt:   record.GetDateTime("created").Time(),
		UpdatedAt:   record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "storage_contracts_json", &item.StorageContracts); err != nil {
		return Contract{}, err
	}
	if err := decodeJSONField(record, "operator_surfaces_json", &item.OperatorSurfaces); err != nil {
		return Contract{}, err
	}
	if err := decodeJSONField(record, "export_coverage_json", &item.ExportCoverage); err != nil {
		return Contract{}, err
	}
	if err := decodeJSONField(record, "migration_coverage_json", &item.MigrationCoverage); err != nil {
		return Contract{}, err
	}
	if err := decodeJSONField(record, "metadata_json", &item.Metadata); err != nil {
		return Contract{}, err
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
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}
