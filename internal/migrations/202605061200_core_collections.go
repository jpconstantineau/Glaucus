package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	pbmigrations "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(func(app core.App) error {
		for _, collection := range buildCollections() {
			if err := app.Save(collection); err != nil {
				return err
			}
		}

		return nil
	}, func(app core.App) error {
		collections := buildCollections()
		for i := len(collections) - 1; i >= 0; i-- {
			existing, err := app.FindCollectionByNameOrId(collections[i].Name)
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

func buildCollections() []*core.Collection {
	return []*core.Collection{
		newProfilesCollection(),
		newOperatorsCollection(),
		newAgentSessionsCollection(),
		newAgentMessagesCollection(),
		newAgentRunsCollection(),
		newAgentRunEventsCollection(),
		newApprovalRequestsCollection(),
		newCronJobsCollection(),
		newCronJobRunsCollection(),
		newSkillsCollection(),
		newToolsetsCollection(),
		newProviderProfilesCollection(),
		newPlatformAdaptersCollection(),
		newPlatformAdapterLogsCollection(),
		newBackgroundProcessesCollection(),
		newExportsCollection(),
	}
}

func newProfilesCollection() *core.Collection {
	col := core.NewBaseCollection("profiles")
	col.Fields.Add(
		&core.TextField{Name: "slug", Required: true, Presentable: true},
		&core.TextField{Name: "display_name", Required: true},
		&core.BoolField{Name: "is_default"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "config_path", Required: true},
		&core.TextField{Name: "root_path", Required: true},
	)
	col.AddIndex("idx_profiles_slug_unique", true, "slug", "")
	col.AddIndex("idx_profiles_status", false, "status", "")
	return col
}

func newOperatorsCollection() *core.Collection {
	col := core.NewAuthCollection("operators")
	col.Fields.Add(
		&core.TextField{Name: "display_name", Required: true},
		&core.TextField{Name: "profile_slug"},
	)
	col.AddIndex("idx_operators_profile_slug", false, "profile_slug", "")
	return col
}

func newAgentSessionsCollection() *core.Collection {
	col := core.NewBaseCollection("agent_sessions")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "source", Required: true},
		&core.TextField{Name: "title", Required: true},
		&core.TextField{Name: "parent_session_id"},
		&core.TextField{Name: "status", Required: true},
		&core.JSONField{Name: "model_snapshot_json"},
		&core.JSONField{Name: "toolset_snapshot_json"},
		&core.DateField{Name: "last_message_at"},
	)
	col.AddIndex("idx_agent_sessions_profile_last_message", false, "profile_id,last_message_at", "")
	col.AddIndex("idx_agent_sessions_status", false, "status", "")
	return col
}

func newAgentMessagesCollection() *core.Collection {
	col := core.NewBaseCollection("agent_messages")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "session_id", Required: true},
		&core.TextField{Name: "run_id"},
		&core.TextField{Name: "role", Required: true},
		&core.NumberField{Name: "ordinal", Required: true, OnlyInt: true},
		&core.JSONField{Name: "content_json", Required: true},
		&core.TextField{Name: "visible_text"},
		&core.JSONField{Name: "tool_calls_json"},
		&core.JSONField{Name: "tool_results_json"},
		&core.JSONField{Name: "usage_json"},
	)
	col.AddIndex("idx_agent_messages_session_ordinal", true, "session_id,ordinal", "")
	col.AddIndex("idx_agent_messages_run", false, "run_id", "")
	return col
}

func newAgentRunsCollection() *core.Collection {
	col := core.NewBaseCollection("agent_runs")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "session_id", Required: true},
		&core.TextField{Name: "parent_run_id"},
		&core.TextField{Name: "trigger_source", Required: true},
		&core.TextField{Name: "status", Required: true},
		&core.JSONField{Name: "request_json"},
		&core.JSONField{Name: "provider_resolution_json"},
		&core.TextField{Name: "working_directory"},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "ended_at"},
		&core.TextField{Name: "error_code"},
		&core.TextField{Name: "error_message"},
	)
	col.AddIndex("idx_agent_runs_session_started", false, "session_id,started_at", "")
	col.AddIndex("idx_agent_runs_profile_status", false, "profile_id,status", "")
	return col
}

func newAgentRunEventsCollection() *core.Collection {
	col := core.NewBaseCollection("agent_run_events")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "run_id", Required: true},
		&core.TextField{Name: "session_id", Required: true},
		&core.NumberField{Name: "sequence", Required: true, OnlyInt: true},
		&core.TextField{Name: "event_type", Required: true},
		&core.JSONField{Name: "payload_json", Required: true},
	)
	col.AddIndex("idx_agent_run_events_run_sequence", true, "run_id,sequence", "")
	col.AddIndex("idx_agent_run_events_session", false, "session_id", "")
	return col
}

func newApprovalRequestsCollection() *core.Collection {
	col := core.NewBaseCollection("approval_requests")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "run_id", Required: true},
		&core.TextField{Name: "tool_name", Required: true},
		&core.JSONField{Name: "request_json", Required: true},
		&core.TextField{Name: "decision"},
		&core.TextField{Name: "decided_by"},
		&core.DateField{Name: "decided_at"},
		&core.TextField{Name: "scope"},
	)
	col.AddIndex("idx_approval_requests_run", false, "run_id", "")
	col.AddIndex("idx_approval_requests_decision", false, "decision", "")
	return col
}

func newCronJobsCollection() *core.Collection {
	col := core.NewBaseCollection("cron_jobs")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "prompt", Required: true},
		&core.TextField{Name: "schedule_kind", Required: true},
		&core.TextField{Name: "schedule_value", Required: true},
		&core.TextField{Name: "timezone", Required: true},
		&core.BoolField{Name: "enabled"},
		&core.JSONField{Name: "delivery_target_json"},
		&core.JSONField{Name: "toolset_overrides_json"},
		&core.JSONField{Name: "provider_overrides_json"},
		&core.TextField{Name: "cwd"},
		&core.DateField{Name: "next_run_at"},
		&core.DateField{Name: "last_run_at"},
	)
	col.AddIndex("idx_cron_jobs_profile_next_run", false, "profile_id,next_run_at", "")
	col.AddIndex("idx_cron_jobs_enabled", false, "enabled", "")
	return col
}

func newCronJobRunsCollection() *core.Collection {
	col := core.NewBaseCollection("cron_job_runs")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "job_id", Required: true},
		&core.TextField{Name: "run_id"},
		&core.TextField{Name: "status", Required: true},
		&core.DateField{Name: "scheduled_for"},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "ended_at"},
		&core.TextField{Name: "output_excerpt"},
		&core.TextField{Name: "error_message"},
	)
	col.AddIndex("idx_cron_job_runs_job_started", false, "job_id,started_at", "")
	col.AddIndex("idx_cron_job_runs_run", false, "run_id", "")
	return col
}

func newSkillsCollection() *core.Collection {
	col := core.NewBaseCollection("skills")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "slug", Required: true},
		&core.TextField{Name: "version"},
		&core.TextField{Name: "description"},
		&core.TextField{Name: "state", Required: true},
		&core.TextField{Name: "trust_level", Required: true},
		&core.JSONField{Name: "provenance_json"},
		&core.TextField{Name: "root_path", Required: true},
		&core.TextField{Name: "entry_file", Required: true},
	)
	col.AddIndex("idx_skills_profile_slug", true, "profile_id,slug", "")
	col.AddIndex("idx_skills_state", false, "state", "")
	return col
}

func newToolsetsCollection() *core.Collection {
	col := core.NewBaseCollection("toolsets")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "name", Required: true},
		&core.TextField{Name: "description"},
		&core.JSONField{Name: "enabled_tools_json"},
		&core.JSONField{Name: "disabled_tools_json"},
	)
	col.AddIndex("idx_toolsets_profile_name", true, "profile_id,name", "")
	return col
}

func newProviderProfilesCollection() *core.Collection {
	col := core.NewBaseCollection("provider_profiles")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "provider_id", Required: true},
		&core.TextField{Name: "model_id", Required: true},
		&core.TextField{Name: "lifecycle_status"},
		&core.TextField{Name: "base_url"},
		&core.JSONField{Name: "capabilities_json"},
		&core.JSONField{Name: "limits_json"},
		&core.JSONField{Name: "headers_json"},
	)
	col.AddIndex("idx_provider_profiles_profile_provider_model", true, "profile_id,provider_id,model_id", "")
	return col
}

func newPlatformAdaptersCollection() *core.Collection {
	col := core.NewBaseCollection("platform_adapters")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "platform", Required: true},
		&core.BoolField{Name: "enabled"},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "auth_mode"},
		&core.JSONField{Name: "config_json"},
		&core.JSONField{Name: "allowlist_json"},
		&core.JSONField{Name: "capabilities_json"},
		&core.JSONField{Name: "metadata_json"},
		&core.DateField{Name: "last_connected_at"},
		&core.TextField{Name: "last_error"},
	)
	col.AddIndex("idx_platform_adapters_profile_platform", true, "profile_id,platform", "")
	return col
}

func newBackgroundProcessesCollection() *core.Collection {
	col := core.NewBaseCollection("background_processes")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "session_id"},
		&core.TextField{Name: "run_id"},
		&core.TextField{Name: "handle", Required: true},
		&core.TextField{Name: "command", Required: true},
		&core.TextField{Name: "cwd"},
		&core.TextField{Name: "status", Required: true},
		&core.DateField{Name: "started_at"},
		&core.DateField{Name: "ended_at"},
		&core.NumberField{Name: "exit_code", OnlyInt: true},
	)
	col.AddIndex("idx_background_processes_profile_status", false, "profile_id,status", "")
	col.AddIndex("idx_background_processes_run", false, "run_id", "")
	return col
}

func newExportsCollection() *core.Collection {
	col := core.NewBaseCollection("exports")
	col.Fields.Add(
		&core.TextField{Name: "profile_id", Required: true},
		&core.TextField{Name: "kind", Required: true},
		&core.TextField{Name: "format", Required: true},
		&core.TextField{Name: "path", Required: true},
		&core.TextField{Name: "status", Required: true},
		&core.TextField{Name: "created_by"},
	)
	col.AddIndex("idx_exports_profile_kind_status", false, "profile_id,kind,status", "")
	return col
}
