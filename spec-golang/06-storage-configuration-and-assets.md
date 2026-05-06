# 06. Storage, Configuration, and Assets

## 1. Persistent Storage

### 1.1 PocketBase Collections

Required collections:

- `profiles`
- `agent_sessions`
- `agent_messages`
- `agent_runs`
- `agent_run_events`
- `approval_requests`
- `cron_jobs`
- `cron_job_runs`
- `skills`
- `toolsets`
- `provider_profiles` or runtime cache equivalent
- `platform_adapters`
- `background_processes`
- `exports`

Minimum field requirements:

- `profiles`: `slug`, `display_name`, `is_default`, `status`, `config_path`, `root_path`
- `agent_sessions`: `profile_id`, `source`, `title`, `parent_session_id`, `status`, `model_snapshot_json`, `toolset_snapshot_json`, `last_message_at`
- `agent_messages`: `profile_id`, `session_id`, `run_id`, `role`, `ordinal`, `content_json`, `visible_text`, `tool_calls_json`, `tool_results_json`, `usage_json`
- `agent_runs`: `profile_id`, `session_id`, `parent_run_id`, `trigger_source`, `status`, `request_json`, `provider_resolution_json`, `working_directory`, `started_at`, `ended_at`, `error_code`, `error_message`
- `agent_run_events`: `profile_id`, `run_id`, `session_id`, `sequence`, `event_type`, `payload_json`, `created`
- `approval_requests`: `profile_id`, `run_id`, `tool_name`, `request_json`, `decision`, `decided_by`, `decided_at`, `scope`
- `cron_jobs`: `profile_id`, `name`, `prompt`, `schedule_kind`, `schedule_value`, `timezone`, `enabled`, `delivery_target_json`, `toolset_overrides_json`, `provider_overrides_json`, `cwd`, `next_run_at`, `last_run_at`
- `cron_job_runs`: `profile_id`, `job_id`, `run_id`, `status`, `scheduled_for`, `started_at`, `ended_at`, `output_excerpt`, `error_message`
- `skills`: `profile_id`, `name`, `slug`, `version`, `description`, `state`, `trust_level`, `provenance_json`, `root_path`, `entry_file`
- `toolsets`: `profile_id`, `name`, `description`, `enabled_tools_json`, `disabled_tools_json`
- `platform_adapters`: `profile_id`, `platform`, `enabled`, `status`, `config_json`, `last_connected_at`, `last_error`
- `background_processes`: `profile_id`, `session_id`, `run_id`, `handle`, `command`, `cwd`, `status`, `started_at`, `ended_at`, `exit_code`
- `exports`: `profile_id`, `kind`, `format`, `path`, `status`, `created_by`, `created`

Optional but recommended:

- `memory_documents`
- `usage_rollups`
- `gateway_deliveries`
- `mcp_servers`
- `plugin_registry`

Rules:

- use PocketBase migrations for schema creation and evolution
- enable WAL-friendly SQLite settings supported by PocketBase
- create FTS indexes for session/message search
- add normal secondary indexes for foreign keys, `status`, `created`, `last_message_at`, and `next_run_at`
- use PocketBase relation fields only where they simplify UI/admin inspection; still keep explicit stable IDs in contracts

### 1.2 Filesystem Artifacts

Keep these on disk per profile:

- `SOUL.md`
- `memories/MEMORY.md`
- `memories/USER.md`
- `skills/<skill>/`
- `logs/`
- `exports/`
- `cron/output/`
- optional browser/session artifacts

### 1.3 Search

Search must support:

- session search
- skill discovery
- dashboard previews

Implementation options:

- primary: PocketBase SQLite plus FTS tables managed by migrations
- optional secondary: trigram-like supplemental index table for substring search if needed

Search result DTO minimum fields:

- object type
- object ID
- title or label
- snippet
- matched field
- profile ID
- session lineage metadata when applicable
- timestamp

## 2. Config Files

### 2.1 `config.yaml`

Stores all non-secret settings and is the canonical human-edited config artifact.

### 2.2 `.env`

Stores secrets only.

### 2.3 Auth Store

Stores refreshable OAuth or equivalent external auth artifacts separated from `.env`.

## 3. Portable Assets

Portable assets remain the same as the source spec.

Repo rule:

- frontend assets may be served from `pb_public/` in development and embedded into the binary for release builds

## 4. Skills Artifact Contract

Each skill directory contains:

- `SKILL.md`
- optional front matter
- `references/`
- `templates/`
- `scripts/`
- `assets/`

Skill metadata in PocketBase must include:

- skill ID
- name
- version
- description
- provenance
- trust level
- state: active, archived, pinned, quarantined
- filesystem path

## 5. Backups and Exports

Required exports:

- sessions
- profile metadata
- config
- skills
- cron jobs

Recommended packaging:

- zip or tar archive containing JSON manifests plus markdown/files

## 6. Profile Layout

Recommended profile root:

```text
profiles/<profile-slug>/
  config.yaml
  .env
  SOUL.md
  memories/
  skills/
  cron/
  exports/
  logs/
  home/
```

PocketBase database files remain under the application data root, but all rows must carry `profile_id` and enforce logical isolation.

## 7. Migrations and Compatibility

Rules:

- all schema changes use PocketBase migrations checked into the repo
- additive config keys get defaults during load
- deprecated keys emit warnings
- migrations must preserve sessions, skills, jobs, and memories
- every migration must be reversible where PocketBase supports down migrations; if not reversible, the migration file must document the one-way behavior
