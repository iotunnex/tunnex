package http

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
)

func TestAuthorizeOrg(t *testing.T) {
	orgA := uuid.New()
	orgB := uuid.New()
	user := uuid.New()

	// Unauthenticated -> 401, fails closed.
	if _, err := authorizeOrg(context.Background(), orgA); !hasCode(err, 401, "unauthenticated") {
		t.Fatalf("no principal: want 401 unauthenticated, got %v", err)
	}

	member := authctx.WithPrincipal(context.Background(),
		&authctx.Principal{UserID: user, Roles: map[uuid.UUID]string{orgA: "owner"}})

	// Member of A -> ok, and the authorized org is placed in context.
	ctx, err := authorizeOrg(member, orgA)
	if err != nil {
		t.Fatalf("member of A: unexpected error %v", err)
	}
	if got, ok := authctx.OrgFrom(ctx); !ok || got != orgA {
		t.Fatalf("authorized org not in context: got %v ok=%v", got, ok)
	}

	// Not a member of B -> 404 (not 403): no cross-tenant existence leak.
	if _, err := authorizeOrg(member, orgB); !hasCode(err, 404, "org_not_found") {
		t.Fatalf("non-member: want 404 org_not_found, got %v", err)
	}
}

func hasCode(err error, status int, code string) bool {
	var apiErr *apierr.Error
	return errors.As(err, &apiErr) && apiErr.Status == status && apiErr.Code == code
}
