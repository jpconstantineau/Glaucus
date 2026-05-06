# 02. Tool Catalog and Toolsets

## 1. Tool Runtime Contract

Every tool is implemented as a Go value satisfying a common interface:

```go
type Tool interface {
    Definition() ToolDefinition
    CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult
    Execute(ctx context.Context, req ToolRequest) ToolResult
}
```

`ToolDefinition` must include:

- canonical name
- description
- JSON Schema for arguments
- toolset membership
- flags: interactive, approval-sensitive, read-only, platform-scoped
- concurrency policy

Tool results use one canonical envelope:

- `status`: `success | recoverable_error | fatal_error | validation_error | approval_required | approval_denied`
- `payload`
- `display_text`
- `diagnostics`
- `timing`

## 2. Built-In Tool Families

### 2.1 File Tools

Required:

- `read_file`
- `write_file`
- `patch`
- `search_files`

Implementation rules:

- use Go stdlib filesystem APIs
- support line offsets and pagination
- prefer patch/diff semantics over blind whole-file replacement
- reject binary files unless a tool explicitly supports binary mode

### 2.2 Terminal and Process Tools

Required:

- `terminal`
- `process`

Implementation rules:

- use `os/exec`
- pass `context.Context` for cancellation and timeout
- provide session-safe process IDs backed by a `background_processes` collection
- capture stdout/stderr separately
- do not expose raw OS PID as the only handle
- dangerous command screening runs before process launch

### 2.3 Browser Tools

Required public contract remains the same.

Go-specific rule:

- browser tools must sit behind `internal/browser`
- local and remote browser backends implement the same interface
- browser snapshots are normalized to a backend-neutral DOM/accessibility tree JSON shape

### 2.4 Web Tools

Required:

- `web_search`
- `web_extract`

Implementation rule:

- use adapter interfaces so provider-native search, generic HTTP extraction, and plugin search all look identical to the runtime

### 2.5 Code Execution Tool

Required:

- `execute_code`

Implementation stance:

- first release should execute code in a constrained subprocess using installed interpreters or compiled helpers
- the tool must be optional and clearly gated when no configured runtime is available
- the code execution substrate must remain `cgo`-free

### 2.6 Scheduling Tool

Required:

- `cronjob`

Implementation rule:

- this tool is a thin façade over the jobs service; it must not mutate PocketBase collections directly from the tool handler without validation

### 2.7 Delegation Tool

Required:

- `delegate_task`

Implementation rule:

- delegated runs are normal `agent_runs` with `parent_run_id`
- child runs inherit profile, auth scope, and constrained toolset unless explicitly overridden by policy

### 2.8 Planning and Memory Tools

Required:

- `todo`
- `memory`
- `session_search`

Storage rules:

- `todo` is session-scoped JSON persisted on the session record
- `memory` writes to markdown files and updates PocketBase metadata
- `session_search` queries FTS-backed indexes and may summarize with an auxiliary model

### 2.9 Skills Tools

Required:

- `skills_list`
- `skill_view`
- `skill_manage`

Storage rules:

- metadata in PocketBase
- bodies/assets on disk
- activation state must be explicit

### 2.10 Media and Perception Tools

Required:

- `vision_analyze`
- `image_generate`
- `text_to_speech`

Additional repo rule:

- `speech_to_text` should also be implemented even though the source section listed it outside this subsection, because voice flows depend on it elsewhere in the spec

### 2.11 Messaging and Platform Tools

Required:

- `send_message`

Platform-specific tools must be registered only when the adapter or plugin is enabled.

### 2.12 MCP Utility Tools

Expose only when the target MCP server advertises the capability and passes policy checks.

## 3. Toolsets

### 3.1 Canonical Toolsets

The source spec toolsets remain canonical. Persist them by name exactly.

Additional repo-specific base toolsets:

- `web_chat`
- `web_admin`
- `api_default`
- `gateway_default`

### 3.2 Selection Rules

Order:

1. start from per-run explicit enables if present, otherwise surface default
2. merge composite toolsets
3. subtract explicit disables
4. run availability checks
5. emit final tool list snapshot into the run record

### 3.3 Web-First Defaults

- browser chat: `safe + messaging + skills + todo + session_search`, plus `file`, `terminal`, `web`, `browser`, `cronjob`, and `delegation` when enabled by policy
- dashboard admin: read-only inspection plus config/jobs/skills management tools
- API: caller-selected subset bounded by server policy
- cron: narrower bundle than browser chat by default

## 4. Tool Availability

Availability may depend on:

- config flags
- provider credentials
- current surface
- working directory policy
- installed browser backend
- runtime OS support

Cross-platform requirement:

- unsupported tools on one OS must degrade cleanly with explicit availability reasons, not silent omission in admin surfaces

## 5. Tool Safety Rules

- shell and external-write tools go through approval policy
- filesystem tools enforce root and sensitive-path policies
- plugin and MCP tools are policy-filtered before exposure
- any tool that talks to external systems must emit audit records with actor, request summary, outcome, and timestamp
