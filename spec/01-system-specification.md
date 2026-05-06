# Specification

Status: Draft v1 (language-agnostic)

Purpose:

Define a complete, self-contained specification for a multi-surface autonomous agent platform that combines conversational AI, tool use, persistent memory, messaging integrations, scheduled jobs, editor integration, web UI, plugin extensibility, and research/evaluation surfaces.

## 1. Problem Statement

The system to be implemented is not a simple chat interface. It is a persistent agent platform that must:

- converse with users across terminal, messaging, API, editor, and web surfaces
- call tools to take actions in the filesystem, shell, browser, network, and external services
- preserve state across sessions
- adapt to many model providers and API dialects
- support long-running integrations such as gateways and scheduled jobs
- remain configurable, extensible, safe, and observable

The system must be implementable in any modern programming language without relying on language-specific assumptions such as Python threads, import-time registration, or a particular package ecosystem.

## 2. Goals and Non-Goals

### 2.1 Goals

- Provide a consistent agent experience across CLI, TUI, API, editor, gateway, and dashboard surfaces.
- Support model tool-calling loops with retries, cancellation, fallback providers, and context compression.
- Preserve durable user state: sessions, titles, memory, skills, cron jobs, profiles, logs, and configuration.
- Support a broad tool ecosystem including shell, file, browser, web, vision, code execution, messaging, scheduling, memory, and MCP.
- Support multiple execution backends for shell and browser work.
- Support external integration via messaging adapters, MCP servers, plugin categories, and provider profiles.
- Provide strong safety controls for destructive commands, credential handling, user authorization, prompt-injection resistance, and profile isolation.
- Make the system portable across implementation languages and deployment environments.

### 2.2 Non-Goals

- This specification does not mandate any specific implementation language.
- This specification does not require preserving the original source layout.
- This specification does not require internal APIs to mirror any prior implementation.
- This specification does not require every optional feature to be built in the first release, as long as unsupported features are clearly gated and not misrepresented.

## 3. System Overview

The system consists of a common agent runtime plus multiple user-facing and machine-facing surfaces.

### 3.1 Main Components

1. Agent Runtime
   - manages prompt assembly, model calls, tool execution, retries, compression, reasoning capture, persistence, and callbacks

2. Provider Runtime
   - resolves provider, model, credentials, base URL, API dialect, and fallback chains

3. Tool Runtime
   - manages tool registry, toolsets, availability, dispatch, result formatting, and dynamic external tools

4. Session and State Store
   - persists sessions, messages, usage, lineage, search indexes, and titles

5. Memory and Skills Subsystem
   - manages durable memory files, user profile data, installed skills, optional skills, and skill lifecycle

6. Messaging Gateway
   - handles inbound/outbound chat integrations for messaging platforms

7. Interactive Terminal Surfaces
   - classic CLI and modern TUI

8. API and Editor Surfaces
   - OpenAI-compatible HTTP API and ACP-compatible editor integration

9. Dashboard
   - browser-based management UI and optional embedded TUI session

10. Scheduler
   - cron-style scheduling and background task execution

11. Extension Layer
   - plugins, MCP servers, provider profiles, memory backends, context engines, dashboards, and platform adapters

### 3.2 Abstraction Levels

The system should be implemented at the following abstraction levels:

- Product Surface Level
  - commands, pages, endpoints, tools, toolsets, sessions, jobs, profiles, and platform adapters

- Runtime Level
  - agent loop, provider transports, tool execution, event emission, session persistence, prompt assembly, and compression

- Integration Level
  - messaging adapters, MCP bridges, plugin loaders, browser drivers, shell backends, and authentication flows

- Storage and Operations Level
  - configuration files, databases, logs, credentials, snapshots, backups, and metrics

### 3.3 External Dependencies

The system may depend on:

- one or more LLM providers
- filesystem access
- local shell execution and optional remote/container execution
- browser automation providers or local browser automation runtime
- HTTP services for APIs, OAuth, and web search
- SQLite or equivalent embedded relational database with full-text search
- optional Node-based TUI frontend or web frontend assets
- optional external services for memory, observability, messaging, or image generation

These dependencies must be abstracted behind interfaces so the implementation language remains unconstrained.

## 4. Core Domain Model

### 4.1 Entities

The implementation must define these domain entities.

1. Profile
   - an isolated Hermes home containing config, secrets, memory, sessions, logs, skills, cron jobs, and state

2. Session
   - a conversation or run context with id, source, title, timestamps, model info, system prompt snapshot, and lineage

3. Message
   - one unit of conversation history with role, content, optional tool metadata, timestamps, reasoning, and usage fields

4. Agent Run
   - one live execution of the agent loop over one user request or scheduled job prompt

5. Tool Definition
   - a named callable capability with schema, toolset membership, availability rules, interactivity flags, and handler binding

6. Toolset
   - a named bundle of tools used per platform, session, or custom configuration

7. Provider Profile
   - static metadata about a model provider: auth modes, API dialect, base URL, capabilities, and defaults

8. Provider Runtime Resolution
   - the resolved provider, model, credentials, base URL, API mode, and fallback behavior for a specific call path

9. Memory Target
   - either agent memory or user profile memory

10. Skill
   - a reusable procedural knowledge artifact with metadata, instructions, optional references, optional scripts, optional templates, and optional config requirements

11. Cron Job
   - a scheduled task with prompt, schedule, delivery target, attached skills, optional script, state, repeat metadata, and last/next run timestamps

12. Platform Adapter
   - a runtime integration for one messaging or transport platform

13. Plugin
   - a discovered extension artifact that may add tools, hooks, providers, dashboards, skills, or adapters

14. MCP Server
   - an external tool provider reachable via stdio or HTTP protocol and dynamically exposing tools/resources/prompts

15. Approval Request
   - a human authorization request for a dangerous operation

16. Background Process
   - a long-lived process started by the terminal subsystem and managed across polling, waiting, killing, and log retrieval

17. Browser Session
   - one isolated automation context for web navigation and interaction

18. Run Event
   - a structured event emitted by the runtime for streaming progress, tool starts, tool completions, state changes, or output deltas

19. Session Source
   - the origin surface for a session, such as CLI, TUI, API, ACP, cron, or a specific messaging platform

20. Auxiliary Task Slot
   - a named non-primary model task category with its own provider-routing policy, such as compression, vision, session search, title generation, or curator review

21. Skills Hub Artifact
   - a remotely sourced skill bundle with provenance, trust level, quarantine state, and install metadata

22. Curator State
   - durable state for the background skill-maintenance subsystem including last run, pause flag, and report references

## 5. Workflow Specification (Repository Contract)

This section defines the artifact contract expected by the system. It is called a repository contract because implementations typically store these artifacts in a user home directory, project directory, or profile directory.

### 5.1 File Discovery and Path Resolution

The implementation must support a profile home directory containing:

- `config.yaml`
- `.env`
- an auth store
- `SOUL.md`
- a `memories/` directory containing `MEMORY.md` and `USER.md`
- a skills directory
- a cron directory
- logs
- session/state storage

The implementation must treat the profile home as the single source of truth for durable state. All stateful features must resolve paths relative to the active profile home rather than hardcoding a global default path.

The implementation should also distinguish:

- profile root
  - the directory containing one or more profiles

- active profile home
  - the currently selected profile's concrete storage directory

- subprocess home
  - an optional per-profile home directory injected only into child processes so external tools such as `git`, `ssh`, package managers, and CLIs can maintain profile-isolated state without changing the agent process's own notion of the operating-system home directory

The implementation must support project context file discovery in this priority order:

1. `.hermes.md` or `HERMES.md`, searched from the current working directory upward toward the project root
2. `AGENTS.md` in the current working directory
3. `CLAUDE.md` in the current working directory
4. `.cursorrules` or `.cursor/rules/*.mdc` in the current working directory

Additional contract:

- exactly one project-context source is loaded per prompt build: the first non-empty source found in the priority order above
- files in step 1 are searched upward through ancestor directories and stop at the repository root or filesystem root
- files in steps 2 through 4 are resolved from the current working directory unless the implementation explicitly documents broader discovery
- if `.cursor/rules/*.mdc` is supported, all matching files in that directory are loaded in deterministic order
- text extraction must reject binary files and enforce size limits
- YAML front matter in `.hermes.md` or `HERMES.md` is treated as metadata and must not be injected verbatim into the prompt body
- suspicious context content may be blocked entirely rather than partially injected when prompt-injection scanning detects hidden or adversarial instructions
- the implementation should support progressive rediscovery during long-lived coding sessions as the working directory changes

Progressive subdirectory discovery is allowed and recommended during long coding sessions.

### 5.2 File Format

The system must support these durable file formats:

- YAML for configuration
- dotenv key/value format for secrets
- Markdown for skills, memory, profile identity, and context files
- JSON or JSONL for exports, jobs, transcripts, trajectories, and API-compatible artifacts

### 5.3 Front Matter Schema

Skills must support front matter fields for:

- name
- description
- version
- author
- license
- platforms
- category
- tags
- related skills
- optional config requirements
- provenance, where relevant

Recommended additional fields:

- created by
- creation timestamp
- trust level
- category path
- external config bindings
- related skills or dependencies

Unknown fields should be tolerated unless explicitly forbidden.

### 5.4 Prompt Template Contract

The stable prompt must be assembled from these conceptual slots:

1. identity
2. tool-use behavior guidance
3. optional provider-specific or memory-provider-specific blocks
4. optional system override
5. memory snapshot
6. user profile snapshot
7. skills index
8. project context
9. timestamp/session metadata
10. platform hint

Ephemeral overlays such as per-turn pressure warnings, prefill prompts, and runtime hints must not permanently mutate the stable cached prefix.

The prompt system must also support these behaviors:

- a stable cached prefix for long-lived system context
- temporary overlays for one turn only
- platform-specific behavioral hints for CLI, API, ACP, cron, and messaging surfaces
- optional memory-provider and plugin-injected prompt fragments
- project-context loading that selects at most one of `.hermes.md`/`HERMES.md`, `AGENTS.md`, `CLAUDE.md`, or cursor rules per prompt build, while allowing `SOUL.md` to remain an independent identity input
- preservation of active task-planning state across context compression
- preservation of assistant reasoning outside the visible assistant content when the provider exposes it separately
- stripping or quarantining provider-emitted hidden reasoning tags from visible assistant content before persistence or replay

### 5.5 Workflow Validation and Error Surface

The system must validate:

- malformed or missing config
- invalid context-file paths
- binary file injection into text-only flows
- malformed skill front matter
- missing or unreadable profile directories
- invalid profile selection or profile-root layout
- attempts to write outside the active profile home for profile-owned artifacts
- malformed remote skill bundles or unsafe bundle paths
- malformed plugin manifests
- malformed MCP server metadata

Validation failures must be surfaced as structured, actionable errors. Silent fallback is acceptable only when the implementation explicitly documents the degraded mode and it is non-destructive.
- unknown tool or toolset names
- unsupported provider credentials
- invalid cron schedules
- invalid API payloads
- invalid or unsafe shell operations

Errors must be surfaced as structured runtime errors and, where applicable, user-readable explanations.

## 6. Configuration Specification

### 6.1 Source Precedence and Resolution Semantics

Configuration resolution order must be:

1. explicit runtime arguments
2. `config.yaml`
3. `.env`
4. built-in defaults

Environment variable substitution inside YAML using `${VAR_NAME}` syntax must be supported.

Secrets must be stored in `.env` or equivalent secret storage, while non-secret settings must live in `config.yaml`.

### 6.2 Dynamic Reload Semantics

The implementation should support reload for:

- `.env` secrets during a running interactive session
- MCP server configuration
- plugin discovery where supported
- model or display settings when safe

Reloaded state must not corrupt active runs. Where a setting cannot be safely reloaded, it must be documented as applying only to new sessions.

### 6.3 Dispatch Preflight Validation

Before starting an agent run, the system should validate:

- provider/runtime resolvability
- enabled toolsets and tool availability
- session persistence readiness
- prompt inputs and context file safety
- working directory validity
- platform auth for adapter-driven requests
- schedule validity for cron creation or edit

### 6.4 Config Fields Summary (Cheat Sheet)

The configuration model must support at least these top-level domains:

- `model`
- `providers`
- `agent`
- `terminal`
- `display`
- `compression`
- `memory`
- `delegation`
- `approvals`
- `cron`
- `browser`
- `voice`
- `tts`
- `stt`
- `gateway`
- `plugins`
- `mcp_servers`
- `profiles`
- `skills`
- `hooks`
- `auxiliary`

## 7. Orchestration State Machine

### 7.1 Issue Orchestration States

This template heading is generalized here into platform orchestration states. The system must support these runtime states for one agent run:

- `created`
- `starting`
- `ready`
- `thinking`
- `executing_tools`
- `waiting_for_approval`
- `waiting_for_clarification`
- `streaming_output`
- `completed`
- `failed`
- `cancelled`
- `paused`, where relevant for background or scheduled work

### 7.2 Run Attempt Lifecycle

One run attempt must follow this lifecycle:

1. create or resolve session
2. load conversation history if applicable
3. build stable prompt and ephemeral overlays
4. resolve provider runtime
5. submit model request
6. normalize response
7. if tool calls exist, execute them and append results
8. loop until final answer or stop condition
9. persist messages, usage, and state
10. emit completion or failure event

### 7.3 Transition Triggers

Transitions are triggered by:

- receipt of user input
- provider response
- tool call parsing
- tool completion
- tool failure
- approval prompt emission and response
- clarification prompt emission and response
- cancellation request
- timeout
- fallback provider activation
- compression trigger

### 7.4 Idempotency and Recovery Rules

- session creation and title assignment should be idempotent where possible
- cron job creation should support explicit idempotency keys or deterministic duplicate avoidance where applicable
- run-state recovery after restart should allow polling and cleanup of recent run records
- session lineages created by compression must remain navigable
- background processes must survive polling disconnects and provide later status inspection

## 8. Polling, Scheduling, and Reconciliation

### 8.1 Poll Loop

The scheduler must poll for due jobs at a regular interval, default 60 seconds.

The dashboard and runs API may also use polling or SSE streams for live state, but polling must remain available for reconnect-friendly clients.

### 8.2 Candidate Selection Rules

A cron tick selects jobs that:

- are enabled
- are due
- are not already in a completed terminal state
- are not currently locked by another scheduler process

Recommended due-window contract:

- one-shot jobs may use a small grace window so a job scheduled slightly before the next tick still fires once
- recurring jobs may use a catch-up grace window derived from schedule period, such as half the interval clamped within a minimum and maximum bound
- jobs that are far past their catch-up window should be fast-forwarded to their next future run rather than executing stale work

### 8.3 Concurrency Control

The system must support:

- only one active execution per session key unless a separate background session model is used
- bounded concurrency for tool execution
- bounded concurrency for delegation
- cross-process scheduler locking
- token locks for messaging identities to prevent multiple profiles from using the same bot token simultaneously

### 8.4 Retry and Backoff

Retry and backoff must exist for:

- transient provider failures
- certain auth refresh scenarios
- scheduler polling and platform reconnects
- MCP connection refresh where supported
- browser session reconnect or keepalive flows where supported

### 8.5 Active Run Reconciliation

Long-running surfaces such as gateway, API runs, and dashboard must reconcile:

- active sessions
- interrupted runs
- stale runs
- recently completed runs
- orphaned background processes

### 8.6 Startup Terminal Workspace Cleanup

If the implementation uses persistent shell backends, containers, or PTY sessions, startup or shutdown cleanup should:

- reap stale resources
- restore reusable sandboxes where supported
- avoid deleting still-active resources owned by another process

## 9. Workspace Management and Safety

### 9.1 Workspace Layout

The system must distinguish between:

- profile home
- current working directory for tool execution
- remote or container workspace roots
- browser session state directories
- skill directories
- cron job state directories

### 9.2 Workspace Creation and Reuse

The system should support:

- local direct execution
- persistent container execution
- persistent or reconnectable remote workspace execution
- per-task isolated workspaces for evaluation or benchmarking flows

### 9.3 Optional Workspace Population (Implementation-Defined)

The implementation may populate workspaces with:

- mounted current directory
- synced credential files
- copied support assets
- restored persistent snapshots

This behavior is implementation-defined but must be explicit and safe.

### 9.4 Workspace Hooks

The system should support hooks around:

- session start
- tool invocation
- file write
- shell command execution
- session end
- gateway startup
- approval handling

### 9.5 Safety Invariants

- local backend has host-equivalent access and must be clearly labeled unsafe relative to containers
- destructive command approval must happen before execution unless the backend is already treated as a safe sandbox boundary
- path traversal must be blocked in file and context-reference operations
- prompt injection scanning must run on context files
- secrets must be redacted from logs where feasible

## 10. Agent Runner Protocol (Coding Agent Integration)

### 10.1 Launch Contract

The agent runner must support:

- one-shot query execution
- interactive session mode
- resumed session mode
- explicit provider and model override
- explicit toolset override
- explicit profile and working directory selection

### 10.2 Session Startup Handshake

At startup, the runner must:

- resolve the active profile
- load configuration and secrets
- initialize session storage
- discover tools, plugins, and MCP servers where enabled
- resolve the model/provider runtime
- load display/theme settings
- optionally preload skills

### 10.3 Streaming Turn Processing

The runtime must support streaming events for:

- text deltas
- reasoning deltas or blocks
- tool call generation
- tool progress
- status transitions
- approvals and clarification waits
- final output

### 10.4 Emitted Runtime Events (Upstream to Orchestrator)

Implementations should define structured events including:

- `run.started`
- `run.status_changed`
- `reasoning.delta`
- `output.delta`
- `tool.requested`
- `tool.started`
- `tool.completed`
- `tool.failed`
- `approval.requested`
- `approval.responded`
- `clarification.requested`
- `clarification.responded`
- `run.completed`
- `run.failed`
- `run.cancelled`

### 10.5 Approval, Tool Calls, and User Input Policy

- dangerous commands require approval depending on policy mode
- some tools are interactive and must not execute in parallel
- new user input may interrupt, queue, or steer active runs depending on configured busy-input policy
- tool result envelopes must always be well-formed

### 10.6 Timeouts and Error Mapping

Timeouts must exist for:

- provider requests
- stale provider calls
- tool execution
- background process wait
- cron scripts
- child delegation
- platform reconnects

Recommended long-run guardrails:

- cron-triggered agent runs should have a hard wall-clock ceiling independent of model iteration limits
- script-backed cron jobs should have their own subprocess timeout distinct from the agent-run timeout

Errors should be classified at least as:

- validation error
- auth error
- transient provider error
- non-retryable provider error
- tool execution error
- safety rejection
- cancellation
- timeout
- integration error

## 11. Issue Tracker Integration Contract (Linear-Compatible)

This template section is generalized because issue tracking is not the core product but may exist through tools or plugins.

### 11.1 Required Operations

The platform must be able to integrate with external systems that expose operations such as:

- list
- search
- read
- create
- update
- comment
- assign
- link
- close/open

### 11.2 Query Semantics (Linear)

For issue trackers, the implementation should support normalized semantics for:

- issues
- pull requests
- tasks
- labels
- assignees
- comments
- status changes

### 11.3 Normalization Rules

External integration objects should be normalized into internal representations with:

- id
- title
- body
- state
- labels
- assignees
- timestamps
- source system

### 11.4 Error Handling Contract

External integration failures must clearly distinguish:

- not found
- unauthorized
- rate limited
- unsupported operation
- transport failure

### 11.5 Tracker Writes (Important Boundary)

Any external write capability is treated as a sensitive action and must honor the platform's normal approval, auth, and audit policies.

## 12. Prompt Construction and Context Assembly

### 12.1 Inputs

Prompt assembly may use:

- profile identity file
- memory snapshot
- user profile snapshot
- installed skills index
- explicitly loaded skills
- project context files
- optional system override
- platform hint
- session metadata
- provider-specific guidance

### 12.2 Rendering Rules

- stable prompt prefix must be deterministic
- context file order must follow defined priority
- large context blocks must be truncated safely
- skill index must be compact, not full skill bodies
- full skill body is loaded only when selected

### 12.3 Retry/Continuation Semantics

- retries should not mutate the stable prompt unexpectedly
- continuation sessions after compression should preserve enough summary context to continue work
- fallback provider invocation should reuse the same logical turn context

### 12.4 Failure Semantics

- prompt assembly failure should halt the run before provider calls
- unreadable context files should degrade gracefully unless required for the current operation
- missing skill assets should produce a user-visible warning or tool error rather than silent omission

## 13. Logging, Status, and Observability

### 13.1 Logging Conventions

The system must write structured or semi-structured logs for:

- agent runtime
- gateway
- errors
- optional platform-specific or cron events

### 13.2 Logging Outputs and Sinks

At minimum:

- agent log
- error log
- gateway log

Additional sinks may include:

- console
- JSON logs
- remote observability plugins

### 13.3 Runtime Snapshot / Monitoring Interface (Optional but Recommended)

Expose current runtime status for:

- active sessions
- active runs
- background processes
- scheduler state
- provider health
- tool availability

### 13.4 Optional Human-Readable Status Surface

At least one human-readable status surface should exist, such as:

- status CLI command
- dashboard status page
- gateway status command

### 13.5 Session Metrics and Token Accounting

Persist at least:

- input tokens
- output tokens
- total tokens
- cache tokens where applicable
- reasoning tokens where applicable
- estimated or actual cost
- session duration
- API call count

### 13.6 Humanized Agent Event Summaries (Optional)

Interactive surfaces may present human-friendly summaries such as:

- "thinking"
- "running tests"
- "waiting for approval"
- "browser connected"
- "cron job completed"

## 14. Additional Project-Specific Sections

### 14.1 Interactive Surfaces

The product must implement:

- classic terminal CLI
- modern TUI
- browser dashboard
- ACP editor surface
- OpenAI-compatible API
- messaging gateway

All should operate on shared runtime primitives rather than implementing separate agent logic.

### 14.2 Tool Families

The following tool families are in scope:

- file operations
- shell and process management
- web search and extraction
- browser automation
- code execution
- delegation
- memory
- session search
- skills
- cron management
- todo/task planning
- vision
- image generation
- text-to-speech
- speech-to-text
- platform-specific messaging tools
- MCP tool proxies
- optional RL/evaluation tools

### 14.3 Messaging Platforms

The documented platform set is:

- Telegram
- Discord
- Slack
- WhatsApp
- Signal
- Matrix
- Mattermost
- Email
- SMS
- DingTalk
- Feishu / Lark
- WeCom
- Weixin
- BlueBubbles
- Home Assistant
- Webhooks
- QQ Official Bot
- Yuanbao

### 14.4 Browser Modes

The browser subsystem must be capable of representing:

- public cloud browser providers
- local browser sidecar mode
- local browser attachment via CDP
- anti-detection local mode
- hybrid local/public routing

### 14.5 Voice and Media

The system must support:

- incoming voice transcription
- outgoing speech synthesis
- image analysis
- image paste or image upload flows
- media delivery differences across platforms

### 14.6 Skills and Extensions

The product must support:

- bundled skills
- optional skills
- user-created skills
- plugin-discovered skills
- plugin categories
- MCP servers
- provider profiles
- memory backends
- context engine replacements

### 14.7 Acceptance Principle

A language implementation conforms to this specification when:

- users can perform the same classes of work across the same surfaces
- durable artifacts and concepts are preserved
- safety boundaries remain intact
- unsupported long-tail features are explicitly gated rather than silently absent
