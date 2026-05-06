# 03. Provider Capabilities and Routing

## 1. Provider Model

A provider is any model-serving backend that can fulfill one or more of:

- text generation
- tool calling
- reasoning output
- streaming
- vision input
- image generation
- speech synthesis

The provider system must be metadata-driven and transport-aware.

## 2. Required API Dialects

The implementation must support at least these provider request/response dialects:

1. OpenAI-style Chat Completions
2. OpenAI-style Responses API
3. Anthropic-style native messages API

Additional dialects may exist behind provider adapters.

## 3. Provider Profile

A provider profile must define:

- provider id
- display name
- default base URL
- auth modes
- request dialect
- capability flags
- model-discovery behavior
- fallback hints
- timeout defaults
- provider-specific auth metadata needed for setup and UI rendering

## 4. Runtime Resolution

For each call path, runtime resolution must produce:

- provider id
- model id
- auth material or token reference
- base URL
- request dialect
- request timeout
- stale timeout
- fallback plan

## 5. Credential Sources

Supported credential sources:

- environment variables
- config fields
- file-backed OAuth or auth stores
- credential pools for rotation/retry
- plugin-provided auth metadata

## 6. Provider Families To Model

The documented product requires compatibility with families such as:

- OpenAI-compatible custom endpoints
- OpenRouter
- Anthropic
- OpenAI Codex / Responses
- Gemini / Google
- Bedrock
- Azure-hosted providers
- DeepSeek
- Hugging Face
- Nous-hosted providers
- xAI
- other providers represented by metadata-driven manifests

The exact list may evolve; the abstraction must not hardcode business logic into one monolith.

## 7. Auxiliary Routing

Auxiliary tasks may use provider routing separate from the main conversation model.

Auxiliary tasks include:

- context compression summaries
- vision
- session-search summarization
- web extraction summarization
- title generation
- skills or maintenance flows
- memory flush helpers
- curator review
- embeddings or search helpers where implemented

Each auxiliary task category should allow:

- inherit main provider
- auto-select provider
- explicit provider override
- explicit model override
- explicit base URL override

## 8. Fallback Behavior

The implementation must support fallback providers or models when:

- auth refresh fails initially but retry is possible
- provider returns transient errors
- provider returns non-retryable but failover-worthy errors such as quota exhaustion or unsupported model

Fallback must:

- be deterministic
- avoid infinite loops
- preserve logical turn continuity

## 9. Model Catalog

The system should expose a coherent model catalog that supports:

- static known models
- live provider model discovery
- normalized provider-prefixed model ids
- capability flags such as tool-calling or vision support
- human-friendly labels suitable for pickers in CLI, dashboard, and editors

## 10. Provider Routing Policy

The implementation should allow policy-based selection using concepts such as:

- prefer lowest cost
- prefer lowest latency
- prefer highest throughput
- allow only selected providers
- avoid selected providers
- ordered preference with fallback

## 11. Image, Voice, and Special Media Backends

The provider system should also accommodate:

- image generation backends
- TTS backends
- STT backends
- vision-capable models

These may share auth or config concepts with text providers but should remain separate capability categories.
