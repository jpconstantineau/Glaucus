package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		for _, collection := range []*core.Collection{
			newSessionGoalsCollection(),
			newProfileGoalsCollection(),
		} {
			if _, err := app.FindCollectionByNameOrId(collection.Name); err == nil {
				continue
			}
			if err := app.Save(collection); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, name := range []string{"session_goals", "profile_goals"} {
			existing, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(existing); err != nil {
				return err
			}
		}
		return nil
	})
}

func newSessionGoalsCollection() *core.Collection {
	col := core.NewBaseCollection("session_goals")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "session_id", Required: true},
		&core.TextField{Name: "title", Required: true},
		&core.TextField{Name: "statement", Required: true},
		&core.TextField{Name: "success_criteria"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "priority", Required: true},
		&core.TextField{Name: "created_by_run_id"},
		&core.TextField{Name: "updated_by_run_id"},
		&core.TextField{Name: "cleared_by_run_id"},
		&core.TextField{Name: "last_evaluated_run_id"},
		&core.NumberField{Name: "version", Required: true, OnlyInt: true},
		&core.JSONField{Name: "tags_json"},
		&core.JSONField{Name: "state_json"},
		&core.JSONField{Name: "metadata_json"},
		&core.JSONField{Name: "last_evaluation_json"},
		&core.JSONField{Name: "evaluation_history_json"},
		&core.DateField{Name: "cleared_at"},
		&core.DateField{Name: "last_evaluated_at"},
	)
	col.AddIndex("idx_session_goals_profile_session_status", false, "profile_id,session_id,status", "")
	col.AddIndex("idx_session_goals_session_priority", false, "session_id,priority", "")
	col.AddIndex("idx_session_goals_last_eval_run", false, "last_evaluated_run_id", "")
	return col
}

func newProfileGoalsCollection() *core.Collection {
	col := core.NewBaseCollection("profile_goals")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "session_id"},
		&core.TextField{Name: "title", Required: true},
		&core.TextField{Name: "statement", Required: true},
		&core.TextField{Name: "success_criteria"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "priority", Required: true},
		&core.TextField{Name: "created_by_run_id"},
		&core.TextField{Name: "updated_by_run_id"},
		&core.TextField{Name: "cleared_by_run_id"},
		&core.TextField{Name: "last_evaluated_run_id"},
		&core.NumberField{Name: "version", Required: true, OnlyInt: true},
		&core.JSONField{Name: "tags_json"},
		&core.JSONField{Name: "state_json"},
		&core.JSONField{Name: "metadata_json"},
		&core.JSONField{Name: "last_evaluation_json"},
		&core.JSONField{Name: "evaluation_history_json"},
		&core.DateField{Name: "cleared_at"},
		&core.DateField{Name: "last_evaluated_at"},
	)
	col.AddIndex("idx_profile_goals_profile_status", false, "profile_id,status", "")
	col.AddIndex("idx_profile_goals_profile_priority", false, "profile_id,priority", "")
	col.AddIndex("idx_profile_goals_last_eval_run", false, "last_evaluated_run_id", "")
	return col
}
