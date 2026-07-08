package cli

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWriteFileAtomic0600 pins the D2 write discipline: 0600 perms, full
// content, and atomic replacement of an existing file.
func TestWriteFileAtomic0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "device.conf")
	if err := WriteFileAtomic0600(p, []byte("[Interface]\nPrivateKey = secret\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perms: want 0600, got %o", st.Mode().Perm())
	}
	// Overwrite is atomic and keeps 0600.
	if err := WriteFileAtomic0600(p, []byte("v2")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "v2" {
		t.Fatalf("content: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("perms after overwrite: %o", st.Mode().Perm())
	}
	// No temp litter left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp litter: %d entries", len(entries))
	}
}

// TestCredentialRoundTrip pins the credential store (0600, XDG/TUNNEX_STATE_DIR).
func TestCredentialRoundTrip(t *testing.T) {
	t.Setenv("TUNNEX_STATE_DIR", t.TempDir())
	want := Credential{Server: "http://x", Token: "tnx_t", Fingerprint: "abc", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	if err := SaveCredential(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	dir, _ := StateDir()
	st, err := os.Stat(filepath.Join(dir, "credential.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credential file: %v perm=%o", err, st.Mode().Perm())
	}
	got, err := LoadCredential()
	if err != nil || got != want {
		t.Fatalf("load: %+v err=%v", got, err)
	}
	if err := DeleteCredential(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := LoadCredential(); err != ErrNotLoggedIn {
		t.Fatalf("post-delete: want ErrNotLoggedIn, got %v", err)
	}
	if err := DeleteCredential(); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

// TestCallbackHandler pins the loopback hygiene: state mismatch → failure page
// + error result + the code NEVER read; good callback → success page + code;
// only ONE result is ever emitted.
func TestCallbackHandler(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("good-state", ch)

	// State mismatch: 403 failure page, error result, no code.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?code=stolen&state=WRONG", nil))
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "Sign-in failed") {
		t.Fatalf("mismatch page: %d %s", rec.Code, rec.Body.String())
	}
	res := <-ch
	if res.err == nil || res.code != "" {
		t.Fatalf("mismatch result: %+v — the code must never surface", res)
	}

	// Missing code: failure.
	ch2 := make(chan callbackResult, 1)
	h2 := callbackHandler("s", ch2)
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?state=s", nil))
	if rec.Code != 400 {
		t.Fatalf("missing code: want 400, got %d", rec.Code)
	}
	if res := <-ch2; res.err == nil {
		t.Fatalf("missing-code result: %+v", res)
	}

	// Happy path: success page + code; a SECOND request cannot emit again.
	ch3 := make(chan callbackResult, 1)
	h3 := callbackHandler("s", ch3)
	rec = httptest.NewRecorder()
	h3.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?state=s&code=one-time", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "close this window") {
		t.Fatalf("success page: %d %s", rec.Code, rec.Body.String())
	}
	if res := <-ch3; res.code != "one-time" || res.err != nil {
		t.Fatalf("success result: %+v", res)
	}
	rec = httptest.NewRecorder()
	h3.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?state=s&code=second", nil))
	select {
	case res := <-ch3:
		t.Fatalf("second request emitted a result: %+v", res)
	default: // single-shot holds
	}

	// Non-callback paths 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/favicon.ico", nil))
	if rec.Code != 404 {
		t.Fatalf("non-callback: want 404, got %d", rec.Code)
	}
}
