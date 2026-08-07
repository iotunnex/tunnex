// Package mail provides a pluggable mailer used by the local-auth flows
// (email verification, password reset — S2.1).
//
// Selection:
//   - No SMTP host configured   -> disabledMailer: REFUSES with ErrNotConfigured and logs the recipient
//     and subject (never the body — it carries links). ⛔ It used to log the whole message and return nil,
//     so a deployment with no mail reported success on every invitation it silently dropped.
//   - SMTP host configured      -> SMTPMailer.
//   - SMTP host + dev logging   -> the SMTP mailer wrapped to ALSO log, so a developer can grab links
//     from logs. ⚠ DevLogging is opt-in and must never be set on a deployment.
package mail

import (
	"context"
	"errors"
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

// ErrNotConfigured is returned by every send on a deployment with no SMTP host.
//
// ⛔ IT IS AN ERROR AND IT USED TO BE `nil`. The disabled mailer logged the message and reported SUCCESS,
// so every invitation, verification link and password reset "sent" and vanished — while the API answered
// 202 and the screen said Sent. Invitations are now the ONLY way anyone joins a deployment, so a silent
// mail failure is a deployment nobody can get into, reporting success on every screen.
var ErrNotConfigured = errors.New("no SMTP host configured — email is disabled on this deployment")

// Configured reports whether this deployment can send mail at all. ⭐ Read at STARTUP and by /meta, so an
// operator learns the state when they install rather than when a recipient does not receive something.
func Configured(cfg Config) bool { return strings.TrimSpace(cfg.Host) != "" }

// New builds the appropriate Mailer for the given configuration.
func New(cfg Config, logger *slog.Logger) Mailer {
	if !Configured(cfg) {
		return &disabledMailer{logger: logger}
	}
	smtpMailer := &SMTPMailer{cfg: cfg}
	if cfg.DevLogging {
		return &teeMailer{primary: smtpMailer, log: &LogMailer{logger: logger, reason: "dev tee"}}
	}
	return smtpMailer
}

// disabledMailer is what a deployment with no SMTP gets. It REFUSES, and says so.
//
// ⚠ IT LOGS THE RECIPIENT AND SUBJECT AND NOT THE BODY. The previous behaviour logged `msg.Text` — which
// carries invitation links, password-reset links and verification links — into `docker compose logs`,
// shipped and searchable. A credential in a log is the class this repo has already ruled on once, for the
// bootstrap password.
type disabledMailer struct{ logger *slog.Logger }

func (m *disabledMailer) Kind() string { return "disabled" }

func (m *disabledMailer) Send(_ context.Context, msg Message) error {
	m.logger.Error("email_not_sent_no_smtp",
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
		slog.String("fix", "set SMTP_HOST/SMTP_PORT/SMTP_FROM (and SMTP_USERNAME/SMTP_PASSWORD if your "+
			"provider needs auth) and restart the api service"))
	return ErrNotConfigured
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
