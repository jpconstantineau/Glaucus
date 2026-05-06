# Go + PocketBase Specification

This folder translates the language-agnostic `spec/` into an implementation-ready specification for this repository.

The target system is:

- a Go application built around PocketBase
- a web-first product surface, not a TUI
- cross-platform (`windows`, `linux`, `darwin`) with no `cgo` requirement
- conservative on dependencies and aligned with idiomatic Go design

## Implementation stance

- PocketBase is the embedded backend, persistence layer, admin/auth foundation, and realtime/event distribution substrate.
- Custom product behavior lives in Go services registered from the main application, not in JavaScript hooks.
- The user-facing interactive surface is a browser dashboard and chat UI. CLI remains available for admin, diagnostics, batch, and optional interactive use, but there is no terminal-first TUI.
- Features from the source spec that are not in the first delivery tranche must still be represented here with explicit extension points, data contracts, and gating rules.

## Reading order

1. [01-system-specification.md](/C:/GIT/ChatBase/spec-golang/01-system-specification.md)
2. [02-tool-catalog-and-toolsets.md](/C:/GIT/ChatBase/spec-golang/02-tool-catalog-and-toolsets.md)
3. [03-provider-capabilities-and-routing.md](/C:/GIT/ChatBase/spec-golang/03-provider-capabilities-and-routing.md)
4. [04-messaging-platforms.md](/C:/GIT/ChatBase/spec-golang/04-messaging-platforms.md)
5. [05-api-protocols-and-ui-surfaces.md](/C:/GIT/ChatBase/spec-golang/05-api-protocols-and-ui-surfaces.md)
6. [06-storage-configuration-and-assets.md](/C:/GIT/ChatBase/spec-golang/06-storage-configuration-and-assets.md)
7. [07-security-operations-and-acceptance.md](/C:/GIT/ChatBase/spec-golang/07-security-operations-and-acceptance.md)
8. [08-advanced-features-and-extension-contracts.md](/C:/GIT/ChatBase/spec-golang/08-advanced-features-and-extension-contracts.md)
9. [clarification questions.md](/C:/GIT/ChatBase/spec-golang/clarification%20questions.md)

## Repo-specific assumptions

- The binary entrypoint remains Go-native and owns lifecycle, config loading, migrations, runtime startup, and shutdown.
- Static web assets are served by PocketBase routes from an embedded filesystem or repo-local `web/dist/` directory.
- The web UI is implemented as an HTMX-driven frontend with HTML fragments rendered server-side using Go templates.
- SQLite is provided through PocketBase's bundled stack, preserving the no-`cgo` requirement.
- PocketBase collections are used for durable application state unless a section explicitly calls for filesystem artifacts.
- Realtime updates for runs, sessions, and dashboard status use SSE first; PocketBase realtime or WebSocket channels may be layered on where it reduces complexity.
