module github.com/tunnexio/tunnex/apps/helper

// GUARD: module path (tunnexio/tunnex) != repo (github.com/iotunnex/tunnex). Build/test
// with GOFLAGS=-mod=readonly so go never remote-resolves this path (it would ls-remote a
// nonexistent repo and fail on fresh clones/CI). Keep readonly until the vanity rename
// (tunnex.io/…) on domain purchase. See PLAN.md "OPEN DECISIONS (b)".
//
// The core (protocol / config / auth / state) is STDLIB-ONLY on purpose — no external
// deps, no go.sum, nothing for CI to fetch. Platform tunnel backends (wireguard-go /
// wireguard-nt) arrive in build-tagged files that never enter the core test path.

go 1.25
