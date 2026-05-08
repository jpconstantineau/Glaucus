package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		if _, err := app.FindCollectionByNameOrId("plugins"); err != nil {
			col := core.NewBaseCollection("plugins")
			col.Fields.Add(
				&core.TextField{Name: "plugin_id", Required: true},
				&core.TextField{Name: "name", Required: true},
				&core.TextField{Name: "version"},
				&core.TextField{Name: "category", Required: true},
				&core.TextField{Name: "description"},
				&core.TextField{Name: "state", Required: true},
				&core.TextField{Name: "trust_level", Required: true},
				&core.TextField{Name: "root_path", Required: true},
				&core.TextField{Name: "manifest_path", Required: true},
				&core.TextField{Name: "discovery_source", Required: true},
				&core.TextField{Name: "quarantine_reason"},
				&core.JSONField{Name: "category_contract_json"},
				&core.JSONField{Name: "config_schema_json"},
				&core.JSONField{Name: "metadata_json"},
			)
			col.AddIndex("idx_plugins_plugin_id", true, "plugin_id", "")
			col.AddIndex("idx_plugins_category_state", false, "category,state", "")
			if err := app.Save(col); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		if collection, err := app.FindCollectionByNameOrId("plugins"); err == nil {
			return app.Delete(collection)
		}
		return nil
	})
}
