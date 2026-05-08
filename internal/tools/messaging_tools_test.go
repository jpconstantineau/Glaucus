package tools

import (
	"context"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/messaging"
)

type stubMessageSender struct {
	target  messaging.DeliveryTarget
	message messaging.OutboundMessage
}

func (s *stubMessageSender) Send(_ context.Context, target messaging.DeliveryTarget, message messaging.OutboundMessage) (messaging.DeliveryResult, error) {
	s.target = target
	s.message = message
	return messaging.DeliveryResult{Status: "sent", MessageID: "msg-1"}, nil
}

func TestSendMessageTool(t *testing.T) {
	sender := &stubMessageSender{}
	tool := SendMessageTool{sender: sender}
	result := tool.Execute(context.Background(), ToolRequest{
		ProfileID: "default",
		Arguments: map[string]any{
			"platform": "webhook",
			"chat_id":  "chat-1",
			"text":     "hello",
		},
	})
	if result.Status != StatusSuccess {
		t.Fatalf("unexpected result: %+v", result)
	}
	if sender.target.Platform != "webhook" || sender.message.Text != "hello" {
		t.Fatalf("unexpected sender invocation: %+v %+v", sender.target, sender.message)
	}
}
