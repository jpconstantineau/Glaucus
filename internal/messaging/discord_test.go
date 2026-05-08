package messaging

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscordAdapterNormalizeInbound(t *testing.T) {
	adapter := NewDiscordAdapter(DiscordConfig{
		ProfileID:      "default",
		AdapterID:      "adapter_discord",
		ApplicationID:  "app-1",
		RequireMention: true,
	}, nil)

	raw := []byte(`{
	  "id": "msg-1",
	  "channel_id": "chan-1",
	  "guild_id": "guild-1",
	  "content": "hello from discord",
	  "thread_id": "thread-9",
	  "author": {
	    "id": "user-1",
	    "username": "pierre",
	    "global_name": "Pierre"
	  },
	  "member": {
	    "nick": "Pierre N",
	    "roles": ["role-1"]
	  },
	  "mentions": [
	    {
	      "id": "app-1",
	      "username": "bot-name"
	    }
	  ],
	  "attachments": [
	    {
	      "id": "att-1",
	      "filename": "image.png",
	      "content_type": "image/png",
	      "url": "https://cdn.example/image.png"
	    }
	  ]
	}`)

	event, err := adapter.NormalizeInbound(raw)
	if err != nil {
		t.Fatalf("normalize inbound: %v", err)
	}
	if event.ChatID != "chan-1" || event.ThreadID != "thread-9" {
		t.Fatalf("unexpected normalized event: %+v", event)
	}
	if len(event.Attachments) != 1 || event.Attachments[0].MIMEType != "image/png" {
		t.Fatalf("unexpected attachments: %+v", event.Attachments)
	}
}

func TestDiscordAdapterHealthAndSendMessage(t *testing.T) {
	var seenAuth string
	var seenPath string
	var seenBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.String()
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/users/@me"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"bot-1"}`))
		case strings.Contains(r.URL.Path, "/channels/chan-1/messages"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"discord-message-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewDiscordAdapter(DiscordConfig{
		ProfileID: "default",
		AdapterID: "adapter_discord",
		BotToken:  "discord-token",
		BaseURL:   server.URL,
	}, server.Client())

	health, err := adapter.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("expected healthy status, got %+v", health)
	}

	result, err := adapter.SendMessage(context.Background(), DeliveryTarget{
		ProfileID: "default",
		Platform:  PlatformDiscord,
		ChatID:    "chan-1",
		ThreadID:  "thread-9",
	}, OutboundMessage{Text: "discord reply"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if result.MessageID != "discord-message-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if seenAuth != "Bot discord-token" {
		t.Fatalf("expected bot auth header, got %q", seenAuth)
	}
	if !strings.Contains(seenPath, "thread_id=thread-9") || !strings.Contains(seenBody, `"content":"discord reply"`) {
		t.Fatalf("unexpected request path/body: %s %s", seenPath, seenBody)
	}
}
