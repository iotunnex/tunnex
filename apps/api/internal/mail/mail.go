// Package mail provides a pluggable mailer used by the local-auth flows
// (email verification, password reset — S2.1).
//
// Selection (S0.3):
//   - No SMTP host configured  -> LogMailer: logs the message (and any link) so
//     development works with zero mail infra.
//   - SMTP host configured      -> SMTPMailer (Mailpit in dev, real SMTP in prod).
//   - SMTP host + non-production -> the SMTP mailer is wrapped to ALSO log, so
//     developers can grab links from logs even while mail lands in Mailpit.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// Message is a minimal plaintext email.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer sends messages. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
	// Kind returns a short label for logging/diagnostics.
	Kind() string
}

// Config controls mailer selection.
type Config struct {
	Host       string
	Port       string
	From       string
	Username   string // optional; empty => no SMTP auth (e.g. Mailpit)
	Password   string // optional
	DevLogging bool   // when true, also log messages for convenience
}

// New builds the appropriate Mailer for the given configuration.
func New(cfg Config, logger *slog.Logger) Mailer {
	if strings.TrimSpace(cfg.Host) == "" {
		return &LogMailer{logger: logger, reason: "no SMTP host configured"}
	}
	smtpMailer := &SMTPMailer{cfg: cfg}
	if cfg.DevLogging {
		return &teeMailer{primary: smtpMailer, log: &LogMailer{logger: logger, reason: "dev tee"}}
	}
	return smtpMailer
}

// LogMailer writes messages to the logger instead of sending them.
type LogMailer struct {
	logger *slog.Logger
	reason string
}

func (m *LogMailer) Kind() string { return "log" }

func (m *LogMailer) Send(_ context.Context, msg Message) error {
	m.logger.Info("email_not_sent_logged",
		slog.String("reason", m.reason),
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
		slog.String("body", msg.Text),
	)
	return nil
}

// SMTPMailer sends via an SMTP server. Auth is used only when a username is set.
type SMTPMailer struct {
	cfg Config
}

func (m *SMTPMailer) Kind() string { return "smtp" }

func (m *SMTPMailer) Send(_ context.Context, msg Message) error {
	addr := m.cfg.Host + ":" + m.cfg.Port
	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	body := buildRFC822(m.cfg.From, msg)
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{msg.To}, body); err != nil {
		return fmt.Errorf("smtp send to %s: %w", addr, err)
	}
	return nil
}

// teeMailer sends via the primary mailer and also logs the message.
type teeMailer struct {
	primary Mailer
	log     *LogMailer
}

func (m *teeMailer) Kind() string { return m.primary.Kind() + "+log" }

func (m *teeMailer) Send(ctx context.Context, msg Message) error {
	_ = m.log.Send(ctx, msg)
	return m.primary.Send(ctx, msg)
}

func buildRFC822(from string, msg Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Text)
	return []byte(b.String())
}
