package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		if _, err := app.FindCollectionByNameOrId("mcp_servers"); err != nil {
			col := core.NewBaseCollection("mcp_servers")
			col.Fields.Add(
				&core.TextField{Name: "name", Required: true},
				&core.TextField{Name: "command"},
				&core.JSONField{Name: "args_json"},
				&core.TextField{Name: "status", Required: true},
				&core.TextField{Name: "health_reason"},
				&core.JSONField{Name: "advertised_tools_json"},
				&core.JSONField{Name: "exposed_tools_json"},
				&core.JSONField{Name: "denied_tools_json"},
			)
			col.AddIndex("idx_mcp_servers_name", true, "name", "")
			if err := app.Save(col); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		if collection, err := app.FindCollectionByNameOrId("mcp_servers"); err == nil {
			return app.Delete(collection)
		}
		return nil
	})
}
