module github.com/tunnexio/tunnex/apps/helper

// GUARD: module path (tunnexio/tunnex) != repo (github.com/iotunnex/tunnex). Build/test
// with GOFLAGS=-mod=readonly so go never remote-resolves this path (it would ls-remote a
// nonexistent repo and fail on fresh clones/CI). Keep readonly until the vanity rename
// (tunnex.io/…) on domain purchase. See PLAN.md "OPEN DECISIONS (b)".
//
// The core (protocol / config / auth / state / ipc) is STDLIB-ONLY. Deps are
// platform-only + never in the core test path: golang.org/x/sys (caller-path —
// SO_PEERCRED / LOCAL_PEERPID / GetNamedPipeClientProcessId) and Microsoft/go-winio
// (the Windows SDDL-protected named-pipe listener). Tunnel backends (wireguard-go /
// wireguard-nt) arrive later in build-tagged files. CI cross-compiles CGO_ENABLED=0,
// so any cgo file (e.g. macOS libproc) carries a no-cgo stub sibling.

go 1.25

require golang.org/x/sys v0.28.0

require github.com/Microsoft/go-winio v0.6.2
