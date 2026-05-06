# Clarification Questions

Please answer in Q&A form by editing this file in place, for example:

`A1: ...`

Answers reviewed and incorporated into the current `spec-golang` documents.

## Q1. Web frontend architecture

Question:

Should the web UI be:

- a server-rendered HTML app with vanilla JS and no Node build toolchain
- or a richer SPA with a separate frontend build pipeline

Answer:

- server-rendered HTML shell plus vanilla ES modules, SSE-first live updates, and no required Node toolchain
- HTMX front end where HTML fragments are rendered server-side using go templates.


## Q2. Browser auth model

Question:

Is the browser UI intended for:

- a single trusted operator/admin on a local machine
- or multiple authenticated end users with separate browser identities

Answer:

- single trusted operator/admin first, using PocketBase auth for dashboard/API management


## Q3. Messaging platform rollout priority

Question:

Which adapters should be implemented in the first working release?

Answer: 

- Telegram, Discord,generic Webhooks and email inbox 


## Q4. CLI scope

Question:

Should the CLI remain only an admin/ops surface, or should it also keep an interactive chat mode?

Answer:

- CLI remains available for admin/ops plus optional interactive fallback chat, but the primary UX is the web UI


## Q5. Browser automation backend

Question:

Do you want browser automation in the first implementation to prefer:

- an external CDP-compatible browser already installed on the machine
- or a bundled/playwright-style managed browser workflow

Answer: 

- prefer external CDP-compatible browser attachment first to avoid adding heavier runtime dependencies
