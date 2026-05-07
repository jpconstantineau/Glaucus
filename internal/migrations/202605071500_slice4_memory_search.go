package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		sessions, err := app.FindCollectionByNameOrId("agent_sessions")
		if err != nil {
			return err
		}
		if sessions.Fields.GetByName("todo_json") == nil {
			sessions.Fields.Add(&core.JSONField{Name: "todo_json"})
		}
		if err := app.Save(sessions); err != nil {
			return err
		}

		if _, err := app.FindCollectionByNameOrId("memory_documents"); err != nil {
			col := core.NewBaseCollection("memory_documents")
			col.Fields.Add(
				&core.TextField{Name: "profile_id", Required: true},
				&core.TextField{Name: "slug", Required: true},
				&core.TextField{Name: "title", Required: true},
				&core.TextField{Name: "path", Required: true},
				&core.TextField{Name: "checksum"},
			)
			col.AddIndex("idx_memory_documents_profile_slug", true, "profile_id,slug", "")
			col.AddIndex("idx_memory_documents_profile_path", true, "profile_id,path", "")
			if err := app.Save(col); err != nil {
				return err
			}
		}

		if _, err := app.NonconcurrentDB().NewQuery(`
			CREATE VIRTUAL TABLE IF NOT EXISTS session_search_fts
			USING fts5(profile_id, session_id, message_id, title, body, visible_text)
		`).Execute(); err != nil {
			return err
		}

		return nil
	}, func(app core.App) error {
		if app.HasTable("session_search_fts") {
			if err := app.DeleteTable("session_search_fts"); err != nil {
				return err
			}
		}

		if collection, err := app.FindCollectionByNameOrId("memory_documents"); err == nil {
			if err := app.Delete(collection); err != nil {
				return err
			}
		}

		sessions, err := app.FindCollectionByNameOrId("agent_sessions")
		if err != nil {
			return err
		}
		if field := sessions.Fields.GetByName("todo_json"); field != nil {
			sessions.Fields.RemoveByName(field.GetName())
		}
		return app.Save(sessions)
	})
}
