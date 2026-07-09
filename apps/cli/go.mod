module github.com/tunnexio/tunnex/apps/cli

// GUARD: module path (tunnexio/tunnex) != repo (github.com/iotunnex/tunnex). Build/test
// with GOFLAGS=-mod=readonly so go never remote-resolves this path (it would ls-remote a
// nonexistent repo and fail on fresh clones/CI). Keep readonly until the vanity rename
// (tunnex.io/…) on domain purchase. See PLAN.md "OPEN DECISIONS (b)".

go 1.25.0

toolchain go1.25.11

require github.com/oapi-codegen/runtime v1.1.1

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.5.0 // indirect
)
