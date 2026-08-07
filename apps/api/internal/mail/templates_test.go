package mail

import (
	"strings"
	"testing"
)

const acceptURL = "https://vpn.acme.test/accept-invite?token=SECRET-TOKEN"

// TestEveryTemplateCarriesAWorkingPlaintextBody — ⛔ THE HALF THAT SILENTLY DISAPPEARS.
//
// The HTML body is the one a recipient usually sees. The PLAINTEXT is what a screen reader announces, what
// a text client shows, what MAIL_DEV_LOG tees, and what survives a client that strips HTML. A link that
// exists only inside an <a href> is unreachable to all four — and the plaintext half is the twin of the
// SMTP-less delivery path this product ships on purpose.
func TestEveryTemplateCarriesAWorkingPlaintextBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  Message
		link string
	}{
		{"invite", InviteMessage("a@b.test", acceptURL, "Acme"), acceptURL},
		{"resend", ResendInviteMessage("a@b.test", acceptURL, "Acme"), acceptURL},
		{"reset", PasswordResetMessage("a@b.test", "https://x.test/reset-password?token=R"), "https://x.test/reset-password?token=R"},
		{"verify", VerifyEmailMessage("a@b.test", "https://x.test/verify-email?token=V"), "https://x.test/verify-email?token=V"},
		{"account-exists", AccountExistsMessage("a@b.test", "https://x.test/reset-password"), "https://x.test/reset-password"},
	} {
		if tc.msg.Text == "" {
			t.Fatalf("%s: no plaintext body", tc.name)
		}
		if !strings.Contains(tc.msg.Text, tc.link) {
			t.Fatalf("%s: the plaintext must carry the URL IN FULL — a recipient reading text has no <a href> "+
				"to follow:\n%s", tc.name, tc.msg.Text)
		}
		// ⚠ AND THE HTML CARRIES IT AS TEXT TOO, beside the button. Some clients render a styled anchor as
		// bare label with no target, and a recipient who distrusts buttons in email is exactly the recipient
		// a security product must still be able to serve.
		if !strings.Contains(tc.msg.HTML, tc.link) {
			t.Fatalf("%s: the HTML must show the URL, not only link it", tc.name)
		}
		if tc.msg.Subject == "" {
			t.Fatalf("%s: no subject", tc.name)
		}
	}
	// The MFA notice deliberately has NO link — see its doc comment. It must still have both bodies.
	m := MFAResetMessage("a@b.test")
	if m.Text == "" || m.HTML == "" {
		t.Fatal("the MFA reset notice needs both bodies even though it carries no link")
	}
	if strings.Contains(m.HTML, "<a href=\"http") {
		t.Fatal("a security-alert email must not teach the recipient to click a link — that is the shape a " +
			"phishing copy of it takes")
	}
}

// TestUserInputIsEscapedInHTMLAndRawInText — ⛔ THE INVARIANT A PORT LOSES FIRST.
//
// The reference's own comment says "never user input without escaping there". renderShell cannot tell an
// intended <strong> from an injected <script>, so escaping belongs at the template, where the value's
// origin is known. This drives an org name — a field an admin types — all the way through.
//
// ⚠ AND THE PLAINTEXT MUST NOT BE ESCAPED. Entities in plain text are the bug, not the fix: a reader of
// the text half should see the org's actual name, not `&lt;script&gt;`.
func TestUserInputIsEscapedInHTMLAndRawInText(t *testing.T) {
	hostile := `<script>alert('x')</script>`
	m := InviteMessage("a@b.test", acceptURL, hostile)

	if strings.Contains(m.HTML, "<script>") {
		t.Fatal("an org name reached the HTML body unescaped — renderShell trusts its input, so a template " +
			"that forgets escapeHTML is the whole vulnerability")
	}
	if !strings.Contains(m.HTML, "&lt;script&gt;") {
		t.Fatalf("the org name must appear ESCAPED rather than dropped:\n%s", m.HTML)
	}
	if !strings.Contains(m.Text, hostile) {
		t.Fatal("the plaintext half must carry the name verbatim — HTML entities in plain text are a defect")
	}
}

// TestEscapeOrderDoesNotDoubleEscape — & must be replaced first or every other entity is mangled.
func TestEscapeOrderDoesNotDoubleEscape(t *testing.T) {
	if got := escapeHTML("a<b&c"); got != "a&lt;b&amp;c" {
		t.Fatalf("escapeHTML mangled its own output: %q", got)
	}
}

// TestLogoIsResolvedAgainstTheDeploymentNotTunnexIO — the port's ONE deliberate divergence.
//
// ⛔ THE REFERENCE HARD-CODES https://tunnex.io/email/tunnex-logo-2x.png. That is right for a site we run
// and wrong for software other people run: every invitation a customer sends would fetch an image from us —
// a phone-home on the most private mail the product produces, from a control plane whose entire pitch is
// that it never contacts us, and a broken image on an air-gapped deployment.
func TestLogoIsResolvedAgainstTheDeploymentNotTunnexIO(t *testing.T) {
	m := WithBaseURL(InviteMessage("a@b.test", acceptURL, ""), "https://vpn.acme.test/")
	if !strings.Contains(m.HTML, `src="https://vpn.acme.test/email/tunnex-logo-2x.png"`) {
		t.Fatalf("the logo must be served by the deployment:\n%s", m.HTML)
	}
	if strings.Contains(m.HTML, "tunnex.io/email/") {
		t.Fatal("a customer's invitation must never fetch an asset from tunnex.io")
	}
	// ⚠ AN EMPTY BASE LEAVES IT ROOT-RELATIVE — a broken image, which is a LOCAL failure. That is the safe
	// direction: the alternative is falling back to our host, which is exactly what the divergence forbids.
	unset := WithBaseURL(InviteMessage("a@b.test", acceptURL, ""), "")
	if strings.Contains(unset.HTML, "tunnex.io/email/") {
		t.Fatal("an unset base URL must not fall back to our host for the asset")
	}
	// ⚠ The footer's mailto:support@tunnex.io is a DIFFERENT thing and stays: it is an address to write to,
	// not a request the recipient's client makes on open.
	if !strings.Contains(unset.HTML, `src="/email/tunnex-logo-2x.png"`) {
		t.Fatal("with no base URL the src must stay root-relative — a broken image is a LOCAL failure, and " +
			"that is the safe direction")
	}
}

// TestPlainTextMessagesAreUnchangedOnTheWire — the multipart branch must not touch callers that have no HTML.
func TestPlainTextMessagesAreUnchangedOnTheWire(t *testing.T) {
	raw := string(buildRFC822("f@x.test", Message{To: "a@b.test", Subject: "S", Text: "body"}))
	if strings.Contains(raw, "multipart") {
		t.Fatal("a message with no HTML must still be a bare text/plain message")
	}
	if !strings.HasSuffix(raw, "\r\n\r\nbody") {
		t.Fatalf("the plaintext wire format changed:\n%q", raw)
	}
}

// TestMultipartOrdersTextBeforeHTML — RFC 2046 orders alternatives least-to-most preferred.
//
// ⛔ A CLIENT PICKS THE LAST PART IT UNDERSTANDS. Reversing these serves plaintext to clients that could
// have rendered the branded version — a bug that looks like "the template didn't apply" and is really an
// ordering mistake in the envelope.
func TestMultipartOrdersTextBeforeHTML(t *testing.T) {
	raw := string(buildRFC822("f@x.test", InviteMessage("a@b.test", acceptURL, "")))
	if !strings.Contains(raw, "multipart/alternative") {
		t.Fatal("a message with both bodies must be multipart/alternative")
	}
	textAt := strings.Index(raw, "text/plain")
	htmlAt := strings.Index(raw, "text/html")
	if textAt < 0 || htmlAt < 0 || textAt > htmlAt {
		t.Fatalf("text/plain must come FIRST: text=%d html=%d", textAt, htmlAt)
	}
	if !strings.HasSuffix(strings.TrimRight(raw, "\r\n"), "--") {
		t.Fatal("the multipart body must be terminated by the closing boundary")
	}
}
