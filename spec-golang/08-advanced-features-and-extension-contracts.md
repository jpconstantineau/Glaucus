# 08. Advanced Features and Extension Contracts

## 1. Persistent Goals

Represent the current goal as structured state on the session plus optional long-lived profile goal records.

Required operations:

- create
- inspect
- update
- clear
- judge/evaluate

## 2. Kanban / Multi-Agent Work Queue

If implemented, store boards/tasks in PocketBase collections and run attempts in normal `agent_runs`.

Required collections:

- `kanban_boards`
- `kanban_tasks`
- `kanban_comments`

This feature is plugin-gatable in the first release, but the data contracts should be fixed up front.

## 3. Hooks System

Expose a typed internal hook bus with explicit registration points.

Rules:

- hooks may observe, transform, or block depending on hook type
- ordering must be deterministic
- blocking hooks must return auditable reasons

## 4. Credential Pools

Represent credential pools as config-defined or persisted provider credential groups.

Required behavior:

- multiple credentials per provider
- rotation on retry-worthy failures
- explicit inheritance rules for subagents

## 5. Provider Routing Policies

Routing policies must be declarative and serializable so they can be edited from config and inspected in the dashboard.

## 6. Fallback Providers

Fallback planning applies to both main and auxiliary tasks.

Persist:

- attempt order
- attempt result
- failover reason

## 7. Batch Processing

Batch execution should be exposed as:

- API endpoints
- CLI commands
- optional dashboard job launcher

Outputs must be machine-readable and resumable.

## 8. Trajectory Format

Define a JSONL export format containing:

- prompt metadata
- conversation turns
- reasoning payload refs or inline content where policy allows
- tool calls/results
- usage
- status

## 9. RL Training and Evaluation

This is optional and plugin-gated, but contracts should remain coherent with batch and trajectory exports.

## 10. Curator and Skill Lifecycle

Curator is a background Go service.

Required behavior:

- usage tracking
- active/stale/archived/pinned states
- recoverable archive
- manual control from dashboard and CLI

## 11. Plugin Surface Categories

Support at least these plugin categories:

- general
- memory backend
- context engine
- image generation backend
- messaging adapter
- dashboard extension
- model provider

Each category must define:

- discovery path
- enablement rules
- multiplicity
- config schema

## 12. Skills Hub and Provenance

If remote skills are supported:

- maintain provenance lock files
- quarantine before activation
- audit installs/updates/removals

## 13. Browser Session Recording and Stealth

These are optional but the browser backend interface must leave room for:

- recording
- stealth flags
- proxy config
- keepalive
- live view

## 14. Themes and Personalization

Because there is no TUI, theming requirements map primarily to the web UI and CLI:

- named dashboard themes
- configurable branding text
- color tokens
- per-profile presentation settings

## 15. Shared Tool Infrastructure

If a shared tool gateway is added later, direct local tools remain the default and authoritative baseline.

The gateway must declare:

- precedence
- fallback rules
- UI visibility

## 16. Built-In Plugin Families

Preserve the source spec plugin-family concepts as discoverable categories, not hardcoded one-off switches.

## 17. Context References

Inline path references from prompts are required.

Safety rules:

- root restriction
- path traversal prevention
- binary rejection
- line-target bounds checking

## 18. Backup, Export, and Migration

Backup/import/export must cover:

- PocketBase records for the relevant collections
- markdown memory and skills artifacts
- config and profile metadata
- safe subsets of secrets only when explicitly requested and permitted
