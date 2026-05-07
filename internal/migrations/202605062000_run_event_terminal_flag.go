package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("agent_run_events")
		if err != nil {
			return err
		}

		if collection.Fields.GetByName("is_terminal") == nil {
			collection.Fields.Add(&core.BoolField{Name: "is_terminal"})
		}

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("agent_run_events")
		if err != nil {
			return err
		}

		if field := collection.Fields.GetByName("is_terminal"); field != nil {
			collection.Fields.RemoveByName(field.GetName())
		}

		return app.Save(collection)
	})
}
