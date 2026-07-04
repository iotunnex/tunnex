package invites

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/mail"
)

func TestDecideAcceptMatrix(t *testing.T) {
	if DecideAccept(false) != AcceptCreate {
		t.Error("no local account -> create")
	}
	if DecideAccept(true) != AcceptAttach {
		t.Error("local account exists -> attach")
	}
}

type captureMailer struct{ tokens []string }

func (m *captureMailer) Kind() string { return "capture" }
func (m *captureMailer) Send(_ context.Context, msg mail.Message) error {
	if i := strings.Index(msg.Text, "token="); i >= 0 {
		m.tokens = append(m.tokens, strings.TrimSpace(msg.Text[i+len("token="):]))
	}
	return nil
}
func (m *captureMailer) last() string { return m.tokens[len(m.tokens)-1] }

func codeOf(err error) string {
	var a *apierr.Error
	if err != nil && errors.As(err, &a) {
		return a.Code
	}
	return ""
}

// newSvc returns an invites service over a rolled-back tx, plus a persisted org
// and actor (audit rows FK to a real user).
func newSvc(t *testing.T) (*Service, *captureMailer, uuid.UUID, uuid.UUID, context.Context) {
	t.Helper()
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	org, actor := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", org, "O", "inv-"+org.String()); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", actor, "actor-"+actor.String()+"@t", "Actor"); err != nil {
		t.Fatalf("actor: %v", err)
	}
	svc := &Service{q: sqlc.New(tx), mailer: &captureMailer{}, baseURL: "http://app", logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return svc, svc.mailer.(*captureMailer), org, actor, ctx
}

func TestInviteCreateAcceptNewUser(t *testing.T) {
	svc, mailer, org, actor, ctx := newSvc(t)
	email := "invitee-" + uuid.NewString() + "@example.com"

	if err := svc.Create(ctx, actor, org, email, "member"); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	token := mailer.last()

	// Accept creates a VERIFIED user + membership.
	uid, gotOrg, err := svc.Accept(ctx, token, "New User", "a valid passphrase")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if gotOrg != org {
		t.Fatal("org mismatch")
	}
	u, err := svc.q.GetUserByEmail(ctx, email)
	if err != nil || u.ID != uid || !u.EmailVerifiedAt.Valid {
		t.Fatalf("user not created/verified: %+v err=%v", u, err)
	}
	if _, err := svc.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: org, UserID: uid}); err != nil {
		t.Fatalf("membership missing: %v", err)
	}
	// Single-use: replay fails.
	if _, _, err := svc.Accept(ctx, token, "", ""); codeOf(err) != "invalid_invite" {
		t.Fatalf("replay: want invalid_invite, got %v", err)
	}
}

// A second pending invite for the same (org,email) is rejected. Isolated in its
// own tx because the unique violation aborts the transaction.
func TestInviteDuplicatePendingRejected(t *testing.T) {
	svc, _, org, actor, ctx := newSvc(t)
	email := "dup-" + uuid.NewString() + "@example.com"
	if err := svc.Create(ctx, actor, org, email, "member"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Create(ctx, actor, org, email, "member"); codeOf(err) != "invite_pending" {
		t.Fatalf("duplicate: want invite_pending, got %v", err)
	}
}

func TestInviteAcceptAttachesExistingAndVerifies(t *testing.T) {
	svc, mailer, org, actor, ctx := newSvc(t)
	// An existing but UNVERIFIED local account.
	existing, err := svc.q.CreateUser(ctx, sqlc.CreateUserParams{Email: "exists@example.com", Name: "E"})
	if err != nil {
		t.Fatalf("existing: %v", err)
	}
	if err := svc.Create(ctx, actor, org, "exists@example.com", "admin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	uid, _, err := svc.Accept(ctx, mailer.last(), "", "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if uid != existing.ID {
		t.Fatal("should attach to existing account, not create")
	}
	// Accepting verified the previously-unverified account (token proves inbox).
	u, _ := svc.q.GetUserByEmail(ctx, "exists@example.com")
	if !u.EmailVerifiedAt.Valid {
		t.Fatal("accept should verify the email")
	}
}

func TestInviteAcceptRejectsDeactivatedAccount(t *testing.T) {
	svc, mailer, org, actor, ctx := newSvc(t)
	u, err := svc.q.CreateUser(ctx, sqlc.CreateUserParams{Email: "frozen@example.com", Name: "F"})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := svc.q.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: u.ID, Status: "deactivated"}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := svc.Create(ctx, actor, org, "frozen@example.com", "member"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Accepting must not resurrect a frozen account into a membership.
	if _, _, err := svc.Accept(ctx, mailer.last(), "", ""); codeOf(err) != "account_deactivated" {
		t.Fatalf("accept into frozen account: want account_deactivated, got %v", err)
	}
}

func TestInviteRevokeAndResend(t *testing.T) {
	svc, mailer, org, actor, ctx := newSvc(t)
	email := "rr-" + uuid.NewString() + "@example.com"

	if err := svc.Create(ctx, actor, org, email, "member"); err != nil {
		t.Fatalf("create: %v", err)
	}
	first := mailer.last()

	// Resend issues a new token and invalidates the old.
	if err := svc.Resend(ctx, actor, org, email); err != nil {
		t.Fatalf("resend: %v", err)
	}
	second := mailer.last()
	if first == second {
		t.Fatal("resend must issue a new token")
	}
	if _, _, err := svc.Accept(ctx, first, "", "a valid passphrase"); codeOf(err) != "invalid_invite" {
		t.Fatalf("old token valid after resend: %v", err)
	}

	// Revoke the pending invite; re-revoking is a clean not-pending error.
	if err := svc.Revoke(ctx, actor, org, email); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.Revoke(ctx, actor, org, email); codeOf(err) != "invite_not_pending" {
		t.Fatalf("double revoke: want invite_not_pending, got %v", err)
	}
}
