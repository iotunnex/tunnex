// Package ovpn is the OpenVPN control-plane service (S9.1, EPIC 9). Slice 2 provides client-cert
// ISSUANCE: it mints a client profile from the separate OVPN client CA (ovpnca, D-S9.1-1) and
// RECORDS the cert identity (serial, expiry, device binding) so the Slice 5 revocation full-sweep
// and CRL have their source (B2). The client private key is EPHEMERAL (D-S9.2-1) — returned to the
// caller for one-time .ovpn delivery (Slice 4), never persisted.
//
// Edition-independent (D-S9.1-6): the OVPN server + PKI ship open-edition; enforcement is the
// enterprise tier.
package ovpn

import (
	"context"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/ovpnca"
)

// Service issues + records OpenVPN client certificates.
type Service struct {
	q  *sqlc.Queries
	ca *ovpnca.CA
}

// NewService wires the OVPN service to the client CA and the query set.
func NewService(q *sqlc.Queries, ca *ovpnca.CA) *Service {
	return &Service{q: q, ca: ca}
}

// Issue mints an OVPN client profile for a device and records the cert identity so revocation
// (Slice 5) can build the CRL and the B2 full-sweep can find the serial. The returned Profile
// carries the EPHEMERAL private key (D-S9.2-1): the caller streams it into the .ovpn exactly once
// (Slice 4's one-time ceremony) and discards it — this service persists ONLY the cert identity,
// never the key.
//
// commonName is the cert subject CN — the device's stable identity (set by the caller from the
// device/user binding). Recording happens AFTER a successful signature, so a persisted row always
// corresponds to a real issued cert (the swallowed-audit law's mirror, applied to PKI state).
func (s *Service) Issue(ctx context.Context, orgID, deviceID uuid.UUID, commonName string) (ovpnca.Profile, error) {
	p, err := s.ca.IssueClient(commonName)
	if err != nil {
		return ovpnca.Profile{}, err
	}
	if _, err := s.q.InsertOVPNClientCert(ctx, sqlc.InsertOVPNClientCertParams{
		OrgID:      orgID,
		DeviceID:   deviceID,
		Serial:     p.Serial,
		CommonName: commonName,
		NotAfter:   p.NotAfter,
	}); err != nil {
		return ovpnca.Profile{}, err
	}
	return p, nil
}

// ExportProfile mints a client cert for an already-created OVPN device (the caller runs the
// devices.Service.Create fork first) and assembles the one-time `.ovpn` profile. It returns the
// profile text (carrying the EPHEMERAL private key, delivered ONCE per the S3.4/D2 ceremony) and a
// FINGERPRINT — the cert serial — which is what the caller records in the audit row: never the
// material, only its keyed identity. The device id is the cert CommonName (and the CCD filename,
// Slice 3), so the roster, the cert record, and the compiled /32 all agree on one identity.
//
// host/port are the gateway's OpenVPN remote (resolved by the caller from the device's node). A lost
// profile is NOT re-fetchable — the key is never stored — so recovery is revoke + re-issue (an
// ordinary revoke, Slice 5), never a re-download.
func (s *Service) ExportProfile(ctx context.Context, orgID, deviceID uuid.UUID, host string, port int) (profile, fingerprint string, err error) {
	p, err := s.Issue(ctx, orgID, deviceID, deviceID.String())
	if err != nil {
		return "", "", err
	}
	profile = BuildProfile(string(s.ca.CertPEM()), p, host, port)
	return profile, p.Serial, nil
}
