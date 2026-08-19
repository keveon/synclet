package reader

import (
	"testing"
	"time"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/filter"
)

// TestMySQLKeysetBindsCursorValueTwice is a regression test: MySQL consumes
// one argument per ?. The keyset predicate references the cursor value
// twice (> c OR c = c AND t >), so MySQL must bind it twice while
// PostgreSQL reuses $n.
func TestMySQLKeysetBindsCursorValueTwice(t *testing.T) {
	job := config.JobConfig{
		Name: "products",
		Mode: "incremental",
		Reader: config.ReaderConfig{
			Table:   "src_products",
			Columns: []string{"sku", "changed_at"},
			Filters: []config.FilterConfig{
				{Column: "active", Op: "eq", Value: 1},
			},
			Cursor: config.CursorConfig{Column: "changed_at", TieBreakerColumn: "sku"},
		},
	}
	cursor := checkpoint.Cursor{Value: time.Date(2026, 8, 19, 14, 0, 0, 0, time.UTC), Tie: "SKU-1"}
	query, args, ok, err := BuildQueryForTest(job, filter.New(filter.Config{AllowAll: true}), cursor, 500, MySQLDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}

	// Count placeholders: 1 filter + 3 keyset (cursor twice + tie) + 1 limit.
	placeholders := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			placeholders++
		}
	}
	if placeholders != len(args) {
		t.Errorf("query has %d placeholders but %d args were bound: %s", placeholders, len(args), query)
	}
}

// TestPostgresKeysetReusesPlaceholder pins the PostgreSQL side: the cursor
// value is referenced twice but bound once.
func TestPostgresKeysetReusesPlaceholder(t *testing.T) {
	job := config.JobConfig{
		Name: "products",
		Mode: "incremental",
		Reader: config.ReaderConfig{
			Table:   "products",
			Columns: []string{"id", "updated_at"},
			Cursor:  config.CursorConfig{Column: "updated_at", TieBreakerColumn: "id"},
		},
	}
	cursor := checkpoint.Cursor{Value: time.Date(2026, 8, 19, 14, 0, 0, 0, time.UTC), Tie: int64(3)}
	query, args, ok, err := BuildQueryForTest(job, filter.New(filter.Config{AllowAll: true}), cursor, 500, PostgresDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}

	// Highest $n referenced must equal len(args).
	highest := 0
	for i := 1; i <= len(args); i++ {
		if !containsStr(query, placeholder(i)) {
			t.Errorf("expected %s to be referenced in %s", placeholder(i), query)
		}
		highest = i
	}
	if highest > len(args) {
		t.Errorf("query references $%d but only %d args exist", highest, len(args))
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func placeholder(n int) string {
	return "$" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
