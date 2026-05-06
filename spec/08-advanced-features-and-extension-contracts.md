# 08. Advanced Features and Extension Contracts

## 1. Persistent Goals

The system should support a persistent goal mechanism that lets a user define an ongoing objective that survives across turns.

Required behavior:

- create, inspect, update, and clear the current goal
- allow a judge model or rule engine to periodically evaluate whether the goal has been satisfied
- keep goal state across normal conversational turns
- allow user messages to preempt active goal work
- fail open when the judge cannot confidently decide

## 2. Kanban / Multi-Agent Work Queue

The platform should support a durable multi-agent task board for collaborative or role-based execution.

Required capabilities:

- create boards
- create tasks
- assign tasks
- link dependent tasks
- comment on tasks
- block and unblock tasks
- archive tasks or boards
- track run attempts per task
- support dispatcher and worker roles
- preserve worker context handoff between attempts

This feature is separate from lightweight delegation because it is durable, queue-backed, and multi-profile or multi-worker aware.

## 3. Hooks System

The platform should support event hooks at three levels:

1. gateway event hooks
2. plugin lifecycle hooks
3. shell or local automation hooks

Required hook events may include:

- session start
- session end
- session finalize
- session reset
- before tool call
- after tool call
- before model call
- after model call
- before API request
- after API request
- subagent stop
- gateway startup
- command execution
- approval request and response
- transformation of tool results or terminal output

Hooks must run with clear safety and ordering rules.

Recommended ordering rules:

- observational hooks may run before and after tool or model calls
- transformation hooks, if supported, must run after the underlying tool call finishes but before the result is appended back into model context
- a hook that blocks an operation must do so explicitly and produce an auditable reason

## 4. Credential Pools

The platform should support multiple credentials for the same provider or endpoint.

Required behavior:

- store multiple credentials per provider or custom endpoint
- rotate on rate limit or auth exhaustion when appropriate
- support interactive management
- share or inherit pools into subagents where configured
- preserve thread safety or concurrency safety

Credential pools should be usable by:

- primary model calls
- auxiliary tasks
- delegated subagents where allowed
- plugin-provided provider backends where explicitly supported

## 5. Provider Routing Policies

The platform should support declarative provider-routing policies for dynamic selection among models or providers.

Routing policies may optimize for:

- cost
- speed
- throughput
- provider allowlist
- provider denylist
- preferred order
- required provider capabilities or parameters

## 6. Fallback Providers

Fallback and routing are related but distinct:

- routing decides what to pick up front
- fallback decides what to do after failure

The system should support both for primary turns and selected auxiliary tasks.

Auxiliary routing slots should at minimum cover:

- context compression
- title generation
- session-search summarization
- vision
- curator review
- embeddings or similarity tasks when the product exposes them

## 7. Batch Processing

The platform should support batch execution over many prompts for:

- trajectory generation
- evaluation
- benchmarking
- dataset processing

Required behavior:

- one-shot or resumed batch runs
- configurable toolset distributions
- output in machine-readable trajectory format
- checkpointing
- statistics aggregation
- per-sample error capture

## 8. Trajectory Format

The system should be able to export agent runs in a training-friendly trajectory format containing:

- normalized conversation turns
- system prompt or tool contract metadata
- reasoning content where available
- tool calls
- tool outputs
- completion status
- usage statistics
- batch metadata when applicable

## 9. RL Training and Evaluation Surfaces

The platform should support, either natively or through optional modules:

- training environment discovery
- training run launch
- training run monitoring
- training run stop
- result retrieval
- benchmark evaluation
- inference testing

This feature may be implemented through optional integrations, but the product surface and data contracts should remain coherent.

## 10. Curator and Skill Lifecycle

The platform should support a background skill-maintenance subsystem for agent-created skills.

Required behavior:

- track usage and activity metadata per skill
- distinguish active, stale, archived, and pinned states
- auto-transition only agent-created skills
- never hard-delete automatically; archive must be recoverable
- allow manual status, run, pause, resume, pin, unpin, archive, restore, backup, and rollback operations
- run on an inactivity or interval policy rather than requiring the main interactive agent loop to carry the logic inline

## 11. Plugin Surface Categories

The extension system should distinguish multiple plugin categories rather than one undifferentiated loader.

Recommended categories:

- general plugins that add hooks, tools, or CLI commands
- memory-provider plugins
- context-engine plugins
- image-generation backend plugins
- messaging-platform plugins
- dashboard or web-surface plugins
- model-provider plugins

The specification must define, for each category:

- discovery location
- enablement rules
- whether multiple implementations may be active simultaneously
- whether the category is additive, backend-like, or exclusive

Recommended category semantics:

- general plugins are additive and may register hooks, tools, or commands simultaneously
- memory-provider plugins are backend-like and normally one provider is active at a time, although multiple providers may be orchestrated internally by a memory manager
- context-engine plugins are replacement-style backends
- image-generation backends are backend-like and selected by routing policy or explicit config
- messaging-platform plugins are additive per platform

## 12. Skills Hub and Provenance

If the product supports remote or optional skill catalogs, it should define:

- trusted and community sources
- provenance lock files
- install/update/remove audit logs
- bundle quarantine before activation
- path normalization to block archive traversal
- source adapters for official bundled optional skills and remote repositories

## 13. Browser Session Recording and Stealth

Where browser providers support them, the platform should preserve:

- session recording
- stealth or anti-detection modes
- residential proxy options
- keepalive or reconnect behavior
- persistent browser identity modes
- VNC or live-view support

## 14. Themes, Skins, and Personalization

The platform should support:

- named skins or themes for terminal surfaces
- custom user-defined skins
- configurable colors, spinner styles, branding text, prompt symbols, and banners
- live switching in interactive sessions
- inheritance from a default theme when user themes omit fields

Conversational personality should remain independent from visual skinning.

## 15. Tool Gateway or Shared Tool Infrastructure

The product may support a centrally managed tool gateway where some tools are provided through a shared remote service rather than direct local credentials.

If implemented, it must define:

- precedence relative to direct keys
- eligibility and fallback behavior
- visibility in setup and tooling UIs

## 16. Built-In Plugin Families

The product should recognize built-in plugin families such as:

- disk cleanup or maintenance
- observability
- conferencing or meeting agents
- achievements or gamification
- dashboards
- kanban boards
- spotify or media services
- additional messaging platforms

These should be represented as plugin categories rather than hardcoded product special cases.

## 17. Context References

The platform should support inline context references from user prompts that point to local files or paths, with optional line-targeting semantics.

Required safety:

- path traversal prevention
- binary-file rejection
- sensitive-path blocking
- size limits

## 18. Backup, Export, and Migration

The product should support:

- backup of state and profile data
- export and import of profiles
- export and import of sessions
- migration from predecessor systems where relevant

Migration features should preserve user memories, skills, selected configuration, messaging setup, and safe subsets of secrets where explicitly allowed.
