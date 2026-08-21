package reader

import (
	"strings"
	"testing"
	"time"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/model"
)

func snapshotJob() config.JobConfig {
	return config.JobConfig{
		Name: "customers",
		Mode: "snapshot",
		Reader: config.ReaderConfig{
			Connection: "source",
			Table:      "customers",
			Columns:    []string{"code", "name"},
			Filters: []config.FilterConfig{
				{Column: "enabled", Op: "eq", Value: true},
			},
			OrderBy: []string{"code"},
		},
	}
}

func incrementalJoinedJob() config.JobConfig {
	return config.JobConfig{
		Name: "orders",
		Mode: "incremental",
		Reader: config.ReaderConfig{
			Connection: "source",
			Table:      "orders",
			Alias:      "o",
			Columns:    []string{"o.id", "o.customer_id", "o.submitted_at", "c.metadata"},
			Joins: []config.JoinConfig{
				{Type: "left", Table: "customers", Alias: "c", On: config.JoinOnConfig{Left: "o.customer_id", Right: "c.code"}},
			},
			Cursor: config.CursorConfig{Column: "submitted_at", TieBreakerColumn: "id"},
			Filters: []config.FilterConfig{
				{Column: "o.status", Op: "eq", Value: "confirmed"},
			},
		},
	}
}

func scopeFilter() filter.Filter {
	return filter.New(filter.Config{AllowedCodes: []string{"C001", "C002"}})
}

func TestBuildQueryPostgresSnapshot(t *testing.T) {
	query, args, ok, err := BuildQueryForTest(snapshotJob(), scopeFilter(), checkpoint.Cursor{}, 500, PostgresDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, `SELECT "code", "name" FROM "customers"`) {
		t.Errorf("unexpected select: %s", query)
	}
	if !strings.Contains(query, `"enabled" = $1`) {
		t.Errorf("filter must be parameterized: %s", query)
	}
	if !strings.Contains(query, `ORDER BY "code" ASC`) {
		t.Errorf("order by missing: %s", query)
	}
	if !strings.Contains(query, "LIMIT $2") {
		t.Errorf("limit must be parameterized: %s", query)
	}
	if len(args) != 2 || args[0] != true || args[1] != 500 {
		t.Errorf("args = %v", args)
	}
}

func TestBuildQueryMySQLSnapshot(t *testing.T) {
	query, _, ok, err := BuildQueryForTest(snapshotJob(), scopeFilter(), checkpoint.Cursor{}, 500, MySQLDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, "SELECT `code`, `name` FROM `customers`") {
		t.Errorf("unexpected select: %s", query)
	}
	if !strings.Contains(query, "`enabled` = ?") {
		t.Errorf("mysql placeholder expected: %s", query)
	}
	if !strings.Contains(query, "LIMIT ?") {
		t.Errorf("mysql limit placeholder expected: %s", query)
	}
}

func TestBuildQueryIncrementalKeyset(t *testing.T) {
	job := config.JobConfig{
		Name: "orders",
		Mode: "incremental",
		Reader: config.ReaderConfig{
			Table:   "orders",
			Columns: []string{"id", "submitted_at"},
			Cursor:  config.CursorConfig{Column: "submitted_at", TieBreakerColumn: "id"},
		},
	}
	cursor := checkpoint.Cursor{Value: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), Tie: int64(10)}
	query, args, ok, err := BuildQueryForTest(job, filter.New(filter.Config{AllowAll: true}), cursor, 500, PostgresDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, `("submitted_at" >= $1 AND ("submitted_at" > $1 OR ("submitted_at" = $1 AND "id" > $2)))`) {
		t.Errorf("composite keyset predicate missing: %s", query)
	}
	if !strings.Contains(query, `ORDER BY "submitted_at" ASC, "id" ASC`) {
		t.Errorf("composite order missing: %s", query)
	}
	if len(args) != 3 {
		t.Errorf("args = %v, want [cursor tie limit]", args)
	}
	if args[1] != int64(10) {
		t.Errorf("tie arg = %v (%T), want int64(10)", args[1], args[1])
	}
}

func TestBuildQueryIncrementalKeysetMySQLBindsCursorThrice(t *testing.T) {
	job := config.JobConfig{
		Name: "orders",
		Mode: "incremental",
		Reader: config.ReaderConfig{
			Table:   "orders",
			Columns: []string{"id", "submitted_at"},
			Cursor:  config.CursorConfig{Column: "submitted_at", TieBreakerColumn: "id"},
		},
	}
	cursor := checkpoint.Cursor{Value: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), Tie: int64(10)}
	query, args, ok, err := BuildQueryForTest(job, filter.New(filter.Config{AllowAll: true}), cursor, 500, MySQLDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, "(`submitted_at` >= ? AND (`submitted_at` > ? OR (`submitted_at` = ? AND `id` > ?)))") {
		t.Errorf("sargable keyset predicate missing: %s", query)
	}
	// MySQL 按 ? 顺序逐个绑定：>= 出现 1 次、OR 内 > 与 = 各 1 次、tie 1 次、LIMIT 1 次。
	if len(args) != 5 {
		t.Fatalf("args = %v, want [cursor cursor cursor tie limit]", args)
	}
	for i := 0; i < 3; i++ {
		if _, isTime := args[i].(time.Time); !isTime {
			t.Errorf("args[%d] = %v (%T), want cursor time.Time", i, args[i], args[i])
		}
	}
	if args[3] != int64(10) {
		t.Errorf("tie arg = %v (%T), want int64(10)", args[3], args[3])
	}
	if args[4] != 500 {
		t.Errorf("limit arg = %v, want 500", args[4])
	}
}

func TestBuildQueryIncrementalJoinedSubquery(t *testing.T) {
	query, _, ok, err := BuildQueryForTest(incrementalJoinedJob(), scopeFilter(), checkpoint.Cursor{}, 500, PostgresDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, `FROM (SELECT `) {
		t.Errorf("fact subquery missing: %s", query)
	}
	if strings.Contains(query, `SELECT "o".*`) {
		t.Errorf("fact subquery must project selected base columns, not %s: %s", `SELECT "o".*`, query)
	}
	if !strings.Contains(query, `SELECT "o"."id", "o"."customer_id", "o"."submitted_at" FROM`) {
		t.Errorf("fact subquery must project base columns: %s", query)
	}
	if !strings.Contains(query, "LEFT JOIN \"customers\" AS \"c\"") {
		t.Errorf("join missing: %s", query)
	}
	if !strings.Contains(query, "LIMIT $") {
		t.Errorf("limit must close the subquery: %s", query)
	}
}

func TestBuildQueryInFilterScope(t *testing.T) {
	job := snapshotJob()
	job.Reader.Filters = append(job.Reader.Filters, config.FilterConfig{Column: "code", Op: "in", ValuesFrom: config.ValuesFromScope})
	query, args, ok, err := BuildQueryForTest(job, scopeFilter(), checkpoint.Cursor{}, 500, PostgresDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, `ANY($2::text[])`) {
		t.Errorf("postgres in-filter must use ANY: %s", query)
	}
	if len(args) != 3 {
		t.Fatalf("args = %v", args)
	}
	values, ok := args[1].([]string)
	if !ok || len(values) != 2 {
		t.Errorf("in values = %v", args[1])
	}
}

func TestBuildQueryInFilterMySQLExpandsPlaceholders(t *testing.T) {
	job := snapshotJob()
	job.Reader.Filters = append(job.Reader.Filters, config.FilterConfig{Column: "code", Op: "in", ValuesFrom: config.ValuesFromScope})
	query, args, ok, err := BuildQueryForTest(job, scopeFilter(), checkpoint.Cursor{}, 500, MySQLDialectForTest())
	if err != nil || !ok {
		t.Fatalf("build: %v ok=%t", err, ok)
	}
	if !strings.Contains(query, "`code` IN (?, ?)") {
		t.Errorf("mysql in-filter must expand placeholders: %s", query)
	}
	if len(args) != 4 {
		t.Errorf("args = %v", args)
	}
}

func TestBuildQueryEmptyScopeWithoutAllowAllSkips(t *testing.T) {
	job := snapshotJob()
	job.Reader.Filters = append(job.Reader.Filters, config.FilterConfig{Column: "code", Op: "in", ValuesFrom: config.ValuesFromScope})
	_, _, ok, err := BuildQueryForTest(job, filter.New(filter.Config{}), checkpoint.Cursor{}, 500, PostgresDialectForTest())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if ok {
		t.Error("empty scope without allow_all must skip the read (ok=false)")
	}
}

func TestBuildQueryRejectsInjection(t *testing.T) {
	job := snapshotJob()
	job.Reader.Table = "customers; DROP TABLE x"
	if _, _, _, err := BuildQueryForTest(job, scopeFilter(), checkpoint.Cursor{}, 500, PostgresDialectForTest()); err == nil {
		t.Error("SQL injection in table name must be rejected")
	}
	job = snapshotJob()
	job.Reader.Columns = []string{"code; --"}
	if _, _, _, err := BuildQueryForTest(job, scopeFilter(), checkpoint.Cursor{}, 500, MySQLDialectForTest()); err == nil {
		t.Error("SQL injection in column must be rejected")
	}
}

func TestNextCursorAndJoinValidation(t *testing.T) {
	job := incrementalJoinedJob()
	rows := []model.SourceRow{
		{"id": int64(1), "submitted_at": time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)},
		{"id": int64(2), "submitted_at": time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)},
	}
	if err := validateIncrementalJoinRows(job, rows); err != nil {
		t.Fatalf("distinct keys must validate: %v", err)
	}
	duplicated := []model.SourceRow{rows[0], rows[0]}
	if err := validateIncrementalJoinRows(job, duplicated); err == nil {
		t.Error("duplicate composite keys must be rejected")
	}
}

func TestTimeValueAcceptsRFC3339(t *testing.T) {
	if _, err := timeValue("2026-08-19T01:02:03Z"); err != nil {
		t.Errorf("RFC3339 string must parse: %v", err)
	}
	if _, err := timeValue("not-a-time"); err == nil {
		t.Error("garbage string must fail")
	}
	if _, err := timeValue(42); err == nil {
		t.Error("int must fail")
	}
}
