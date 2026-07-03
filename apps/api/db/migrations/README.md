# Database migrations & schema conventions

Migrations are plain SQL, applied with [golang-migrate](https://github.com/golang-migrate/migrate).
Files are embedded into the `migrate` binary (`go:embed`) so the tool is
self-contained. Naming: `NNNN_name.up.sql` / `NNNN_name.down.sql`.

```bash
make migrate            # apply all up migrations
make migrate-down       # roll back one migration
make migrate-create name=add_widgets   # scaffold a new pair
make sqlc               # regenerate typed query code
```

## Conventions (established in 0001, inherited by every table)

These three are painful to change once S1.1's tables exist, so they are fixed now:

### 1. Primary keys — UUIDv7
Every table's PK is `id uuid PRIMARY KEY DEFAULT uuid_generate_v7()`.
UUIDv7 is time-ordered (index-friendly, no page fragmentation like v4), needs no
central coordination, and doesn't leak row counts like serial IDs. The generator
is defined in 0001 (native `uuidv7()` arrives in PG18).

### 2. Timestamps — `timestamptz`, always, with created_at/updated_at
- Never `timestamp` (without zone). Always `timestamptz`.
- Every table has `created_at timestamptz NOT NULL DEFAULT now()` and
  `updated_at timestamptz NOT NULL DEFAULT now()`.
- Attach the `set_updated_at()` trigger (defined in 0001) to every table:
  ```sql
  CREATE TRIGGER set_updated_at BEFORE UPDATE ON widgets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
  ```

### 3. Multi-tenancy — org_id scoping
- Every tenant-owned table has `org_id uuid NOT NULL REFERENCES organizations(id)`.
- Composite indexes **lead with `org_id`** (e.g. `(org_id, email)`), because
  every query is tenant-scoped — this makes the tenant filter the index prefix.
- Uniqueness that is per-tenant must be scoped: `UNIQUE (org_id, email)`, never
  a bare `UNIQUE (email)`.

## Auto-migrate on boot — and when to turn it off (S10.1 / S11.4)
The API auto-migrates on boot (`TUNNEX_AUTO_MIGRATE=true`) so the compose
quickstart self-provisions. golang-migrate takes an advisory lock, so concurrent
starts are safe. **But for multi-replica / Kubernetes deployments, the
production pattern is migrate-as-init-job with `TUNNEX_AUTO_MIGRATE=false` on the
API pods** — every pod racing to migrate on boot is an operational footgun even
when it's technically race-safe. The Helm chart (S10.1) must ship it this way.

## Down migrations are not optional
Every `up` has a `down` that has actually been run at least once (the CI/DoD runs
`up → down → up`). A down that has never executed is a broken down waiting to be
discovered in an incident.
