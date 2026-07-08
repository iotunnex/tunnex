package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/tunnexio/tunnex/apps/cli/internal/api"
)

// ExpiredCredentialLine is the exact one-line UX for 401 credential_expired
// (S5.1 acceptance: never a raw error dump).
const ExpiredCredentialLine = "credential expired — run 'tunnex login'"

// NewClient builds an unauthenticated API client for server.
func NewClient(server string) (*api.ClientWithResponses, error) {
	return api.NewClientWithResponses(strings.TrimRight(server, "/"))
}

// NewAuthedClient builds a client that sends the stored bearer credential.
func NewAuthedClient(cred Credential) (*api.ClientWithResponses, error) {
	return api.NewClientWithResponses(strings.TrimRight(cred.Server, "/"),
		api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+cred.Token)
			return nil
		}))
}

// envelope mirrors the server's error envelope for message extraction.
type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// apiErr renders a non-2xx response as a short human error. A 401
// credential_expired is special-cased to the exact acceptance line and exits 1
// — the CLI never dumps raw errors for that case.
func apiErr(status int, body []byte, fallback string) error {
	var e envelope
	_ = json.Unmarshal(body, &e)
	if status == http.StatusUnauthorized && e.Error.Code == "credential_expired" {
		fmt.Fprintln(os.Stderr, ExpiredCredentialLine)
		os.Exit(1)
	}
	if e.Error.Message != "" {
		return fmt.Errorf("%s (%s)", e.Error.Message, e.Error.Code)
	}
	return fmt.Errorf("%s (HTTP %d)", fallback, status)
}
