# 07. Security, Operations, and Acceptance

## 1. Security Model

The platform must implement defense in depth across:

1. user authorization
2. dangerous command approval
3. backend isolation
4. prompt-injection resistance
5. credential scoping and redaction
6. path and workspace safety
7. session and profile isolation
8. supply-chain and extension trust
9. local web-surface protection

## 2. Dangerous Command Approval

### 2.1 Modes

- manual
- smart
- off
- YOLO override for the session or launch

### 2.2 Hardline blocklist

Some commands must never run regardless of approval mode, including representative classes such as:

- root filesystem wipes
- fork bombs
- destructive block-device writes
- remote-script execution patterns considered irredeemably unsafe

The implementation should separate:

- commands that are always blocked
- commands that require approval
- commands that are allowed without approval

This policy must be transport-independent so CLI, TUI, ACP, API, and gateway surfaces all enforce equivalent safety.

### 2.3 Approval outcomes

- allow once
- allow for session
- allow permanently
- deny

Gateway and API-driven surfaces must support equivalent approval semantics using their own interaction models.

## 3. User Authorization

The gateway must support:

- platform-specific allowlists
- global allowlists
- platform-specific allow-all
- global allow-all
- DM pairing or equivalent invite-code model

## 4. Credential Safety

- provider credentials must be scoped to the correct endpoint
- secrets should not be forwarded blindly to subprocesses or MCP servers
- logs should redact known secret values where feasible
- browser and dashboard session tokens must be short-lived and separated from durable profile secrets
- plugin and MCP configuration UIs must avoid echoing secret values back in plain text unless the user explicitly reveals them

## 5. Isolation

- profiles isolate state and credentials
- local shell backend is not sandboxed and must be labeled accordingly
- container or remote backends are optional stronger isolation boundaries
- cron jobs and sessions must not access one another's logical state accidentally
- subprocess HOME overrides, when supported, must affect child processes only and must not silently alter the agent process's own global home directory

## 6. Supply-Chain and Extension Safety

The product should treat extensions as a supply-chain boundary.

Required controls:

- plugins are opt-in or otherwise explicitly authorized
- plugin manifests are validated before activation
- remote skill bundles are path-validated before extracting to disk
- remote skill installation supports quarantine and optional scanning before activation
- dashboard and API surfaces must not allow arbitrary plugin or skill code execution without the same trust checks applied elsewhere

## 7. Dashboard and Local Web Safety

If the product exposes a local dashboard, it must defend against browser-local attacks.

Recommended controls:

- bind to loopback by default
- validate the `Host` header or equivalent to reduce DNS-rebinding risk
- use an ephemeral session token or equivalent local auth barrier for sensitive endpoints
- distinguish public read-only endpoints from protected configuration or secret-management endpoints

## 8. Operational Surfaces

The implementation should provide:

- status command or page
- logs command or page
- health endpoints
- diagnostics or doctor flow
- version and environment reporting
- gateway service management guidance

## 9. Observability

Recommended:

- structured logs
- metrics
- event stream for live UIs
- session usage analytics
- cost accounting

## 10. Acceptance Criteria

An implementation is acceptable when all of the following are true:

1. A user can chat with the agent in CLI, TUI, API, and at least one messaging surface.
2. Sessions persist, resume, title, search, export, and lineages behave as specified.
3. The prompt assembly model includes identity, memory, skills, context, and platform hints.
4. The agent can execute file, terminal, web, browser, memory, cron, skills, and session-search flows.
5. Dangerous commands are checked and approval flows work.
6. Cron jobs can be created, edited, paused, resumed, and triggered.
7. Profiles isolate config, secrets, memory, sessions, and logs.
8. The dashboard or equivalent management surface can inspect config, sessions, logs, and cron jobs.
9. MCP or equivalent external tool integration works through dynamic tool registration.
10. The implementation clearly gates or defers any unsupported long-tail features instead of silently omitting them.

## 11. Recommended Implementation Phases

1. Foundation
   - config, profiles, storage, provider resolution, prompt builder
2. Core agent
   - tool loop, session persistence, compression, fallback
3. Primary surfaces
   - CLI, API, gateway, ACP
4. Interactive polish
   - TUI, dashboard, analytics
5. Extensibility and long-tail integrations
   - plugins, MCP, long-tail messaging adapters, advanced browser modes, RL surfaces
