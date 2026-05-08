package messaging

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookAdapterNormalizesSignedInboundRequest(t *testing.T) {
	adapter := NewWebhookAdapter(WebhookConfig{
		ProfileID:    "default",
		AdapterID:    "adapter_webhook",
		HMACSecret:   "super-secret",
		AllowedCIDRs: []string{"127.0.0.0/8"},
	}, nil)

	body := []byte(`{"actor":{"id":"ext-1","username":"client"},"chat_id":"chat-7","thread_id":"thread-2","text":"hello webhook"}`)
	mac := hmac.New(sha256.New, []byte("super-secret"))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	request := httptest.NewRequest(http.MethodPost, "http://example.test/gateway/webhooks/adapter_webhook", strings.NewReader(string(body)))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Glaucus-Signature", signature)

	event, err := adapter.NormalizeRequest(request, body)
	if err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	if event.ChatID != "chat-7" || event.ThreadID != "thread-2" || event.Actor.ID != "ext-1" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestWebhookAdapterHealthAndSendMessage(t *testing.T) {
	var seenBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	adapter := NewWebhookAdapter(WebhookConfig{
		ProfileID:   "default",
		AdapterID:   "adapter_webhook",
		OutboundURL: server.URL,
		BearerToken: "token-1",
	}, server.Client())

	health, err := adapter.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("unexpected health: %+v", health)
	}

	result, err := adapter.SendMessage(context.Background(), DeliveryTarget{
		ProfileID: "default",
		Platform:  PlatformWebhook,
		ChatID:    "chat-7",
		ThreadID:  "thread-2",
	}, OutboundMessage{Text: "send_message body"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if result.Status != "sent" || !strings.Contains(seenBody, `"text":"send_message body"`) {
		t.Fatalf("unexpected send result/body: %+v %s", result, seenBody)
	}
}
