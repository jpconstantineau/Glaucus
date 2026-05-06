# 03. Provider Capabilities and Routing

## 1. Provider Model

Providers are modeled as metadata plus a transport adapter.

Go interface split:

- `ProviderCatalog` resolves manifests and credentials
- `ProviderClient` executes requests for one dialect
- `ProviderRouter` chooses the provider/model pair for a task

## 2. Required API Dialects

Must support:

1. OpenAI Chat Completions
2. OpenAI Responses API
3. Anthropic Messages API

Recommended implementation:

- normalize all outbound requests into internal structs first
- translate internal structs to provider dialects in adapter packages

## 3. Provider Profile

Each provider profile must define:

- stable provider ID
- display name
- default base URL
- auth mode
- dialect
- capability flags
- supported task categories
- timeout defaults
- model discovery mode

Store baseline manifests in repo-managed YAML or JSON under a dedicated providers manifest directory. Sync them into runtime memory at startup; persisting a UI cache in PocketBase is optional.

## 4. Runtime Resolution

For every model call, resolve:

- provider ID
- model ID
- credential source
- base URL
- dialect
- timeout
- stale timeout
- fallback plan
- auxiliary task slot

Persist this resolution snapshot on the run for auditability.

## 5. Credential Sources

Supported sources:

- environment variables
- `config.yaml`
- PocketBase-managed secret records or auth store references
- file-backed OAuth tokens
- credential pools

Rule:

- secrets must never be returned raw from general-purpose dashboard APIs

## 6. Provider Families

The provider abstraction must be data-driven enough to support the source spec families without structural changes.

Hardcoded business logic is allowed only for:

- protocol translation
- auth header formatting
- known provider-specific streaming quirks

## 7. Auxiliary Routing

Define these auxiliary slots at minimum:

- `compression`
- `vision`
- `session_search_summary`
- `web_extract_summary`
- `title_generation`
- `curator`
- `memory_helper`
- `speech_to_text`
- `text_to_speech`

Each slot supports:

- inherit
- explicit provider override
- explicit model override
- auto route by policy

## 8. Fallback Behavior

Fallback must:

- be deterministic
- be bounded
- reuse the same logical turn
- preserve tool-call context and turn IDs

Implementation rule:

- fallback planning is computed once before the first provider request
- each attempt appends a structured attempt record to the run

## 9. Model Catalog

Expose a normalized catalog through:

- dashboard picker API
- `/v1/models`
- CLI status/inspect commands

Each model entry includes:

- canonical ID
- provider ID
- display name
- capabilities
- lifecycle status
- input mode limits when known

## 10. Routing Policy

Support declarative routing fields for:

- preferred providers
- denied providers
- cost bias
- latency bias
- throughput bias
- required capabilities
- fallback order

Policy evaluation belongs in Go code, not in PocketBase collection hooks.

## 11. Image, Voice, and Special Media Backends

Treat image generation, TTS, STT, and vision as first-class provider categories.

Rule:

- do not overload the primary text provider config with media-specific settings when those settings have different auth, quotas, or capabilities
