# 05. API, Protocols, and UI Surfaces

## 1. Interactive CLI

The classic CLI must support:

- interactive chat
- single-query mode
- resume latest or explicit session
- explicit provider/model override
- explicit toolset selection
- preloaded skills
- multiline input
- external editor handoff
- image paste handling
- voice recording trigger where supported
- status bar with model, token, cost, and duration
- busy-input behaviors: interrupt, queue, steer
- slash command autocomplete
- approval prompts

The CLI command system must be registry-driven rather than copy-pasted per surface. A single canonical command registry should feed:

- CLI dispatch
- help output
- autocomplete
- gateway slash-command routing
- platform command menus where applicable
- alias resolution

Additional command-registry contract:

- commands may be marked CLI-only, gateway-only, or config-gated for selected gateway surfaces
- aliases must resolve back to one canonical command identity for persistence, help output, and telemetry

## 2. Modern TUI

The TUI must provide the same runtime capabilities as CLI with richer presentation:

- overlays for help, model picker, session picker, approvals, and clarifications
- non-blocking input before full startup
- streaming markdown and tool details
- mouse-friendly selection
- alternate-screen rendering
- detail visibility modes
- optional embedded browser rendering via dashboard PTY bridge

Recommended transport contract:

- a newline-delimited JSON-RPC or equivalent message protocol over stdio between the frontend renderer and a backend gateway
- explicit methods for prompt submission, session listing and resume, slash-command execution, approvals, clarifications, tool events, and streaming deltas
- a persistent slash-command worker or equivalent optimization so slash commands do not reinitialize the whole runtime every time
- a strategy for keeping interrupt and approval events responsive even while long-running handlers are active

## 3. OpenAI-Compatible API Server

### 3.1 Required endpoints

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
- jobs CRUD endpoints

### 3.2 Required behavior

- bearer auth
- stateless Chat Completions mode
- stateful Responses mode with previous-response chaining
- named conversations
- SSE streaming
- structured tool progress events
- inline image input support
- model discovery endpoint
- machine-readable capability discovery endpoint

The API server should also define:

- optional session continuity headers for clients that want Hermes-managed session state on otherwise stateless endpoints
- request normalization for multimodal content arrays, not just plain strings
- explicit limits for payload size, content-part count, and normalized text size
- a durable response store for the Responses API so prior responses survive process restarts where feasible

## 4. ACP-Compatible Editor Integration

The ACP surface must support:

- stdio JSON-RPC transport
- session lifecycle: create, resume, fork, list, cancel
- prompt execution in editor workspace context
- approval bridge
- progress event stream
- tool-rendering helper payloads for editors

The ACP surface should also support:

- session fork and branch semantics
- content blocks for text and images at minimum
- model-selection metadata suitable for editor pickers
- paginated session listing
- cancellation that interrupts active runs without losing persisted session history

## 5. Web Dashboard

The dashboard must expose a browser-based management interface with pages for:

- status
- chat
- config
- API keys or env vars
- sessions
- logs
- analytics
- cron
- skills
- tools or toolsets
- plugins, profiles, and models if included in product scope

### 5.1 Dashboard backend API

The backend must expose REST resources for:

- status
- config and schema
- env var CRUD
- sessions and messages
- session search
- logs
- analytics
- cron jobs
- skills
- toolsets

The dashboard backend should also expose:

- config schema metadata for form generation
- env-var metadata describing which secrets or optional settings are supported
- plugin and theme discovery
- session-token or equivalent browser auth bootstrap data

### 5.2 Embedded TUI

The chat tab may embed the real TUI over PTY/WebSocket rather than re-implementing chat semantics in the browser. If so, the PTY lifecycle, auth, resizing, and cleanup must be well-defined.

Recommended PTY-bridge contract:

- browser terminal emulator talks to a PTY-backed `hermes --tui` child process
- raw PTY bytes pass through a WebSocket without reinterpreting terminal semantics in the dashboard
- resize messages are translated into PTY window-size updates
- authentication for the WebSocket uses the same local dashboard auth mechanism as REST
- platforms without PTY support must fail clearly and non-destructively rather than partially emulating the terminal
- the dashboard chat surface should prefer embedding the real terminal UI over re-implementing a second independent chat runtime in the browser

## 6. Runs and Events Protocol

All machine-facing clients should be able to observe:

- run start
- run status changes
- tool events
- reasoning or text deltas
- final output
- cancellation
- completion or failure

This may be exposed over SSE, WebSocket, polling, or protocol-specific event bridges, but the logical event model should stay consistent.

Suggested event families:

- run lifecycle
- assistant text deltas
- assistant reasoning deltas when available
- tool start, progress, and complete
- approval requested, answered, and timed out
- clarification requested and answered
- background-process updates
- gateway stderr or crash notices for interactive surfaces
