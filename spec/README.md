# Distilled Specification

This folder contains a self-contained, language-agnostic specification for implementing a Hermes-like autonomous agent platform in any modern programming language.

The specification is written so an implementation team does not need the original repository, code, or documentation. It defines product behavior, runtime contracts, data models, APIs, protocols, configuration, security boundaries, and feature catalogs in implementation-ready form.

## Reading Order

1. [01-system-specification.md](C:\GIT\hermes-agent\distilled spec\01-system-specification.md)
2. [02-tool-catalog-and-toolsets.md](C:\GIT\hermes-agent\distilled spec\02-tool-catalog-and-toolsets.md)
3. [03-provider-capabilities-and-routing.md](C:\GIT\hermes-agent\distilled spec\03-provider-capabilities-and-routing.md)
4. [04-messaging-platforms.md](C:\GIT\hermes-agent\distilled spec\04-messaging-platforms.md)
5. [05-api-protocols-and-ui-surfaces.md](C:\GIT\hermes-agent\distilled spec\05-api-protocols-and-ui-surfaces.md)
6. [06-storage-configuration-and-assets.md](C:\GIT\hermes-agent\distilled spec\06-storage-configuration-and-assets.md)
7. [07-security-operations-and-acceptance.md](C:\GIT\hermes-agent\distilled spec\07-security-operations-and-acceptance.md)
8. [08-advanced-features-and-extension-contracts.md](C:\GIT\hermes-agent\distilled spec\08-advanced-features-and-extension-contracts.md)

## What This Specification Covers

- conversational agent runtime
- prompt assembly and context handling
- tool calling and toolset management
- provider resolution and fallback routing
- session persistence, memory, skills, and search
- CLI, TUI, messaging gateway, API server, ACP, and dashboard surfaces
- cron and background automation
- goals, kanban, hooks, credential pools, routing policy, and evaluation surfaces
- plugin and extension categories
- browser, terminal, code execution, voice, vision, and media capabilities
- platform integrations and delivery contracts
- operational and security requirements

## Design Goal

An implementation built from this folder should reproduce the functionality and user-facing behavior of the original system while remaining free to choose its own language, runtime model, frameworks, and internal architecture.
