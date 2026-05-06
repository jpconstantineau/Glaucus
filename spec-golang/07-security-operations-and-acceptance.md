# 07. Security, Operations, and Acceptance

## 1. Security Model

The Go implementation must preserve the source spec's defense-in-depth model with explicit service boundaries for:

- authorization
- approvals
- path safety
- prompt-injection screening
- secret handling
- profile isolation
- extension trust
- local web protection

## 2. Dangerous Command Approval

### 2.1 Modes

- `manual`
- `smart`
- `off`
- per-run or per-session `yolo`

### 2.2 Blocklist

Maintain a transport-independent rule set for:

- always block
- require approval
- allow

Implementation rule:

- policy evaluation occurs in a shared approvals package before shell launch
- API, web, CLI, cron, and gateway surfaces all call the same evaluator

### 2.3 Approval Outcomes

- allow once
- allow for session
- allow permanently
- deny

Approval decisions must be persisted and auditable.

## 3. User Authorization

Dashboard and API authorization default:

- PocketBase admin or privileged operator auth for server management
- separate end-user chat identity support may be layered through a dedicated auth collection if multi-user browser chat is needed

Messaging authorization follows the source spec allowlist modes.

## 4. Credential Safety

- secrets stored in `.env`, auth store, or secured records only
- redact secrets from logs
- never pass all process env vars blindly to subprocesses
- browser session tokens must be short-lived
- dashboard secret APIs reveal values only through explicit privileged flows

## 5. Isolation

- profiles isolate durable state logically and by filesystem path
- local shell backend is unsandboxed and must be labeled in UI and API metadata
- no run may access another profile's records without explicit admin action

## 6. Supply-Chain and Extension Safety

- plugins are opt-in
- skill bundles are path-normalized and may be quarantined
- MCP servers must be explicitly configured
- plugin manifests are validated before activation

## 7. Dashboard and Local Web Safety

Required:

- bind to loopback by default
- host validation
- CSRF mitigation for state-changing browser requests
- secure session handling
- clear separation between public health endpoints and protected admin APIs

## 8. Operational Surfaces

Required:

- status page
- logs page
- health endpoints
- `doctor` CLI command
- version/build info endpoint

## 9. Observability

Recommended:

- structured logs via `slog`
- metrics endpoint or internal metrics API
- live event stream
- usage/cost rollups

## 10. Acceptance Criteria

This repo is acceptable when:

1. Users can interact through the web UI, API, CLI, ACP, and at least one messaging adapter.
2. Sessions persist, resume, search, export, and preserve lineage.
3. Prompt assembly includes identity, memory, skills, context, and platform hints.
4. Tool families from the source spec are implemented or explicitly gated with stable contracts.
5. Approval and dangerous-command controls work consistently.
6. Cron jobs support create, update, pause, resume, run-now, and audit history.
7. PocketBase-backed storage and auth are functioning as the primary backend.
8. The dashboard can inspect status, config, sessions, logs, providers, adapters, skills, and cron jobs.
9. MCP or equivalent external tool integration works through dynamic registration.
10. No required production path depends on `cgo`.

## 11. Recommended Implementation Phases

1. foundation: config, profiles, PocketBase schema, provider/router, prompt builder
2. runtime: sessions, runs, tool loop, approvals, search, events
3. surfaces: web UI, API, CLI, ACP
4. automations: cron, gateway, background processes
5. extensions: MCP, plugins, long-tail adapters, advanced browser/media features
