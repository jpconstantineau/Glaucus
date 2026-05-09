package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		for _, collection := range []*core.Collection{
			newBatchJobsCollection(),
			newBatchAttemptsCollection(),
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
		for _, name := range []string{"batch_attempts", "batch_jobs"} {
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

func newBatchJobsCollection() *core.Collection {
	col := core.NewBaseCollection("batch_jobs")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "schema_version", Required: true},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "provider_id"},
		&core.TextField{Name: "model_id"},
		&core.TextField{Name: "toolset"},
		&core.TextField{Name: "working_directory"},
		&core.NumberField{Name: "item_count", OnlyInt: true},
		&core.NumberField{Name: "completed_count", OnlyInt: true},
		&core.NumberField{Name: "failed_count", OnlyInt: true},
		&core.TextField{Name: "created_by"},
		&core.TextField{Name: "export_path"},
		&core.JSONField{Name: "metadata_json"},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "ended_at"},
	)
	col.AddIndex("idx_batch_jobs_profile_status", false, "profile_id,status", "")
	col.AddIndex("idx_batch_jobs_profile_name", false, "profile_id,name", "")
	return col
}

func newBatchAttemptsCollection() *core.Collection {
	col := core.NewBaseCollection("batch_attempts")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "job_id", Required: true},
		&core.TextField{Name: "item_id", Required: true},
		&core.NumberField{Name: "item_index", Required: true, OnlyInt: true},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "prompt", Required: true},
		&core.JSONField{Name: "metadata_json"},
		&core.TextField{Name: "session_id"},
		&core.TextField{Name: "run_id"},
		&core.TextField{Name: "output_text"},
		&core.JSONField{Name: "usage_json"},
		&core.TextField{Name: "error_message"},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "ended_at"},
	)
	col.AddIndex("idx_batch_attempts_job_item", true, "job_id,item_index", "")
	col.AddIndex("idx_batch_attempts_job_status", false, "job_id,status", "")
	col.AddIndex("idx_batch_attempts_run", false, "run_id", "")
	return col
}
