# 02. Tool Catalog and Toolsets

## 1. Tool Runtime Contract

Every tool must have:

- a unique name
- a toolset membership
- a machine-readable schema
- a human-readable description
- an availability check
- an execution handler
- metadata flags such as interactive, approval-sensitive, or platform-scoped

Tool availability contract:

- a tool may be discovered and registered without being exposed to the model
- a tool is exposed only when it belongs to at least one enabled toolset for the current surface or run
- availability checks must be evaluated separately from toolset membership
- plugin-provided and MCP-provided tools must obey the same exposure rules as built-in tools
- unknown or malformed tool schemas must be rejected rather than partially exposed

Tool execution results must always be returned in a structured envelope that can represent:

- success with payload
- recoverable error
- non-retryable error
- validation failure
- approval required or denied

Additional normalization contract:

- tool-call arguments should be validated against the published schema before execution
- safe type coercion is allowed for common model drift such as numbers or booleans emitted as strings
- malformed tool-call arguments may be repaired conservatively, but unrecoverable input must degrade into a clear validation failure rather than undefined execution

## 2. Built-In Tool Families

### 2.1 File Tools

Required tools:

- `read_file`
- `write_file`
- `patch`
- `search_files`

Required behavior:

- line-aware output for reads
- support for pagination or offsets
- fuzzy or targeted patching rather than whole-file replacement where requested
- path traversal protection
- binary file detection
- sensitive path blocking

### 2.2 Terminal and Process Tools

Required tools:

- `terminal`
- `process`

Required behavior:

- run shell commands
- support cwd override
- optional background execution
- return exit code, stdout, stderr, and timing metadata
- track background processes by session-safe process ids
- support list, poll, log, wait, kill, and stdin write for background processes
- detect dangerous commands unless backend safety policy explicitly disables host-risk checks
- preserve a session-safe process identifier namespace rather than exposing raw operating-system process ids as the only handle
- support a configurable execution backend abstraction including local execution and optional container, SSH, or remote-sandbox execution
- clearly label unsandboxed local execution in product surfaces
- background-process operations must use the session-safe handle namespace for poll, log, wait, stdin write, and kill operations

### 2.3 Browser Tools

Required tools:

- `browser_navigate`
- `browser_snapshot`
- `browser_click`
- `browser_type`
- `browser_scroll`
- `browser_press`
- `browser_back`
- `browser_get_images`
- `browser_vision`
- `browser_console`

Optional but documented tools:

- `browser_cdp`
- `browser_dialog`

Required behavior:

- represent pages as accessibility-tree-like snapshots
- assign stable element references per snapshot
- support screenshots and vision analysis
- expose console and JS error output
- support local and cloud backends
- support element references that are valid only for the snapshot that produced them, or document an equivalent invalidation model
- support live-browser attachment when the product exposes a Chrome DevTools Protocol mode
- support dialog handling where the backend exposes alerts, confirms, or prompts

### 2.4 Web Tools

Required tools:

- `web_search`
- `web_extract`

Required behavior:

- backend-agnostic search
- HTML and PDF extraction
- summarized output for large pages
- support provider-specific filters where available

### 2.5 Code Execution Tool

Required tool:

- `execute_code`

Required behavior:

- allow a script to call Hermes tools programmatically
- provide resource limits
- keep tool-call intermediates out of the main context when possible
- allow conditional and multi-step logic

### 2.6 Scheduling Tool

Required tool:

- `cronjob`

Required behavior:

- unified action-based interface for create, list, update, pause, resume, run, remove
- support attached skills
- support script-backed and script-only jobs
- support schedule parsing and delivery target specification
- support one-shot, interval, and cron-expression schedules
- preserve origin metadata so jobs can optionally deliver back to the originating chat or thread
- preserve per-job toolset overrides separate from global cron defaults
- support an explicit local-only delivery mode and origin-based auto-delivery mode
- support an optional absolute working-directory override for the job so project context and filesystem tools execute relative to that directory
- if a script-only mode is supported, it must allow silent success on empty output while still surfacing script crashes or timeouts as job failures

### 2.7 Delegation Tool

Required tool:

- `delegate_task`

Required behavior:

- spawn isolated subagents
- support single and batch modes
- allow model and toolset override
- preserve parent budgeting and nesting rules
- default to a leaf-worker role that cannot recursively delegate or directly invoke selected shared-state tools
- support an orchestrator role, gated by configuration, that can spawn further children up to a bounded depth
- ensure subagent intermediate tool traces do not inflate the parent model context unless explicitly summarized back

### 2.8 Planning and Memory Tools

Required tools:

- `todo`
- `memory`
- `session_search`

Required behavior:

- task planning list per session
- durable memory writes to agent memory or user profile memory
- long-term cross-session search with optional summarization

Additional behavioral contract:

- the planning tool is session-scoped and non-durable by default, but its active items must survive context-compression events inside the same session
- memory writes must differentiate broad agent memory from user-specific profile memory
- session search should prefer focused summaries over raw transcript dumping when the implementation has an auxiliary summarization model

### 2.9 Skills Tools

Required tools:

- `skills_list`
- `skill_view`
- `skill_manage`

Required behavior:

- discover available skills
- load full skill bodies and linked files on demand
- create, update, and remove user-managed skills
- support supporting files under constrained subdirectories such as references, templates, scripts, and assets
- validate names, paths, and front matter for user-managed skills
- preserve skill provenance and pin/archive protections where the product supports curator-style lifecycle management

### 2.10 Media and Perception Tools

Required tools:

- `vision_analyze`
- `image_generate`
- `text_to_speech`

Required behavior:

- analyze images via vision-capable provider
- generate images using configured image backend
- synthesize audio for supported delivery channels

### 2.11 Messaging and Platform Tools

Required core tool:

- `send_message`

Documented optional platform-scoped tools:

- Discord server tools
- Discord admin tools
- Feishu doc and drive comment tools
- Yuanbao-specific tools
- Spotify tools
- Home Assistant tools
- RL training tools
- Kanban worker tools for durable multi-agent coordination
- platform comment or document tools for adapters such as Feishu/Lark

Implementations may gate these behind platform capabilities or plugins, but their product semantics must be preserved.

### 2.12 MCP Utility Tools

When an MCP server supports prompts or resources, utility tools may be exposed for:

- list resources
- read resource
- list prompts
- get prompt

## 3. Toolsets

### 3.1 Core Toolsets

The system must support named toolsets including at least:

- browser
- clarify
- code_execution
- cronjob
- debugging
- delegation
- discord
- discord_admin
- file
- feishu_doc
- feishu_drive
- homeassistant
- image_gen
- kanban
- memory
- messaging
- moa
- rl
- safe
- search
- session_search
- skills
- spotify
- terminal
- todo
- tts
- vision
- web
- yuanbao

The implementation should also support:

- video
- platform runtime toolsets such as `hermes-cli`, `hermes-cron`, `hermes-acp`, `hermes-api`, `hermes-gateway`, and `hermes-<platform>`

### 3.2 Composite Toolsets

Composite toolsets are bundles of other toolsets for common scenarios, such as:

- debugging = file + terminal + web
- safe = read-only research and analysis set

Composite-toolset contract:

- disabling a toolset must subtract its tools even if they were included transitively by a composite toolset
- implementations should provide a stable resolution algorithm from toolset names to final tool names
- legacy aliases may exist, but the product must define one canonical name per toolset for persistence and API responses
- wildcard aliases such as `all` or `*` may be supported to mean the union of all currently registered toolsets

### 3.3 Platform Toolsets

Each interactive or machine-facing surface must define a base toolset policy.

Examples:

- CLI and TUI typically inherit the default core tool bundle
- messaging adapters inherit a messaging-safe base bundle
- cron runs use a cron-specific bundle that may be narrower than CLI defaults
- ACP or API runs may expose a caller-selected subset

The product must support per-surface enable and disable lists and define clear precedence:

1. start from either the explicit per-run enable-list union, or the surface default bundle when no explicit enable list is supplied
2. subtract any explicit disable-list toolsets
3. apply availability checks so only tools whose runtime requirements pass remain exposed
4. preserve plugin and MCP toolsets under the same rules as built-in toolsets

The system must support platform-specific toolset presets for:

- CLI
- ACP
- API server
- cron
- each messaging platform

### 3.4 Dynamic Toolsets

Dynamic toolsets must be creatable from:

- MCP servers
- plugins
- user-defined custom bundles

### 3.5 Toolset Selection Rules

Support:

- per-session explicit toolsets
- per-platform configured toolsets
- custom toolset bundles
- wildcard `all`
- explicit enable/disable behavior

## 4. Tool Availability

Availability decisions may depend on:

- config
- environment variables
- installed binaries
- provider credentials
- live external integrations
- platform mode

Unavailable tools must not be advertised to the model unless the product intentionally wants discoverable-but-disabled semantics in a UI.

## 5. Tool Safety Rules

- shell tools use dangerous-command approval policy
- file tools enforce path policy
- MCP tools are filtered by server policy and capabilities
- platform tools require correct platform auth and permissions
- tools that modify external systems should integrate with audit logging
