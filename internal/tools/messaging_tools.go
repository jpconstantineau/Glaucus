package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/messaging"
)

type MessageSender interface {
	Send(context.Context, messaging.DeliveryTarget, messaging.OutboundMessage) (messaging.DeliveryResult, error)
}

type SendMessageTool struct {
	sender MessageSender
}

func RegisterMessagingTools(registry *Registry, sender MessageSender) {
	if registry == nil {
		return
	}
	registry.Register(SendMessageTool{sender: sender})
}

func (SendMessageTool) Definition() ToolDefinition { return mustDefinition("send_message") }

func (t SendMessageTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	if t.sender == nil {
		return AvailabilityResult{Available: false, Reason: "no messaging gateway is configured"}
	}
	return AvailabilityResult{Available: true}
}

func (t SendMessageTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.sender == nil {
		return fatalResult("messaging gateway is unavailable", started)
	}

	text, ok := getStringArg(req.Arguments, "text")
	if !ok || strings.TrimSpace(text) == "" {
		return validationResult("text is required", started)
	}
	chatID, ok := getStringArg(req.Arguments, "chat_id")
	if !ok || strings.TrimSpace(chatID) == "" {
		return validationResult("chat_id is required", started)
	}
	platform, _ := getStringArg(req.Arguments, "platform")
	adapterID, _ := getStringArg(req.Arguments, "adapter_id")
	threadID, _ := getStringArg(req.Arguments, "thread_id")
	userScope, _ := getStringArg(req.Arguments, "user_scope")

	result, err := t.sender.Send(ctx, messaging.DeliveryTarget{
		AdapterID: adapterID,
		Platform:  platform,
		ProfileID: req.ProfileID,
		ChatID:    chatID,
		ThreadID:  threadID,
		UserScope: userScope,
	}, messaging.OutboundMessage{Text: text})
	if err != nil {
		return fatalResult(fmt.Sprintf("send_message failed: %v", err), started)
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: fmt.Sprintf("Sent message to %s/%s.", firstNonEmpty(platform, "adapter"), chatID),
		Payload:     result,
		Timing:      timingSince(started),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
