package helper

import "errors"

// CleanStaleResolvers sweeps any owned domain-scoped resolver files left by a PRIOR process that exited
// without a graceful sweep — a helper/host restart, or an uninstall-without-relaunch (F6, mirroring the
// kill-switch SelfHeal precedent). It reconciles the owned set to EMPTY; foreign resolver files are never
// touched. The helper calls it ONCE at startup, before serving. A platform without resolver support
// (Windows until S8.4b) reports resolvers_unsupported, which is treated as a no-op.
func CleanStaleResolvers() error {
	if err := setResolvers(nil); err != nil {
		var pe *ProtocolError
		if errors.As(err, &pe) && pe.Code == "resolvers_unsupported" {
			return nil
		}
		return err
	}
	return nil
}
