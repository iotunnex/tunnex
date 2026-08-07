// Package mail provides a pluggable mailer used by the local-auth flows
// (email verification, password reset — S2.1).
//
// Selection — ⛔ SMTP_HOST SET MEANS SEND, AND NOTHING ELSE MAY OVERRIDE THAT (S12.13 D1):
//   - No SMTP host configured   -> disabledMailer: REFUSES with ErrNotConfigured and logs the recipient
//     and subject (never the body — it carries links). ⛔ It used to log the whole message and return nil,
//     so a deployment with no mail reported success on every invitation it silently dropped.
//   - SMTP host configured      -> SMTPMailer.
//   - SMTP host + MAIL_DEV_LOG  -> the SMTP mailer wrapped to ALSO log. It still sends. The tee has never
//     suppressed delivery and must never be readable as if it did.
//
// ⛔ DevLogging IS ITS OWN VARIABLE (MAIL_DEV_LOG), NOT A CONSEQUENCE OF TUNNEX_ENV. It used to be
// `!IsProduction()`, which meant a variable about the KIND of deployment silently governed mail behaviour —
// so a correctly-configured rig produced a mailer labelled `smtp+log` and a log line reading
// `email_not_sent_logged`, and the operator reasonably concluded mail was off. It was sending the whole
// time. ONE FLAG MUST NOT GOVERN TWO UNRELATED THINGS.
//
// ⚠ AND EVERY MAILER NOW NAMES ITS DESTINATION rather than its capability. `smtp+log` read as "SMTP AND
// log" — which is what it did — but the `+` invited "SMTP plus a log copy" and "log instead of SMTP"
// equally, and the reader who guessed wrong had no way to tell.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// Message is one outgoing email.
//
// ⛔ TEXT IS NOT OPTIONAL AND HTML IS. Every message must carry a working plaintext body: it is what a
// screen reader announces, what a text client shows, what MAIL_DEV_LOG tees, and what survives a client
// that refuses HTML. An invitation whose link exists only inside an <a href> is unreachable to all four.
type Message struct {
	To      string
	Subject string
	Text    string
	// HTML is the branded alternative. Empty means send plaintext only — which is what every caller did
	// before S12.14, and what buildRFC822 still produces byte-for-byte when it is empty.
	HTML string
}

// Mailer sends messages. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
	// Kind returns a short label for logging/diagnostics.
	Kind() string
}

// Config controls mailer selection.
type Config struct {
	Host     string
	Port     string
	From     string
	Username string // optional; empty => no SMTP auth (e.g. Mailpit)
	Password string // optional
	// DevLogging tees a copy of every message to the log. It NEVER suppresses the send. Opt-in via
	// MAIL_DEV_LOG; see the package doc for why it is not derived from the environment name.
	DevLogging bool
	// BaseURL is the DEPLOYMENT's public URL (APP_BASE_URL), used to resolve the branded template's logo.
	//
	// ⛔ IT LIVES ON THE MAILER, NOT ON EACH CALLER, AND THAT IS THE POINT. Stamping it at the four send
	// sites meant a fifth sender could forget — and mfa.Service, which has no base URL at all, already
	// would have. One place, applied to every message, impossible to omit.
	BaseURL string
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
	smtpMailer := &SMTPMailer{cfg: cfg, logger: logger}
	var m Mailer = smtpMailer
	if cfg.DevLogging {
		m = &teeMailer{primary: smtpMailer, log: &LogMailer{logger: logger, reason: "MAIL_DEV_LOG"}}
	}
	return &brandedMailer{inner: m, baseURL: cfg.BaseURL}
}

// brandedMailer resolves the branded template's logo against THIS deployment before delegating.
//
// ⛔ IT WRAPS EVERY MAILER RATHER THAN LIVING IN THE TEMPLATES, so a message cannot reach the wire with an
// unresolved logo no matter which service sent it. The templates stay pure — same inputs, same bytes — and
// the one deployment-specific value is applied at the only seam that always runs.
type brandedMailer struct {
	inner   Mailer
	baseURL string
}

func (m *brandedMailer) Kind() string { return m.inner.Kind() }

func (m *brandedMailer) Send(ctx context.Context, msg Message) error {
	return m.inner.Send(ctx, WithBaseURL(msg, m.baseURL))
}

// Destination names WHERE MAIL ACTUALLY GOES, in one sentence an operator can act on (S12.13 D2).
//
// ⛔ IT REPLACES Kind() AT THE BOOT LINE, because Kind() answered a different question. `smtp+log` is a
// truthful description of the MECHANISM and a terrible answer to "will my invitation arrive" — the `+`
// reads as capability, and the operator who read it as "log instead of SMTP" had no way to find out
// otherwise except by not receiving an email.
func Destination(cfg Config) string {
	if !Configured(cfg) {
		return "log only — no SMTP_HOST is set, so mail is DISABLED and every send will be refused"
	}
	dest := cfg.Host + ":" + cfg.Port
	if cfg.DevLogging {
		return dest + " — and MAIL_DEV_LOG is on, so a copy of every message INCLUDING ITS BODY " +
			"(invitation, verification and reset links work) is written to this log"
	}
	return dest
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

// Send writes the message to the log.
//
// ⛔ THE EVENT NAME IS `email_copied_to_log`, NOT `email_not_sent_logged`, AND THE OLD NAME COST A SESSION.
// This type is used two ways: alone (nothing is sent) and INSIDE THE TEE (the message is sent immediately
// afterwards). The old name was true for the first and a flat lie for the second, and the second is the one
// an operator with working SMTP meets. A founder read "email_not_sent" on a correctly-configured rig and
// concluded mail was disabled; it had already left.
//
// > **A LOG LINE THAT NAMES AN OUTCOME MUST BE TRUE IN EVERY CONTEXT THE LINE CAN BE REACHED FROM.**
// > "Copied to log" is true in both. "Not sent" was true in one and diagnostic poison in the other.
//
// ⚠ IT LOGS THE BODY, WHICH IS THE POINT AND ALSO THE RISK. Invitation, verification and reset bodies are
// working links, so this is a credential in a shipped, searchable log — the class already ruled on for the
// bootstrap password. That is why MAIL_DEV_LOG is opt-in, defaults off, and is named at boot.
func (m *LogMailer) Send(_ context.Context, msg Message) error {
	m.logger.Info("email_copied_to_log",
		slog.String("reason", m.reason),
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
		slog.String("body", msg.Text),
		slog.String("warning", "this body contains a working link; MAIL_DEV_LOG must not be set on a deployment"),
	)
	return nil
}

// SMTPMailer sends via an SMTP server. Auth is used only when a username is set.
//
// ⚠ PORT 587, NOT 465. net/smtp dials PLAINTEXT and upgrades via STARTTLS when the server advertises it;
// it has no implicit-TLS path, so an SMTPS port (465) hangs or errors. Recorded here because it is a
// property of the standard library, not of this configuration, and cannot be fixed by an env var.
type SMTPMailer struct {
	cfg    Config
	logger *slog.Logger
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
	// ⛔ SUCCESS IS AS VISIBLE AS FAILURE, AND UNTIL NOW ONLY FAILURE LOGGED (S12.13 D2). That made an empty
	// log mean BOTH "it worked" and "it never tried", which is not a diagnosis — it is a coin flip an
	// operator performs at the exact moment they are least able to check.
	//
	// ⚠ WHAT IT CLAIMS IS EXACTLY WHAT HAPPENED AND NO MORE: the server ACCEPTED the message. Acceptance is
	// not inbox delivery — SPF/DKIM alignment, reputation and the recipient's own filters all act after
	// this point, and the provider's outbound log is the authority on those. A line reading "delivered"
	// would be the swallowed-error shape rebuilt in the other direction.
	//
	// ⚠ NO BODY, ever. Recipient and subject only, the same line disabledMailer draws — this one runs on
	// every deployment, so it must be safe on every deployment.
	if m.logger != nil {
		m.logger.Info("email_accepted_by_provider",
			slog.String("to", msg.To),
			slog.String("subject", msg.Subject),
			slog.String("server", addr),
			slog.String("means", "the SMTP server accepted the message for delivery; whether it reaches the "+
				"inbox is the provider's outbound log to answer, not this one"))
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

// buildRFC822 renders the wire message.
//
// ⚠ multipart/alternative ONLY WHEN THERE IS AN ALTERNATIVE. With no HTML this produces exactly the bytes
// it produced before S12.14 — a plain text/plain message — so adding the branded path could not change
// what an unbranded caller sends.
//
// ⚠ TEXT COMES FIRST INSIDE THE MULTIPART, which is not cosmetic: RFC 2046 orders alternatives
// least-to-most preferred, so a client picks the LAST part it understands. Reversing them serves plaintext
// to clients that could have rendered the branded version.
func buildRFC822(from string, msg Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	if msg.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		b.WriteString("\r\n")
		b.WriteString(msg.Text)
		return []byte(b.String())
	}
	// A FIXED BOUNDARY, and it is safe because both parts are generated by this repo: the templates escape
	// user input into HTML entities, so no interpolated value can reproduce this string. A random boundary
	// would make every rendered message differ from the last and the renderer untestable, which is the same
	// trade the dropped CSP nonce lost.
	const boundary = "tunnex-alt-boundary-8f2c1d"
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(msg.Text)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(msg.HTML)
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String())
}
