# 04. Messaging Platforms

## 1. Common Adapter Contract

Every messaging adapter must implement:

- connect
- disconnect
- receive inbound event
- normalize to common message event
- send outbound message
- send media when supported
- optional edit/update/reaction affordances when supported
- health and reconnect behavior

## 2. Common Gateway Semantics

The messaging gateway must support:

- user authorization
- session-key construction per platform and chat/thread
- running-agent interruption or queue behavior
- slash command routing
- home channel delivery
- background or cron delivery
- platform-specific policy such as mention gating
- interruption, queueing, or steering semantics when a chat sends input during an active run
- restart and shutdown notifications where the platform supports outbound messaging
- per-platform user authorization plus optional global authorization rules
- durable pairing or invite-code authorization for platforms that cannot be preconfigured with static user ids
- graceful downgrade for platforms that cannot edit streamed messages: they may send whole-message replies instead of live partial updates

## 3. Universal Platform Features

Where the platform supports them, the system should preserve:

- DM vs group/channel session semantics
- thread/topic-aware sessions
- mention-required mode
- free-response channel configuration
- per-channel prompts
- per-channel skill binding
- reactions
- typing indicators
- read receipts
- file/media attachments
- voice transcription
- audio delivery
- message chunking
- home-channel or home-room binding for notifications
- delivery target references that can include both chat identity and thread or topic identity

## 4. Platform Catalog

### 4.1 Telegram

Required behavior:

- bot token auth
- DM and group support
- privacy-mode-aware group behavior
- webhook or polling mode
- voice messages
- topic or forum-aware session support
- optional topic-per-session mode for private-chat workflows
- multi-session DM mode
- home channel delivery
- reactions and rendering differences

### 4.2 Discord

Required behavior:

- bot token auth
- guild and DM support
- privileged intents requirement
- mention and free-response configuration
- thread and forum support
- optional auto-threading
- voice message or voice-channel related integrations where implemented
- role-based access control support

### 4.3 Slack

Required behavior:

- bot token and app-level connection model
- channel and DM support
- slash commands
- mention and free-response behavior
- multi-workspace support
- per-channel prompts and skill bindings

### 4.4 WhatsApp

Required behavior:

- supported auth mode
- DM or group policy
- session persistence
- re-pairing behavior
- message formatting and chunking
- voice messages

### 4.5 Signal

Required behavior:

- daemon or bridge-based connection
- DM and group handling
- attachment support
- quoting/reactions if available
- health monitoring

### 4.6 Matrix

Required behavior:

- homeserver auth
- DM and room support
- optional E2EE support
- proxy/host-helper pattern if needed by implementation
- home room support

### 4.7 Mattermost

Required behavior:

- bot auth
- channel and DM sessions
- optional thread reply mode
- mention gating
- home channel support

### 4.8 Email

Required behavior:

- inbound polling or webhook/mailbox integration
- reply threading
- attachment handling
- skip-attachment or filter policies

### 4.9 SMS

Required behavior:

- webhook-driven inbound messages
- SMS-safe output formatting
- explicit allowlist support
- signature validation

### 4.10 DingTalk

Required behavior:

- app auth
- robot capability
- group mention or response gating
- home channel support
- interactive-card or response affordances where supported

### 4.11 Feishu / Lark

Required behavior:

- WebSocket or webhook mode
- user allowlists
- bot identity behavior
- document-comment intelligent reply path
- comment and drive tool support
- interactive cards
- batching and rate limiting

### 4.12 WeCom

Required behavior:

- self-built app or AI bot behavior depending variant
- callback or stream style integration
- access policies for DM and groups
- media decryption or handling where needed
- deduplication and reconnect behavior

### 4.13 Weixin

Required behavior:

- long-poll connection behavior
- token persistence
- markdown/text compatibility
- message chunking
- typing indicators
- access policies

### 4.14 BlueBubbles

Required behavior:

- iMessage-style bridge server auth
- rich media
- tapback reactions
- typing indicators
- chat addressing

### 4.15 Home Assistant

Required behavior:

- conversational gateway mode
- event-driven gateway integration
- smart-home tools

### 4.16 Webhooks

Required behavior:

- route definitions
- signature validation
- idempotency
- direct delivery mode
- dynamic subscriptions
- webhook-to-agent workflows

### 4.17 Yuanbao

Required behavior:

- bot auth
- DM/group policies
- media uploads
- home channel routing
- cross-platform messaging compatibility

### 4.18 QQ Official Bot

Required behavior:

- official-bot auth
- DM and group support where the upstream platform permits it
- message chunking and formatting compatibility
- allowlist-based access control
- compatibility with the shared gateway command and delivery model

## 5. Authorization and Session Keys

The gateway must normalize each inbound event into a stable session key. The session-key contract must account for:

- platform
- user identity where relevant
- chat or room identity
- thread, forum topic, or reply-chain identity where relevant
- optional multi-session overlays such as Telegram topic mode

Additional session-key contract:

- for thread-aware platforms, delivery metadata must preserve both the parent chat identity and the thread or topic identity so outbound messages can route back to the same lane
- for private-chat multi-session modes, session-key construction may add a synthetic thread or topic layer as long as it remains stable and user-visible

Authorization must support:

- global allow-all
- global allowlist
- per-platform allow-all
- per-platform allowlists
- invite-code or DM-pairing style onboarding where supported

## 6. Extensibility

Additional messaging platforms may be implemented through the platform-plugin system. The core specification only requires the platforms represented in the current codebase; other adapters must be documented as optional extensions rather than implied baseline requirements.

## 7. Platform Phasing

If implementation must be phased, recommended order is:

1. Telegram, Discord, Slack, Webhooks
2. WhatsApp, Signal, Matrix, Mattermost, Email, SMS
3. Feishu, DingTalk, WeCom, Weixin, BlueBubbles, Home Assistant, QQ Official Bot, Yuanbao

This phasing does not remove the requirement to preserve the full documented product surface eventually.
