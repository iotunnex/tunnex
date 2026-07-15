//go:build enterprise

package idpsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// EntraProvider is the Microsoft Entra (Azure AD) DirectoryProvider — the FIRST implementation.
// Everything Entra-specific is contained here and never leaks past DirectoryProvider:
//   - APP-CRED TOKEN: OAuth2 client_credentials against login.microsoftonline.com/{tenant}, cached
//     until near expiry (mu-guarded).
//   - GRAPH PAGINATION: the members endpoint returns @odata.nextLink pages; ListGroupMembers loops
//     them and returns one flat slice.
//   - DISABLED-vs-GONE: Graph `accountEnabled=false` → StatusDisabled; a 404 on the user → StatusGone;
//     a 404 on the group → ErrGroupGone.
//
// httpDoer + the base URLs are injectable so unit tests drive canned Graph JSON with NO live Graph.
type EntraProvider struct {
	tenantID     string
	clientID     string
	clientSecret string

	http      httpDoer
	graphBase string // default https://graph.microsoft.com
	loginBase string // default https://login.microsoftonline.com
	now       func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// NewEntraProvider builds the provider. doer defaults to http.DefaultClient; the base URLs default
// to the real Microsoft hosts (tests override all three).
func NewEntraProvider(tenantID, clientID, clientSecret string, doer httpDoer) *EntraProvider {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &EntraProvider{
		tenantID: tenantID, clientID: clientID, clientSecret: clientSecret,
		http:      doer,
		graphBase: "https://graph.microsoft.com",
		loginBase: "https://login.microsoftonline.com",
		now:       time.Now,
	}
}

// --- token (client_credentials, cached) ---

func (e *EntraProvider) accessToken(ctx context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.token != "" && e.now().Before(e.tokenExp) {
		return e.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {e.clientID},
		"client_secret": {e.clientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
	}
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", e.loginBase, e.tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("entra token: status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("entra token: empty access_token")
	}
	e.token = tok.AccessToken
	// Refresh a minute early so an in-flight call never uses a just-expired token.
	e.tokenExp = e.now().Add(time.Duration(tok.ExpiresIn)*time.Second - time.Minute)
	return e.token, nil
}

// graphGet issues an authenticated GET to a Graph URL (absolute, e.g. a nextLink) or a path under
// graphBase. Returns the raw *http.Response for the caller to decode / inspect status.
func (e *EntraProvider) graphGet(ctx context.Context, rawURL string) (*http.Response, error) {
	tok, err := e.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(rawURL, "http") {
		rawURL = e.graphBase + rawURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	return e.http.Do(req)
}

// --- DirectoryProvider ---

type graphUser struct {
	ID                string `json:"id"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
	AccountEnabled    *bool  `json:"accountEnabled"`
}

func statusOf(accountEnabled *bool) UserStatus {
	if accountEnabled != nil && !*accountEnabled {
		return StatusDisabled
	}
	return StatusActive // nil (not selected) or true → treat as active
}

func emailOf(u graphUser) string {
	if u.Mail != "" {
		return strings.ToLower(u.Mail)
	}
	return strings.ToLower(u.UserPrincipalName) // Entra guests / no-mailbox users fall back to UPN
}

// ListGroupMembers loops the Graph nextLink pages and returns the flat member set. Only user
// members are requested (`/members/microsoft.graph.user` filters out nested groups / service
// principals). A 404 on the group → ErrGroupGone.
func (e *EntraProvider) ListGroupMembers(ctx context.Context, groupID string) ([]DirectoryMember, error) {
	next := fmt.Sprintf("/v1.0/groups/%s/members/microsoft.graph.user?$select=id,mail,userPrincipalName,accountEnabled&$top=999", url.PathEscape(groupID))
	var out []DirectoryMember
	for next != "" {
		resp, err := e.graphGet(ctx, next)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close() //nolint:errcheck
			return nil, ErrGroupGone
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close() //nolint:errcheck
			return nil, fmt.Errorf("entra list group members: status %d", resp.StatusCode)
		}
		var page struct {
			Value    []graphUser `json:"value"`
			NextLink string      `json:"@odata.nextLink"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close() //nolint:errcheck
		if derr != nil {
			return nil, derr
		}
		for _, u := range page.Value {
			out = append(out, DirectoryMember{ExternalID: u.ID, Email: emailOf(u), Status: statusOf(u.AccountEnabled)})
		}
		next = page.NextLink
	}
	return out, nil
}

// ResolveUserStatus reports active / disabled / gone for a user by external id. A 404 → StatusGone.
func (e *EntraProvider) ResolveUserStatus(ctx context.Context, externalID string) (UserStatus, error) {
	resp, err := e.graphGet(ctx, fmt.Sprintf("/v1.0/users/%s?$select=id,accountEnabled", url.PathEscape(externalID)))
	if err != nil {
		return StatusActive, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return StatusGone, nil
	}
	if resp.StatusCode != http.StatusOK {
		return StatusActive, fmt.Errorf("entra resolve user: status %d", resp.StatusCode)
	}
	var u graphUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return StatusActive, err
	}
	return statusOf(u.AccountEnabled), nil
}
