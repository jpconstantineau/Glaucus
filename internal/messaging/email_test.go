package messaging

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
)

type stubPoller struct {
	err error
}

func (s stubPoller) Health(context.Context) error {
	return s.err
}

func TestEmailAdapterNormalizeInbound(t *testing.T) {
	adapter := NewEmailAdapter(EmailConfig{
		ProfileID:        "default",
		AdapterID:        "adapter_email",
		AttachmentPolicy: "allow",
	}, nil, nil)

	raw := []byte(strings.Join([]string{
		"From: Pierre <pierre@example.com>",
		"To: bot@example.com",
		"Subject: Build status",
		"Message-ID: <msg-1@example.com>",
		"In-Reply-To: <thread-1@example.com>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		"hello email",
		"--abc",
		"Content-Type: application/pdf",
		"Content-Disposition: attachment; filename=report.pdf",
		"",
		"binary",
		"--abc--",
		"",
	}, "\r\n"))

	event, err := adapter.NormalizeInbound(raw)
	if err != nil {
		t.Fatalf("normalize inbound: %v", err)
	}
	if event.ChatID != "bot@example.com" || event.ThreadID != "<thread-1@example.com>" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if len(event.Attachments) != 1 || event.Attachments[0].Name != "report.pdf" {
		t.Fatalf("unexpected attachments: %+v", event.Attachments)
	}
}

func TestEmailAdapterHealthAndSendMessage(t *testing.T) {
	var sentAddr string
	var sentFrom string
	var sentTo []string
	var sentMsg string
	send := func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		sentAddr = addr
		sentFrom = from
		sentTo = to
		sentMsg = string(msg)
		return nil
	}

	adapter := NewEmailAdapter(EmailConfig{
		ProfileID:      "default",
		AdapterID:      "adapter_email",
		IMAPAddress:    "imap.example.com:993",
		SMTPAddress:    "smtp.example.com:587",
		SMTPUsername:   "bot@example.com",
		SMTPPassword:   "secret",
		FromAddress:    "bot@example.com",
		ThreadByHeader: true,
	}, stubPoller{}, send)

	health, err := adapter.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("unexpected health: %+v", health)
	}

	result, err := adapter.SendMessage(context.Background(), DeliveryTarget{
		ProfileID: "default",
		Platform:  PlatformEmail,
		ChatID:    "user@example.com",
		ThreadID:  "<thread-1@example.com>",
	}, OutboundMessage{
		Text: "email reply",
		Metadata: map[string]any{
			"subject": "RE: Build status",
		},
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if result.Status != "sent" || sentAddr != "smtp.example.com:587" || sentFrom != "bot@example.com" {
		t.Fatalf("unexpected send result/state: %+v %s %s", result, sentAddr, sentFrom)
	}
	if len(sentTo) != 1 || sentTo[0] != "user@example.com" || !strings.Contains(sentMsg, "In-Reply-To: <thread-1@example.com>") {
		t.Fatalf("unexpected SMTP payload: to=%v msg=%s", sentTo, sentMsg)
	}
}
