package licence

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewKey(t *testing.T) {
	b, _ := os.ReadFile("/tmp/newkey.txt")
	res, err := Verify(TrustedKeys, strings.TrimSpace(string(b)))
	t.Logf("VERIFY ok=%v reason=%q err=%v", res.OK, res.Reason, err)
	if !res.OK {
		t.Fatalf("REFUSED: %s", res.Reason)
	}
	c := res.Claims
	t.Logf("kid=%s dom=%s tier=%q band=%s gw=%v", c.Kid, c.Domain, c.Tier, c.Band, *c.Gateways)
	t.Logf("expires=%s", time.Unix(c.ExpiresAt, 0).UTC().Format("2006-01-02"))
	m := &Manager{}
	m.claims = &c
	st := m.Evaluate(time.Now())
	ceil, _ := GatewayCeilingFor(st.Tier)
	t.Logf("PRODUCT VERDICT: state=%v tier=%v gateway_ceiling=%v", st.State, st.Tier, *ceil)
}
