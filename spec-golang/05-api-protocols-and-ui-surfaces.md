# 05. API, Protocols, and UI Surfaces

## 1. Interactive CLI

This repo still includes a CLI, but it is not the flagship UX.

Required CLI capabilities:

- server start
- profile management
- diagnostics/status
- migrations/bootstrap
- export/import
- optional one-shot prompt execution
- optional interactive fallback chat

The source spec's richer interactive requirements are fulfilled primarily by the web UI rather than a TUI.

## 2. Web UI Instead of TUI

There is no TUI in this implementation.

The browser UI must provide the TUI-equivalent capabilities:

- help overlays
- model picker
- session picker
- approvals and clarifications
- streaming assistant output
- tool activity feed
- markdown rendering
- logs/status panels
- responsive layout for desktop and tablet

Frontend implementation default:

- server-rendered HTML shell using Go templates
- HTMX for navigation, form submission, partial refresh, and progressive enhancement
- vanilla ES modules only for behavior that is awkward or fragile in pure HTMX, such as SSE stream coordination, optimistic chat input state, and richer tool-event rendering
- no required Node build pipeline
- SSE for streaming run output
- WebSocket only where bidirectional low-latency interaction materially improves UX

Frontend rendering rules:

- the initial page response must be fully server-rendered and usable without a JavaScript bundle step
- HTML fragments returned to HTMX requests must be rendered from the same Go template/component system as full-page loads
- the UI should degrade gracefully when SSE is temporarily unavailable by falling back to polling for key status surfaces
- client-side state should remain minimal; canonical state lives on the server

## 3. OpenAI-Compatible API Server

### 3.1 Required Endpoints

Required endpoints:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `DELETE /v1/responses/{id}`
- `GET /v1/models`
- `GET /v1/capabilities`
- `GET /health`
- `GET /health/detailed`
- `POST /v1/runs`
- `GET /v1/runs/{id}`
- `GET /v1/runs/{id}/events`
- `POST /v1/runs/{id}/stop`
- `GET /v1/jobs`
- `POST /v1/jobs`
- `GET /v1/jobs/{id}`
- `PATCH /v1/jobs/{id}`
- `POST /v1/jobs/{id}/pause`
- `POST /v1/jobs/{id}/resume`
- `POST /v1/jobs/{id}/run`
- `DELETE /v1/jobs/{id}`

They should be mounted under PocketBase's router as custom Go handlers.

### 3.2 Required Behavior

- bearer auth
- stateless chat completions
- stateful responses
- SSE streaming
- inline image input
- run inspection and cancellation
- jobs CRUD

Implementation rule:

- API response schemas must be versioned and integration-tested independently from the dashboard

## 4. ACP-Compatible Editor Integration

ACP remains required.

Implementation approach:

- stdio JSON-RPC sidecar mode exposed through a dedicated CLI command such as `chatbase acp`
- the command wires into the same runtime services used by web/API/gateway

## 5. Web Dashboard

The web dashboard is the primary human-facing surface.

Required pages:

- status
- chat
- sessions
- run detail
- approvals
- config
- providers/models
- skills
- cron jobs
- logs
- tools/toolsets
- messaging adapters
- analytics

Required page behavior:

- `status`: process health, provider health, gateway health, scheduler health, active runs
- `chat`: session list, message transcript, streaming turn panel, tool activity rail, approval/clarification modals
- `sessions`: filter, search, rename, export, archive, resume, lineage navigation
- `run detail`: event timeline, provider attempts, tool events, usage, error diagnostics
- `approvals`: pending approvals queue, decision actions, audit trail
- `config`: editable non-secret config, schema-aware forms, reload status
- `providers/models`: configured providers, model catalog, auxiliary routing slots, credential presence indicators
- `skills`: search, inspect, activate/deactivate, create, update, archive, provenance/trust display
- `cron jobs`: list, create, edit, pause, resume, trigger now, inspect run history
- `logs`: tailed log views with filtering by subsystem and severity
- `tools/toolsets`: enabled toolsets per surface, runtime availability reasons, plugin and MCP tool visibility
- `messaging adapters`: auth/setup instructions, status, reconnect actions, allowlist configuration
- `analytics`: token/cost aggregates, run counts, latency percentiles, tool frequency

UI composition rules:

- pages should be composed from reusable Go template partials
- modal, drawer, and sidebar content should be HTMX-loadable fragments where possible
- chat transcript updates should use SSE-fed append or replace operations into stable DOM anchors
- approval and clarification interactions should be submit-without-full-reload flows

### 5.1 Dashboard Backend API

Must expose REST resources for:

- status
- config/schema
- secrets metadata and CRUD flows
- sessions/messages
- runs/events
- session search
- logs
- analytics
- cron jobs
- skills
- toolsets
- providers/models
- adapter health
- approvals queue and decision actions
- exports/imports
- profile selection where enabled

Rules:

- browser APIs return DTOs tailored for UI use, not raw PocketBase records
- writes go through service-layer validation
- sensitive fields are redacted by default
- list endpoints support pagination, sorting, and text search where the dataset can grow
- all write endpoints return the normalized resource plus any validation warnings

### 5.2 Live Update Contract

Preferred contract:

- REST for query and mutation
- SSE for run/event streams
- optional PocketBase realtime subscriptions for admin/status pages

Event payloads must align with the shared run event model.

HTMX endpoint guidance:

- expose fragment-oriented endpoints for session lists, transcript panes, approval panels, logs panes, and adapter status cards
- keep fragment routes separate from machine-facing JSON APIs where mixing formats would create ambiguity

Minimum SSE streams:

- `/api/dashboard/runs/{id}/stream`
- `/api/dashboard/sessions/{id}/stream`
- `/api/dashboard/status/stream`

## 6. Runs and Events Protocol

All machine-facing clients observe the same logical event types from the source spec.

Implementation rule:

- define one Go event schema under `pkg/contracts` or `internal/runtime/events`
- serialize it to SSE, JSON API, and ACP messages without inventing incompatible per-surface variants

Minimum event envelope:

- `id`
- `run_id`
- `session_id`
- `type`
- `sequence`
- `timestamp`
- `payload`
- `is_terminal`
