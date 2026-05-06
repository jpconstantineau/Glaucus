# 06. Storage, Configuration, and Assets

## 1. Persistent Storage

### 1.1 Session Database

Use an embedded relational database with full-text search support.

Required capabilities:

- sessions table
- messages table
- full-text search indexes
- lineage tracking
- usage accounting
- schema migration support
- export and deletion operations

Recommended additional contract:

- write-ahead logging or an equivalent concurrency-friendly mode
- at least one FTS index optimized for ordinary token search
- support for non-Latin substring-friendly search, such as a trigram index or equivalent
- message fields that can preserve assistant reasoning, tool-call metadata, finish reasons, and provider-specific normalized payloads
- session fields that can preserve source surface, model config snapshot, cost accounting, parent session id, and explicit title
- full-text indexes should cover not only visible message text but also tool names and serialized tool-call payloads
- session titles should be treated as user-facing durable identifiers and implementations may enforce uniqueness among live titles
- session deletion should preserve descendant sessions by orphaning or re-rooting lineage links rather than destroying unrelated continuations

### 1.2 Jobs Store

Cron jobs must be stored durably with:

- id
- name
- prompt
- schedule
- skills
- delivery target
- state
- next and last run timestamps
- repeat metadata
- provider overrides
- script metadata

Recommended adjacent cron artifacts:

- per-job output history
- lock files or leases preventing duplicate scheduler ticks
- scheduler metadata such as last tick or last failure

### 1.3 Memory Files

The system must persist:

- `SOUL.md`
- `memories/MEMORY.md`
- `memories/USER.md`

The implementation should also persist:

- optional skill-usage telemetry
- curator state
- profile-level identity metadata

### 1.4 Skills Store

The system must support:

- bundled skills
- optional skills
- user-created skills
- plugin-provided skills
- external skill directories if the product exposes them
- remote-hub-installed skills with provenance tracking

## 2. Config Files

### 2.1 `config.yaml`

Stores all non-secret settings.

The configuration model should be hierarchical and include, at minimum, sections for:

- model
- providers
- agent
- terminal
- display
- memory
- approvals or security
- delegation
- compression
- gateway
- logging
- cron
- curator
- plugins
- profiles
- auxiliary task routing

### 2.2 `.env`

Stores secrets and token material.

Non-secret operational settings such as current working directory, timeouts, behavior flags, and feature toggles must live in `config.yaml` rather than `.env`.

### 2.3 Auth Store

Stores provider-specific refreshable auth or OAuth material where needed.

The product must distinguish:

- long-lived user-managed secrets in `.env` or an equivalent secret store
- refreshable auth artifacts in the auth store
- transient runtime tokens or browser session tokens that must not be persisted as durable profile secrets

## 3. Portable Assets

The following artifact classes are language-neutral and should be preserved:

- skill markdown content
- optional skill catalog content
- profile identity and memory markdown
- plugin manifests
- dashboard manifests
- static frontend assets
- model/provider metadata manifests
- session export formats
- cron job export formats
- skills-hub lock files and audit logs
- quarantined skill bundles awaiting review
- dashboard frontend assets and theme manifests

## 4. Skills Artifact Contract

Each skill may include:

- main instruction file
- front matter metadata
- references
- templates
- scripts
- assets

The runtime must support progressive disclosure: listing skills is cheap; loading a full skill body is explicit.

If the product supports remote skill installation, it must also support:

- provenance recording
- trust levels
- quarantine before activation
- audit logging of installs, updates, and removals

## 5. Search

The system must support:

- session search across messages
- skill discovery search
- optional dashboard search
- search snippets and result previews

Search implementations should preserve enough metadata to show:

- matched session title or source
- matched message preview
- timestamps
- lineage relationship where applicable

## 6. Backups and Exports

The implementation should support:

- session export
- profile export/import
- config export/import
- skill archive and restore
- cron job inspection and export

## 7. Profile Layout and Derived State

A profile directory should be able to contain:

- `config.yaml`
- `.env`
- an auth store
- `state.db` or equivalent session database
- `response_store.db` or equivalent API-response store if stateful API responses are supported
- `logs/`
- `cron/`
- `skills/`
- `memories/`
- optional `home/` for subprocess HOME isolation

Recommended durable subpaths include:

- `logs/agent.log`
- `logs/errors.log`
- `logs/gateway.log`
- `logs/curator/`
- `cron/jobs.*`
- `cron/output/`
- `skills/.hub/`
- `skills/.archive/`
- `skills/.curator_state`

## 8. Migrations and Compatibility

The implementation must support storage and config migration over time.

Migration requirements:

- additive config keys should be seeded automatically where possible
- removed or deprecated keys should produce a user-visible warning and, where feasible, a bridged fallback
- migrations must preserve user data, especially sessions, skills, memories, and cron jobs
- legacy storage paths may remain readable for backward compatibility if the product has already shipped them
