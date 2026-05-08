package messaging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type MailPoller interface {
	Health(context.Context) error
}

type SendMailFunc func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error

type EmailConfig struct {
	ProfileID        string
	AdapterID        string
	IMAPAddress      string
	IMAPUsername     string
	IMAPPassword     string
	SMTPAddress      string
	SMTPUsername     string
	SMTPPassword     string
	FromAddress      string
	PollInterval     time.Duration
	AttachmentPolicy string
	ThreadByHeader   bool
}

type EmailAdapter struct {
	config   EmailConfig
	poller   MailPoller
	sendMail SendMailFunc
}

func NewEmailAdapter(cfg EmailConfig, poller MailPoller, sendMail SendMailFunc) *EmailAdapter {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Minute
	}
	if strings.TrimSpace(cfg.AttachmentPolicy) == "" {
		cfg.AttachmentPolicy = "allow"
	}
	if sendMail == nil {
		sendMail = smtp.SendMail
	}
	return &EmailAdapter{config: cfg, poller: poller, sendMail: sendMail}
}

func (a *EmailAdapter) Platform() string { return PlatformEmail }

func (a *EmailAdapter) Start(context.Context) error { return nil }

func (a *EmailAdapter) Stop(context.Context) error { return nil }

func (a *EmailAdapter) Health(ctx context.Context) (AdapterHealth, error) {
	status := "healthy"
	lastError := ""
	if strings.TrimSpace(a.config.IMAPAddress) == "" || strings.TrimSpace(a.config.SMTPAddress) == "" || strings.TrimSpace(a.config.FromAddress) == "" {
		status = "misconfigured"
		lastError = "email adapter requires IMAP address, SMTP address, and from address"
	} else if a.poller != nil {
		if err := a.poller.Health(ctx); err != nil {
			status = "degraded"
			lastError = err.Error()
		}
	}
	return AdapterHealth{
		Status:          status,
		AuthMode:        "imap_smtp_password",
		LastError:       lastError,
		LastConnectedAt: time.Now().UTC(),
		Capabilities:    []string{"inbound", "outbound", "health", "attachments", "threading"},
		Metadata: map[string]any{
			"poll_interval":     a.config.PollInterval.String(),
			"attachment_policy": a.config.AttachmentPolicy,
			"thread_by_headers": a.config.ThreadByHeader,
		},
	}, nil
}

func (a *EmailAdapter) NormalizeInbound(raw []byte) (InboundEvent, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return InboundEvent{}, fmt.Errorf("read inbound email: %w", err)
	}
	from, err := mail.ParseAddress(message.Header.Get("From"))
	if err != nil {
		return InboundEvent{}, fmt.Errorf("parse from address: %w", err)
	}
	toHeader := firstNonEmpty(message.Header.Get("Reply-To"), message.Header.Get("To"))
	to, _ := mail.ParseAddressList(toHeader)
	chatID := "inbox"
	if len(to) > 0 {
		chatID = strings.ToLower(to[0].Address)
	}
	threadID := firstNonEmpty(message.Header.Get("In-Reply-To"), message.Header.Get("Message-ID"), strings.TrimSpace(message.Header.Get("Subject")))

	text, attachments, err := extractEmailBody(message)
	if err != nil {
		return InboundEvent{}, err
	}
	return InboundEvent{
		Platform:  PlatformEmail,
		ProfileID: a.config.ProfileID,
		AdapterID: a.config.AdapterID,
		Actor: Actor{
			ID:          strings.ToLower(from.Address),
			DisplayName: from.Name,
			Username:    strings.ToLower(from.Address),
		},
		ChatID:      chatID,
		ThreadID:    threadID,
		UserScope:   strings.ToLower(from.Address),
		Content:     []ContentPart{{Type: "input_text", Text: strings.TrimSpace(text)}},
		Attachments: attachments,
		Metadata: map[string]any{
			"subject":           message.Header.Get("Subject"),
			"message_id":        message.Header.Get("Message-ID"),
			"in_reply_to":       message.Header.Get("In-Reply-To"),
			"attachment_policy": a.config.AttachmentPolicy,
		},
	}, nil
}

func (a *EmailAdapter) SendMessage(ctx context.Context, target DeliveryTarget, message OutboundMessage) (DeliveryResult, error) {
	if a.sendMail == nil {
		return DeliveryResult{}, fmt.Errorf("smtp sender is not configured")
	}
	recipient := strings.TrimSpace(target.ChatID)
	if recipient == "" {
		return DeliveryResult{}, fmt.Errorf("chat_id must contain the recipient email address")
	}

	subject := stringValue(message.Metadata, "subject")
	if strings.TrimSpace(subject) == "" {
		subject = "Glaucus reply"
	}
	var body strings.Builder
	body.WriteString("From: " + a.config.FromAddress + "\r\n")
	body.WriteString("To: " + recipient + "\r\n")
	body.WriteString("Subject: " + subject + "\r\n")
	if threadID := strings.TrimSpace(target.ThreadID); threadID != "" && a.config.ThreadByHeader {
		body.WriteString("In-Reply-To: " + threadID + "\r\n")
		body.WriteString("References: " + threadID + "\r\n")
	}
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	body.WriteString("\r\n")
	body.WriteString(message.Text)

	auth := smtp.PlainAuth("", a.config.SMTPUsername, a.config.SMTPPassword, smtpHost(a.config.SMTPAddress))
	if err := a.sendMail(a.config.SMTPAddress, auth, a.config.FromAddress, []string{recipient}, []byte(body.String())); err != nil {
		return DeliveryResult{}, err
	}
	_ = ctx
	return DeliveryResult{
		Status: "sent",
		Details: map[string]any{
			"recipient": recipient,
			"subject":   subject,
		},
	}, nil
}

func extractEmailBody(message *mail.Message) (string, []Attachment, error) {
	mediaType, params, _ := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(message.Body, params["boundary"])
		var text strings.Builder
		attachments := []Attachment{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", nil, fmt.Errorf("read multipart email: %w", err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return "", nil, fmt.Errorf("read multipart part: %w", err)
			}
			partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if filename := part.FileName(); filename != "" {
				attachments = append(attachments, Attachment{
					Name:     filename,
					MIMEType: partType,
				})
				continue
			}
			if strings.HasPrefix(partType, "text/plain") {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.Write(data)
			}
		}
		return text.String(), attachments, nil
	}

	data, err := io.ReadAll(message.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read email body: %w", err)
	}
	return string(data), nil, nil
}

func smtpHost(addr string) string {
	host, _, ok := strings.Cut(addr, ":")
	if ok {
		return host
	}
	return addr
}
