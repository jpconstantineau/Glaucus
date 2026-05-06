# 01. System Specification

Status: Draft v1, Go + PocketBase adaptation

## 1. Problem Statement

This repository must evolve from a basic PocketBase-backed Go application into a persistent autonomous agent platform with:

- conversational agent runtime
- web dashboard and web chat UI as the primary interactive surface
- OpenAI-compatible API
- messaging gateway integrations
- durable sessions, jobs, memories, skills, and logs
- tool execution across filesystem, shell, browser, web, and external systems
- provider routing, fallback, approval, and observability

The implementation must remain practical for a single self-contained Go binary and must not require `cgo`.

## 2. Goals and Non-Goals

### 2.1 Goals

- Keep the backend idiomatic Go: constructor-based wiring, interfaces at subsystem boundaries, `context.Context` everywhere, explicit lifecycle management, and structured errors.
- Use PocketBase as the durable storage and auth backbone rather than introducing a second primary backend.
- Deliver a browser-based interactive experience instead of a TUI while preserving the runtime capabilities described by the source spec.
- Keep the system shippable as a single binary plus static assets.
- Minimize third-party dependencies outside PocketBase and narrowly scoped provider/browser libraries.

### 2.2 Non-Goals

- No `cgo`, Electron shell, or Node-dependent production runtime.
- No separate microservice split for API, scheduler, dashboard, and gateway in the default architecture.
- No duplicated agent runtime per surface; all surfaces must call the same Go orchestration services.

## 3. System Overview

### 3.1 Main Components

1. `cmd/chatbase`
   - binary entrypoint
   - config/profile/bootstrap
   - PocketBase startup
   - service registration

2. Agent Runtime
   - prompt assembly
   - provider loop
   - tool dispatch
   - retries, fallback, compression
   - run event emission

3. Provider Runtime
   - provider catalog
   - credential resolution
   - request normalization
   - fallback planning

4. Tool Runtime
   - registry
   - toolsets
   - validation
   - approval checks
   - execution envelopes

5. PocketBase Persistence Layer
   - collections for sessions, messages, runs, jobs, skills metadata, profiles, providers, logs, and gateway state
   - files for skill bodies, assets, markdown memories, and exports

6. Web Surface
   - dashboard HTML shell rendered with Go templates
   - HTMX-driven partial updates
   - REST endpoints
   - SSE/WebSocket live events
   - browser chat interface

7. API Surface
   - OpenAI-compatible endpoints
   - runs/events endpoints
   - jobs CRUD

8. Scheduler and Background Services
   - cron polling
   - run reconciliation
   - skill curator
   - messaging adapter workers

### 3.2 Required Go Project Layout

The implementation should converge toward this layout:

```text
cmd/chatbase/
internal/app/
internal/config/
internal/profile/
internal/runtime/
internal/providers/
internal/tools/
internal/approvals/
internal/sessions/
internal/jobs/
internal/skills/
internal/memory/
internal/gateway/
internal/api/
internal/web/
internal/browser/
internal/search/
internal/observability/
internal/platform/
pkg/contracts/
web/
pb_hooks/           # only if PocketBase hook files are truly needed; otherwise omit
pb_public/          # built frontend assets if not embedding
profiles/           # default local profile root in development only
```

Rules:

- `internal/` holds application code not intended as stable public API.
- `pkg/contracts/` may expose stable types used by plugins, MCP bridges, or external helpers.
- Avoid package cycles; prefer small interfaces owned by the consuming package.
- Every long-running goroutine must be owned by a service with `Start(ctx)` and `Stop(ctx)` or equivalent lifecycle hooks.

### 3.3 External Dependencies

Permitted primary runtime dependencies:

- PocketBase
- Go standard library
- provider SDKs only when materially simpler than raw HTTP and still `cgo`-free
- browser automation backends only behind a clear adapter boundary

Avoid:

- heavy DI frameworks
- ORM layers on top of PocketBase
- SPA frameworks or Node-based asset pipelines as a hard requirement
- any browser or shell backend that requires `cgo`

## 4. Core Domain Model

### 4.1 Entities and Canonical Storage

1. Profile
   - storage: PocketBase collection `profiles` plus filesystem profile root
   - contains logical isolation boundary

2. Session
   - storage: `agent_sessions`
   - fields: id, source, title, profile_id, parent_session_id, model snapshot, toolset snapshot, timestamps, archive flags

3. Message
   - storage: `agent_messages`
   - fields: session_id, run_id, role, content parts JSON, visible text, reasoning blob ref, tool metadata JSON, usage JSON, ordinal, timestamps

4. Agent Run
   - storage: `agent_runs`
   - fields: session_id, trigger_source, status, provider resolution JSON, started_at, ended_at, error class, cancellation data, summary

5. Run Event
   - storage: `agent_run_events` with TTL/retention policy
   - also streamed live over SSE

6. Tool Definition
   - runtime registry object; optionally materialized in `tool_registry_cache` for UI inspection

7. Toolset
   - config-defined plus optional collection `toolsets` for user-defined bundles

8. Provider Profile
   - config-defined manifest with optional sync into `provider_profiles`

9. Memory Target
   - filesystem-backed markdown under active profile
   - metadata in `memory_documents`

10. Skill
   - metadata in `skills`
   - body/assets on disk under profile `skills/`

11. Cron Job
   - storage: `cron_jobs`
   - output history: `cron_job_runs`

12. Platform Adapter
   - runtime service, with config/state in `platform_adapters`

13. MCP Server
   - config manifest plus runtime state in `mcp_servers`

14. Approval Request
   - storage: `approval_requests`

15. Background Process
   - storage: `background_processes`
   - logs in `profiles/<id>/logs/processes/`

16. Response Store Entry
   - storage: `response_store_entries`
   - required when implementing stateful `/v1/responses` retrieval across restarts

### 4.2 PocketBase as the Application Backbone

PocketBase is not just serving static files. It is responsible for:

- SQLite-backed persistence and migrations
- auth for dashboard/API operators
- file storage for skill assets and exports where appropriate
- admin and operator management
- realtime primitives where useful
- HTTP router integration for custom endpoints

Custom Go services remain authoritative for:

- the agent loop
- provider calls
- tool execution
- scheduler locking
- gateway connections
- prompt assembly

## 5. Workflow Specification

### 5.1 Profile Home and Path Resolution

Each active profile has:

- PocketBase records keyed by `profile_id`
- filesystem root at `profiles/<profile_slug>/`

Required filesystem contents:

- `config.yaml`
- `.env`
- `SOUL.md`
- `memories/MEMORY.md`
- `memories/USER.md`
- `skills/`
- `cron/`
- `logs/`
- `exports/`
- optional `home/` for subprocess HOME isolation

Rules:

- profile-owned files must never resolve outside the active profile root
- all file writes use cleaned absolute paths rooted to the profile or approved working directory
- project context discovery follows the source spec order

### 5.2 Supported Durable Formats

- YAML for config
- dotenv for secrets
- Markdown for identity, memory, skills, and context files
- JSON/JSONL for events, exports, trajectories, and API artifacts

### 5.3 Prompt Template Contract

Stable prompt slots are identical to the source spec, but the Go implementation must expose them as explicit builder stages:

1. identity
2. tool behavior
3. provider overlay
4. optional system override
5. memory snapshot
6. user profile snapshot
7. skills index
8. project context
9. session metadata
10. platform hint

Implementation rule:

- each stage returns a typed fragment with `Name`, `Priority`, `Cacheable`, `Content`, and `Diagnostics`
- prompt rendering is deterministic from ordered fragments

### 5.4 Validation and Error Surface

Every validation failure must map to a typed Go error category:

- `ErrValidation`
- `ErrAuth`
- `ErrSafety`
- `ErrNotFound`
- `ErrConflict`
- `ErrUnavailable`
- `ErrTimeout`
- `ErrProvider`
- `ErrToolExecution`

HTTP handlers, CLI commands, jobs, and gateways must all normalize these to a shared error envelope.

## 6. Configuration Specification

### 6.1 Source Precedence

Order:

1. explicit runtime flags
2. `config.yaml`
3. `.env`
4. compiled defaults

### 6.2 Required Go Config Model

Define a root `Config` struct with nested structs for:

- `Model`
- `Providers`
- `Agent`
- `Terminal`
- `Display`
- `Compression`
- `Memory`
- `Delegation`
- `Approvals`
- `Cron`
- `Browser`
- `Voice`
- `TTS`
- `STT`
- `Gateway`
- `Plugins`
- `MCPServers`
- `Profiles`
- `Skills`
- `Hooks`
- `Auxiliary`
- `Web`
- `API`
- `PocketBase`

Minimum config fields to define explicitly:

- `Model.DefaultProvider`
- `Model.DefaultModel`
- `Providers.<id>.BaseURL`
- `Providers.<id>.Auth`
- `Providers.<id>.Dialect`
- `Agent.MaxTurns`
- `Agent.BusyInputPolicy`
- `Approvals.Mode`
- `Approvals.BlockPatterns`
- `Cron.Enabled`
- `Cron.PollInterval`
- `Browser.Mode`
- `Browser.AllowedBackends`
- `Gateway.Platforms`
- `Web.BindAddress`
- `Web.SessionTTL`
- `API.BearerTokens` or equivalent auth reference
- `PocketBase.DataDir`

Rules:

- parse once into typed structs
- validate immediately after load
- store the effective config snapshot in memory
- never pass raw `map[string]any` through the application except for plugin or provider manifest payloads

### 6.3 Dynamic Reload

Safe to reload for new runs:

- provider credentials
- MCP config
- display settings
- some gateway settings

Not safe to mutate in-place for active runs:

- profile root
- storage paths
- provider dialect adapters mid-request

## 7. Orchestration State Machine

Required run states remain the same as the source spec.

Implementation rule:

- represent states as a typed string enum
- state transitions happen through one service method that enforces valid transition graph and emits events

## 8. Polling, Scheduling, and Reconciliation

### 8.1 Scheduler Design

- default scheduler tick: 60 seconds
- cross-process locking: PocketBase record lease with compare-and-set semantics and expiry timestamp
- stale lease recovery: allowed after lease expiry plus grace period

### 8.2 Reconciliation Jobs

At startup:

- mark stuck runs as `failed` or `cancelled` based on last heartbeat
- recover due cron jobs
- reconcile background processes
- restart enabled gateway adapters

## 9. Workspace Management and Safety

### 9.1 Working Directory Rules

- each run has exactly one effective working directory
- the working directory may be:
  - explicit per request
  - explicit per cron job
  - profile default
  - current process cwd only in development CLI mode

### 9.2 Safety Invariants

- path traversal blocked on every file tool
- unsandboxed local shell labeled clearly in UI and API metadata
- browser profile data isolated per browser session when persistence is enabled

## 10. Agent Runner Protocol

### 10.1 Supported Launch Modes

- one-shot API/CLI query
- browser chat turn
- resumed session turn
- cron-triggered run
- messaging-triggered run
- delegated child run

### 10.2 Run Execution Contract

Every run receives:

- resolved profile
- session reference
- actor identity
- provider resolution
- enabled toolsets
- working directory
- prompt fragments
- cancellation context

Every run emits:

- lifecycle events
- tool events
- assistant deltas
- final persistence write set

## 11. External Integration Contract

Integrations such as issue trackers must be written against explicit interfaces under `internal/platform/` or `pkg/contracts/`.

Rules:

- no direct provider/tool code inside HTTP handlers
- external writes must always pass through approval/audit middleware when marked sensitive

## 12. Prompt Construction and Context Assembly

Implementation details:

- use a `PromptBuilder` service, not ad hoc string concatenation in handlers
- stable fragments are cached by hash for the run duration
- full skill bodies load only on demand
- project context files are size-limited and scanned before injection

## 13. Logging, Status, and Observability

### 13.1 Logging

- default logger: structured JSON logger built on Go stdlib `log/slog`
- sinks: stdout and profile log files
- log files: `agent.log`, `errors.log`, `gateway.log`, `scheduler.log`

### 13.2 Metrics

Expose:

- process health
- active runs
- queued approvals
- gateway connection status
- provider latency
- token and cost aggregates

### 13.3 Status Surfaces

Required:

- `GET /health`
- `GET /health/detailed`
- dashboard status page
- CLI `status` command

## 14. Surface Mapping for This Repo

### 14.1 Interactive Surfaces

Required in this repo:

- web dashboard and web chat
- OpenAI-compatible API
- ACP-compatible editor protocol
- messaging gateway
- CLI for admin, diagnostics, batch work, and optional interactive fallback

Not implemented:

- no TUI surface

Replacement rule:

- every behavior the source spec assigns to the TUI must be implemented either in the browser UI or the CLI/admin surface
- the browser UI should favor server-rendered templates and HTMX fragment swaps over a client-heavy SPA architecture

### 14.2 Acceptance Principle

This Go adaptation is conformant when:

- the shared Go runtime powers all supported surfaces
- the web UI fully replaces the TUI role for rich interactive use
- PocketBase-backed persistence and auth are first-class, not bolted on
- all unsupported long-tail features are represented as config-gated or plugin-gated capabilities with documented contracts
