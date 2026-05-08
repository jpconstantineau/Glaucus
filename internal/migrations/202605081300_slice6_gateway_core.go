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
		if sessions.Fields.GetByName("session_key") == nil {
			sessions.Fields.Add(&core.TextField{Name: "session_key"})
		}
		if sessions.Fields.GetByName("metadata_json") == nil {
			sessions.Fields.Add(&core.JSONField{Name: "metadata_json"})
		}
		sessions.AddIndex("idx_agent_sessions_profile_session_key", true, "profile_id,session_key", "session_key != ''")
		if err := app.Save(sessions); err != nil {
			return err
		}

		adapters, err := app.FindCollectionByNameOrId("platform_adapters")
		if err != nil {
			return err
		}
		if adapters.Fields.GetByName("auth_mode") == nil {
			adapters.Fields.Add(&core.TextField{Name: "auth_mode"})
		}
		if adapters.Fields.GetByName("allowlist_json") == nil {
			adapters.Fields.Add(&core.JSONField{Name: "allowlist_json"})
		}
		if adapters.Fields.GetByName("capabilities_json") == nil {
			adapters.Fields.Add(&core.JSONField{Name: "capabilities_json"})
		}
		if adapters.Fields.GetByName("metadata_json") == nil {
			adapters.Fields.Add(&core.JSONField{Name: "metadata_json"})
		}
		if err := app.Save(adapters); err != nil {
			return err
		}

		if _, err := app.FindCollectionByNameOrId("platform_adapter_logs"); err == nil {
			return nil
		}
		return app.Save(newPlatformAdapterLogsCollection())
	}, func(app core.App) error {
		if logs, err := app.FindCollectionByNameOrId("platform_adapter_logs"); err == nil {
			if err := app.Delete(logs); err != nil {
				return err
			}
		}

		adapters, err := app.FindCollectionByNameOrId("platform_adapters")
		if err != nil {
			return err
		}
		for _, name := range []string{"auth_mode", "allowlist_json", "capabilities_json", "metadata_json"} {
			if field := adapters.Fields.GetByName(name); field != nil {
				adapters.Fields.RemoveByName(field.GetName())
			}
		}
		if err := app.Save(adapters); err != nil {
			return err
		}

		sessions, err := app.FindCollectionByNameOrId("agent_sessions")
		if err != nil {
			return err
		}
		for _, name := range []string{"session_key", "metadata_json"} {
			if field := sessions.Fields.GetByName(name); field != nil {
				sessions.Fields.RemoveByName(field.GetName())
			}
		}
		return app.Save(sessions)
	})
}

func newPlatformAdapterLogsCollection() *core.Collection {
	col := core.NewBaseCollection("platform_adapter_logs")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "adapter_id", Required: true},
		&core.TextField{Name: "platform", Required: true},
		&core.TextField{Name: "direction", Required: true},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "session_key"},
		&core.TextField{Name: "chat_id"},
		&core.TextField{Name: "thread_id"},
		&core.TextField{Name: "external_message_id"},
		&core.TextField{Name: "summary"},
		&core.TextField{Name: "error_message"},
		&core.JSONField{Name: "payload_json"},
	)
	col.AddIndex("idx_platform_adapter_logs_adapter_status", false, "adapter_id,status", "")
	col.AddIndex("idx_platform_adapter_logs_profile_platform", false, "profile_id,platform", "")
	return col
}
