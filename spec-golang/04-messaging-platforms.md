# 04. Messaging Platforms

## 1. Common Adapter Contract

Every adapter implements:

- `Start(ctx)`
- `Stop(ctx)`
- `Health(ctx)`
- inbound event normalization
- outbound message send
- outbound media send where supported

Normalized inbound event shape:

- platform
- profile ID
- actor identity
- chat identity
- thread/topic identity
- message content parts
- attachments
- raw event metadata

## 2. Common Gateway Semantics

The gateway is a Go service running inside the main process.

Responsibilities:

- authorization
- session key construction
- active run steering rules
- slash command routing
- home channel delivery
- cron and notification delivery
- reconnect/backoff

Gateway state is durably tracked in PocketBase so the dashboard can show:

- adapter status
- last connect time
- last error
- active auth mode

## 3. Universal Platform Features

Preserve the source spec semantics where the upstream platform allows them.

Implementation rule:

- capability differences are surfaced via adapter capability flags, not scattered `if platform == ...` checks across the runtime

## 4. Platform Catalog

All platforms in the source spec remain in scope.

Delivery strategy for this repo:

- phase 1 runtime-quality adapters: Telegram, Discord, Webhooks, Email
- phase 2 adapters: WhatsApp, Signal, Matrix, Mattermost, SMS
- phase 3 adapters: DingTalk, Feishu/Lark, WeCom, Weixin, BlueBubbles, Home Assistant, Yuanbao, QQ Official Bot

Requirement:

- even phase 2 and 3 platforms must already have stable adapter interfaces, config schema slots, session-key rules, and auth placeholders in the Go spec so later implementation does not require contract redesign

## 5. Authorization and Session Keys

Session key format must be deterministic:

```text
<platform>:<profile>:<chat>:<thread-or-default>:<user-scope>
```

Where:

- `thread-or-default` preserves topic/thread context
- `user-scope` is included only when the platform/session mode requires it

Authorization supports:

- global allow-all
- global allowlist
- per-platform allow-all
- per-platform allowlist
- invite-code or DM pairing

## 6. Extensibility

Messaging adapters are plugin-capable but must still implement the same Go interface and normalized contracts.

## 7. Platform Phasing

The phasing from the source spec is retained.

Web-first contextualization:

- the dashboard must expose adapter setup, health, and logs for all configured messaging platforms
- no platform setup flow may depend on a TUI
- Email setup must include inbox polling or webhook/mailbox configuration, threading behavior, attachment policy, and allowlist controls in the dashboard
