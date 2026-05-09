package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		if _, err := app.FindCollectionByNameOrId("feature_contracts"); err != nil {
			col := core.NewBaseCollection("feature_contracts")
			col.Fields.Add(
				&core.TextField{Name: "feature_id", Required: true},
				&core.TextField{Name: "display_name", Required: true},
				&core.TextField{Name: "state", Required: true},
				&core.TextField{Name: "gate", Required: true},
				&core.TextField{Name: "description", Required: true},
				&core.JSONField{Name: "storage_contracts_json"},
				&core.JSONField{Name: "operator_surfaces_json"},
				&core.JSONField{Name: "export_coverage_json"},
				&core.JSONField{Name: "migration_coverage_json"},
				&core.JSONField{Name: "metadata_json"},
			)
			col.AddIndex("idx_feature_contracts_feature_id", true, "feature_id", "")
			if err := app.Save(col); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		if collection, err := app.FindCollectionByNameOrId("feature_contracts"); err == nil {
			return app.Delete(collection)
		}
		return nil
	})
}
