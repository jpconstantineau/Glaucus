package messaging

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramAdapterNormalizeInbound(t *testing.T) {
	adapter := NewTelegramAdapter(TelegramConfig{
		ProfileID:            "default",
		AdapterID:            "adapter_telegram",
		PrivateChatUserScope: true,
	}, nil)

	raw := []byte(`{
	  "update_id": 1,
	  "message": {
	    "message_id": 10,
	    "message_thread_id": 77,
	    "text": "hello world",
	    "from": {
	      "id": 123,
	      "is_bot": false,
	      "first_name": "Pierre",
	      "username": "pierre"
	    },
	    "chat": {
	      "id": 456,
	      "type": "private"
	    },
	    "voice": {
	      "file_id": "voice-1",
	      "duration": 8
	    }
	  }
	}`)

	event, err := adapter.NormalizeInbound(raw)
	if err != nil {
		t.Fatalf("normalize inbound: %v", err)
	}
	if event.ChatID != "456" || event.ThreadID != "77" || event.UserScope != "123" {
		t.Fatalf("unexpected normalized event: %+v", event)
	}
	if len(event.Attachments) != 1 || event.Attachments[0].ID != "voice-1" {
		t.Fatalf("expected voice attachment, got %+v", event.Attachments)
	}
}

func TestTelegramAdapterHealthAndSendMessage(t *testing.T) {
	var seenPath string
	var seenBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewTelegramAdapter(TelegramConfig{
		ProfileID: "default",
		AdapterID: "adapter_telegram",
		BotToken:  "secret-token",
		BaseURL:   server.URL,
		Mode:      "webhook",
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
		Platform:  PlatformTelegram,
		ChatID:    "456",
		ThreadID:  "77",
	}, OutboundMessage{
		Text: "reply text",
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if result.MessageID != "99" {
		t.Fatalf("expected message id 99, got %+v", result)
	}
	if !strings.Contains(seenPath, "/botsecret-token/sendMessage") {
		t.Fatalf("unexpected request path %q", seenPath)
	}
	if !strings.Contains(seenBody, `"message_thread_id":77`) {
		t.Fatalf("expected thread id in request body, got %s", seenBody)
	}
}
