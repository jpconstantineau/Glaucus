package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		for _, collection := range []*core.Collection{
			newKanbanBoardsCollection(),
			newKanbanTasksCollection(),
			newKanbanCommentsCollection(),
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
		for _, name := range []string{"kanban_comments", "kanban_tasks", "kanban_boards"} {
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

func newKanbanBoardsCollection() *core.Collection {
	col := core.NewBaseCollection("kanban_boards")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "slug", Required: true},
		&core.TextField{Name: "description"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "owner"},
		&core.NumberField{Name: "wip_limit", OnlyInt: true},
		&core.JSONField{Name: "metadata_json"},
	)
	col.AddIndex("idx_kanban_boards_profile_slug", true, "profile_id,slug", "")
	col.AddIndex("idx_kanban_boards_profile_status", false, "profile_id,status", "")
	return col
}

func newKanbanTasksCollection() *core.Collection {
	col := core.NewBaseCollection("kanban_tasks")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "board_id", Required: true},
		&core.TextField{Name: "parent_task_id"},
		&core.TextField{Name: "title", Required: true},
		&core.TextField{Name: "description"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "queue_state", Required: true},
		&core.TextField{Name: "priority", Required: true},
		&core.NumberField{Name: "position", OnlyInt: true},
		&core.TextField{Name: "owner"},
		&core.TextField{Name: "assignee"},
		&core.TextField{Name: "session_id"},
		&core.TextField{Name: "parent_run_id"},
		&core.TextField{Name: "latest_run_id"},
		&core.TextField{Name: "delegation_prompt"},
		&core.TextField{Name: "last_error"},
		&core.NumberField{Name: "retry_count", OnlyInt: true},
		&core.JSONField{Name: "metadata_json"},
		&core.DateField{Name: "due_at"},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "completed_at"},
	)
	col.AddIndex("idx_kanban_tasks_board_position", false, "board_id,position", "")
	col.AddIndex("idx_kanban_tasks_profile_queue", false, "profile_id,queue_state", "")
	col.AddIndex("idx_kanban_tasks_latest_run", false, "latest_run_id", "")
	col.AddIndex("idx_kanban_tasks_session", false, "session_id", "")
	return col
}

func newKanbanCommentsCollection() *core.Collection {
	col := core.NewBaseCollection("kanban_comments")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "board_id", Required: true},
		&core.TextField{Name: "task_id", Required: true},
		&core.TextField{Name: "run_id"},
		&core.TextField{Name: "author", Required: true},
		&core.TextField{Name: "kind", Required: true},
		&core.TextField{Name: "body", Required: true},
		&core.JSONField{Name: "metadata_json"},
	)
	col.AddIndex("idx_kanban_comments_task_kind", false, "task_id,kind", "")
	col.AddIndex("idx_kanban_comments_run", false, "run_id", "")
	return col
}
