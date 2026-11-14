// Package mailer sends transactional email (password reset, notifications).
package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Log writes emails to the log instead of sending them. It must only be used
// in development and tests (enforced by the caller's config validation).
type Log struct{ L *slog.Logger }

func (l Log) Send(_ context.Context, m Message) error {
	l.L.Warn("DEV MAILER: email not sent, logged instead", "to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}

// SMTP sends mail through an SMTP relay with STARTTLS (required when not localhost).
type SMTP struct {
	Addr     string // host:port
	Username string
	Password string
	From     string
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("mailer: header injection rejected")
	}
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("mailer: invalid SMTP address: %w", err)
	}
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		s.From, m.To, m.Subject, time.Now().UTC().Format(time.RFC1123Z), m.Text)

	done := make(chan error, 1)
	go func() { done <- smtp.SendMail(s.Addr, auth, s.From, []string{m.To}, []byte(body)) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("mailer: send: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
