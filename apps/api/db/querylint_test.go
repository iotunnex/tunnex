package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Query-lint: make the tenancy conventions executable, not just documented.
//
//   - Tables carrying deleted_at (organizations, users) MUST filter
//     `deleted_at IS NULL` on SELECT/UPDATE, or silently resurrect deleted rows.
//   - Tenant-owned tables (memberships, invitations) MUST scope by `org_id`, or
//     leak/mutate another tenant's data.
//
// Both rules have an explicit escape hatch (`lint:allow-deleted` /
// `lint:cross-org`) so intentional exceptions are reviewed, not accidental —
// same spirit as the set_updated_at trigger check.
//
// This is a source-level lint over db/queries/*.sql; false positives are cheap
// (annotate), silent isolation bugs are not.

var (
	deletedAtTables = []string{"organizations", "users"}
	tenantTables    = []string{"memberships", "invitations"}
)

type query struct {
	name string
	body string // full block incl. leading comments (annotations live here)
}

func loadQueries(t *testing.T) []query {
	t.Helper()
	entries, err := os.ReadDir("queries")
	if err != nil {
		t.Fatalf("read queries dir: %v", err)
	}
	var out []query
	nameRe := regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join("queries", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		locs := nameRe.FindAllStringSubmatchIndex(string(content), -1)
		for i, loc := range locs {
			end := len(content)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			out = append(out, query{
				name: string(content)[loc[2]:loc[3]],
				body: string(content)[loc[0]:end],
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no queries found — lint would pass vacuously")
	}
	return out
}

func refersTo(low, table string) bool {
	// SELECT/UPDATE/DELETE references that need scoping (INSERT sets columns in
	// VALUES and is exempt from these read/update-scope rules).
	re := regexp.MustCompile(`(?:from|update|join|delete\s+from)\s+` + table + `\b`)
	return re.MatchString(low)
}

func TestQueriesScopeDeletedAt(t *testing.T) {
	for _, q := range loadQueries(t) {
		low := strings.ToLower(q.body)
		if strings.Contains(low, "lint:allow-deleted") {
			continue
		}
		for _, tbl := range deletedAtTables {
			if refersTo(low, tbl) && !strings.Contains(low, "deleted_at") {
				t.Errorf("query %q reads/updates %q without `deleted_at IS NULL` "+
					"(add the filter, or annotate `-- lint:allow-deleted` with a reason)",
					q.name, tbl)
			}
		}
	}
}

func TestQueriesScopeOrgID(t *testing.T) {
	for _, q := range loadQueries(t) {
		low := strings.ToLower(q.body)
		if strings.Contains(low, "lint:cross-org") {
			continue
		}
		for _, tbl := range tenantTables {
			if refersTo(low, tbl) && !strings.Contains(low, "org_id") {
				t.Errorf("query %q touches tenant-owned table %q without `org_id` scoping "+
					"(add org_id, or annotate `-- lint:cross-org` with a reason)",
					q.name, tbl)
			}
		}
	}
}
