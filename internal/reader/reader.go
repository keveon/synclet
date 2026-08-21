// Package reader polls the source database read-only.
//
// Contract:
//   - PostgreSQL reader: builds parameterized queries from
//     table + alias + columns + joins + filters; every identifier is
//     validated; arbitrary SQL is rejected;
//   - JOINs are restricted: inner/left equi-joins; filter conditions
//     reference the primary alias only;
//   - incremental mode pages by composite (cursor, tie_breaker) keyset;
//     JOINs apply after the fact batch is selected so LIMIT never truncates
//     expanded rows.
package reader

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/dbutil"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/model"
)

// ReadResult is one read batch plus the next composite cursor for
// incremental jobs.
type ReadResult struct {
	Rows           []model.SourceRow
	NextCheckpoint checkpoint.Cursor
}

// Reader polls a source database. Implementations must be read-only.
type Reader interface {
	Read(ctx context.Context, job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int) (ReadResult, error)
	Close()
}

// dialect abstracts the SQL differences between PostgreSQL and MySQL.
type dialect struct {
	name  string
	quote func(string) (string, error)
}

func (d dialect) paramAt(index int) string {
	if d.name == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

var postgresDialect = dialect{name: "postgres", quote: dbutil.QuotePostgresPath}
var mysqlDialect = dialect{name: "mysql", quote: dbutil.QuoteMySQLPath}

// buildQuery renders the SELECT for a job. It returns ok=false when a
// filter resolves to an empty scope without allow_all, meaning: nothing to
// read this round.
func buildQuery(job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int, d dialect) (string, []any, bool, error) {
	readerCfg := job.Reader
	table, err := d.quote(readerCfg.Table)
	if err != nil {
		return "", nil, false, fmt.Errorf("reader.table: %w", err)
	}

	columns := make([]string, 0, len(readerCfg.Columns))
	for _, column := range readerCfg.Columns {
		quoted, err := d.quote(column)
		if err != nil {
			return "", nil, false, fmt.Errorf("reader column %s: %w", column, err)
		}
		columns = append(columns, quoted)
	}

	if job.CanonicalMode() == "incremental" && len(readerCfg.Joins) > 0 {
		return buildIncrementalJoinedQuery(job, scope, cursor, defaultBatch, d, table, columns)
	}

	query := strings.Builder{}
	query.WriteString("SELECT ")
	query.WriteString(strings.Join(columns, ", "))
	query.WriteString(" FROM ")
	query.WriteString(table)
	if strings.TrimSpace(readerCfg.Alias) != "" {
		alias, err := d.quote(readerCfg.Alias)
		if err != nil {
			return "", nil, false, fmt.Errorf("reader.alias: %w", err)
		}
		query.WriteString(" AS ")
		query.WriteString(alias)
	}
	if err := appendJoins(&query, readerCfg.Joins, d); err != nil {
		return "", nil, false, err
	}
	query.WriteString(" WHERE 1=1")

	var args []any
	for _, f := range readerCfg.Filters {
		fragment, filterArgs, ok, err := buildFilter(f, scope, len(args)+1, d)
		if err != nil {
			return "", nil, false, err
		}
		if !ok {
			return "", nil, false, nil
		}
		if fragment == "" {
			continue
		}
		query.WriteString(" AND ")
		query.WriteString(fragment)
		args = append(args, filterArgs...)
	}

	if job.CanonicalMode() == "incremental" && cursor.Valid() {
		cursorColumn, err := d.quote(qualifyBaseColumn(readerCfg, readerCfg.Cursor.Column))
		if err != nil {
			return "", nil, false, fmt.Errorf("reader.cursor.column: %w", err)
		}
		tieColumn, err := d.quote(qualifyBaseColumn(readerCfg, readerCfg.Cursor.TieBreakerColumn))
		if err != nil {
			return "", nil, false, fmt.Errorf("reader.cursor.tie_breaker_column: %w", err)
		}
		args = appendKeysetPredicate(&query, cursorColumn, tieColumn, cursor, args, d)
	}

	orderBy, err := buildOrderBy(readerCfg, job.CanonicalMode(), d)
	if err != nil {
		return "", nil, false, err
	}
	if orderBy != "" {
		query.WriteString(" ORDER BY ")
		query.WriteString(orderBy)
	}

	limit := readerCfg.BatchSize
	if limit <= 0 {
		limit = defaultBatch
	}
	if limit <= 0 {
		limit = 500
	}
	args = append(args, limit)
	query.WriteString(fmt.Sprintf(" LIMIT %s", d.paramAt(len(args))))

	return query.String(), args, true, nil
}

// buildIncrementalJoinedQuery selects the fact batch first (subquery with
// keyset + LIMIT), then applies JOINs, so LIMIT never truncates expanded
// rows.
func buildIncrementalJoinedQuery(job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int, d dialect, table string, columns []string) (string, []any, bool, error) {
	readerCfg := job.Reader
	aliasName := strings.TrimSpace(readerCfg.Alias)
	if aliasName == "" || strings.Contains(aliasName, ".") {
		return "", nil, false, fmt.Errorf("reader.alias must be a single identifier for incremental joins")
	}
	alias, err := d.quote(aliasName)
	if err != nil {
		return "", nil, false, fmt.Errorf("reader.alias: %w", err)
	}

	projections, err := baseColumnProjections(readerCfg, d)
	if err != nil {
		return "", nil, false, err
	}
	if len(projections) == 0 {
		return "", nil, false, fmt.Errorf("reader.columns must include at least one %q column for incremental joins", aliasName)
	}

	query := strings.Builder{}
	query.WriteString("SELECT ")
	query.WriteString(strings.Join(columns, ", "))
	query.WriteString(" FROM (SELECT ")
	query.WriteString(strings.Join(projections, ", "))
	query.WriteString(" FROM ")
	query.WriteString(table)
	query.WriteString(" AS ")
	query.WriteString(alias)
	query.WriteString(" WHERE 1=1")

	var args []any
	for i, f := range readerCfg.Filters {
		parts := strings.Split(strings.TrimSpace(f.Column), ".")
		if len(parts) != 2 || parts[0] != aliasName {
			return "", nil, false, fmt.Errorf("reader.filters[%d] must reference base alias %q for incremental joins", i, aliasName)
		}
		fragment, filterArgs, ok, err := buildFilter(f, scope, len(args)+1, d)
		if err != nil {
			return "", nil, false, err
		}
		if !ok {
			return "", nil, false, nil
		}
		if fragment == "" {
			continue
		}
		query.WriteString(" AND ")
		query.WriteString(fragment)
		args = append(args, filterArgs...)
	}

	if cursor.Valid() {
		cursorColumn, err := d.quote(qualifyBaseColumn(readerCfg, readerCfg.Cursor.Column))
		if err != nil {
			return "", nil, false, fmt.Errorf("reader.cursor.column: %w", err)
		}
		tieColumn, err := d.quote(qualifyBaseColumn(readerCfg, readerCfg.Cursor.TieBreakerColumn))
		if err != nil {
			return "", nil, false, fmt.Errorf("reader.cursor.tie_breaker_column: %w", err)
		}
		args = appendKeysetPredicate(&query, cursorColumn, tieColumn, cursor, args, d)
	}

	orderBy, err := buildOrderBy(readerCfg, "incremental", d)
	if err != nil {
		return "", nil, false, err
	}
	query.WriteString(" ORDER BY ")
	query.WriteString(orderBy)

	limit := readerCfg.BatchSize
	if limit <= 0 {
		limit = defaultBatch
	}
	if limit <= 0 {
		limit = 500
	}
	args = append(args, limit)
	query.WriteString(fmt.Sprintf(" LIMIT %s)", d.paramAt(len(args))))
	query.WriteString(" AS ")
	query.WriteString(alias)
	if err := appendJoins(&query, readerCfg.Joins, d); err != nil {
		return "", nil, false, err
	}
	query.WriteString(" ORDER BY ")
	query.WriteString(orderBy)

	return query.String(), args, true, nil
}

// appendKeysetPredicate appends the composite (cursor, tie_breaker) keyset
// predicate and binds its arguments. The predicate is deliberately redundant:
//
//	(cursor >= $c AND (cursor > $c OR (cursor = $c AND tie > $t)))
//
// The bare OR form is semantically identical but is not sargable — without
// the leading >= conjunct, planners cannot use the cursor boundary as an
// index start condition and degrade to scanning (and discarding) every row
// at or below the cursor on each poll.
func appendKeysetPredicate(query *strings.Builder, cursorColumn, tieColumn string, cursor checkpoint.Cursor, args []any, d dialect) []any {
	first := len(args) + 1
	if d.name == "mysql" {
		// MySQL consumes one argument per ?: >= once, then > and =
		// inside the OR, then the tie breaker.
		args = append(args, cursor.Value, cursor.Value, cursor.Value, cursor.Tie)
	} else {
		// PostgreSQL placeholders can be reused ($n passed once).
		args = append(args, cursor.Value, cursor.Tie)
	}
	var cursorPlaceholders []string
	if d.name == "mysql" {
		cursorPlaceholders = []string{d.paramAt(first), d.paramAt(first + 1), d.paramAt(first + 2)}
	} else {
		cursorPlaceholders = []string{d.paramAt(first), d.paramAt(first), d.paramAt(first)}
	}
	tiePlaceholder := d.paramAt(len(args))

	query.WriteString(" AND (")
	query.WriteString(fmt.Sprintf("%s >= %s", cursorColumn, cursorPlaceholders[0]))
	query.WriteString(" AND (")
	query.WriteString(fmt.Sprintf("%s > %s", cursorColumn, cursorPlaceholders[1]))
	query.WriteString(" OR ")
	query.WriteString(fmt.Sprintf("(%s = %s AND %s > %s)",
		cursorColumn, cursorPlaceholders[2], tieColumn, tiePlaceholder))
	query.WriteString("))")
	return args
}

// baseColumnProjections returns the quoted projection for the fact subquery
// of an incremental joined read: only the selected columns that belong to the
// base alias. Projecting alias.* would drag every table column (including
// large unused payloads) through the batch scan for no benefit.
func baseColumnProjections(readerCfg config.ReaderConfig, d dialect) ([]string, error) {
	aliasName := strings.TrimSpace(readerCfg.Alias)
	prefix := aliasName + "."
	projections := make([]string, 0, len(readerCfg.Columns))
	for _, column := range readerCfg.Columns {
		if !strings.HasPrefix(column, prefix) {
			continue
		}
		quoted, err := d.quote(column)
		if err != nil {
			return nil, fmt.Errorf("reader column %s: %w", column, err)
		}
		projections = append(projections, quoted)
	}
	return projections, nil
}

func appendJoins(query *strings.Builder, joins []config.JoinConfig, d dialect) error {
	for i, join := range joins {
		joinType := strings.ToUpper(strings.TrimSpace(join.Type))
		switch joinType {
		case "INNER", "LEFT":
		default:
			return fmt.Errorf("reader.joins[%d].type must be inner or left", i)
		}
		joinTable, err := d.quote(join.Table)
		if err != nil {
			return fmt.Errorf("reader.joins[%d].table: %w", i, err)
		}
		joinAliasName := strings.TrimSpace(join.Alias)
		if joinAliasName == "" || strings.Contains(joinAliasName, ".") {
			return fmt.Errorf("reader.joins[%d].alias must be a single identifier", i)
		}
		joinAlias, err := d.quote(joinAliasName)
		if err != nil {
			return fmt.Errorf("reader.joins[%d].alias: %w", i, err)
		}
		left, err := d.quote(join.On.Left)
		if err != nil {
			return fmt.Errorf("reader.joins[%d].on.left: %w", i, err)
		}
		right, err := d.quote(join.On.Right)
		if err != nil {
			return fmt.Errorf("reader.joins[%d].on.right: %w", i, err)
		}
		query.WriteString(" ")
		query.WriteString(joinType)
		query.WriteString(" JOIN ")
		query.WriteString(joinTable)
		query.WriteString(" AS ")
		query.WriteString(joinAlias)
		query.WriteString(" ON ")
		query.WriteString(left)
		query.WriteString(" = ")
		query.WriteString(right)
	}
	return nil
}

func buildFilter(f config.FilterConfig, scope filter.Filter, nextArg int, d dialect) (string, []any, bool, error) {
	column, err := d.quote(f.Column)
	if err != nil {
		return "", nil, false, fmt.Errorf("filter column %s: %w", f.Column, err)
	}
	op := strings.TrimSpace(f.Op)
	switch op {
	case "eq":
		if f.ValuesFrom != "" {
			return "", nil, false, fmt.Errorf("eq filter does not support values_from")
		}
		return fmt.Sprintf("%s = %s", column, d.paramAt(nextArg)), []any{f.Value}, true, nil
	case "in":
		values, skip, ok, err := filterValues(f, scope)
		if err != nil || !ok || skip {
			return "", nil, ok, err
		}
		if d.name == "postgres" {
			return fmt.Sprintf("%s = ANY(%s::text[])", column, d.paramAt(nextArg)), []any{values}, true, nil
		}
		placeholders := make([]string, 0, len(values))
		for range values {
			placeholders = append(placeholders, "?")
		}
		return fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ", ")), toAnySlice(values), true, nil
	default:
		return "", nil, false, fmt.Errorf("unsupported filter op %q", op)
	}
}

func filterValues(f config.FilterConfig, scope filter.Filter) ([]string, bool, bool, error) {
	if f.ValuesFrom == config.ValuesFromScope {
		if scope.AllowAll() {
			return nil, true, true, nil
		}
		codes := scope.AllowedCodes()
		if len(codes) == 0 {
			return nil, false, false, nil
		}
		return codes, false, true, nil
	}
	if f.ValuesFrom != "" {
		return nil, false, false, fmt.Errorf("unsupported values_from %q", f.ValuesFrom)
	}
	switch typed := f.Value.(type) {
	case []string:
		return typed, false, len(typed) > 0, nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, v := range typed {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				values = append(values, s)
			}
		}
		return values, false, len(values) > 0, nil
	default:
		return nil, false, false, fmt.Errorf("in filter value must be a string list")
	}
}

func buildOrderBy(readerCfg config.ReaderConfig, mode string, d dialect) (string, error) {
	columns := readerCfg.OrderBy
	if mode == "incremental" {
		columns = []string{readerCfg.Cursor.Column, readerCfg.Cursor.TieBreakerColumn}
	}
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		q, err := d.quote(qualifyBaseColumn(readerCfg, column))
		if err != nil {
			return "", fmt.Errorf("order column %s: %w", column, err)
		}
		quoted = append(quoted, q+" ASC")
	}
	return strings.Join(quoted, ", "), nil
}

func qualifyBaseColumn(readerCfg config.ReaderConfig, column string) string {
	column = strings.TrimSpace(column)
	alias := strings.TrimSpace(readerCfg.Alias)
	if alias == "" || strings.Contains(column, ".") {
		return column
	}
	return alias + "." + column
}

// ResultColumnName returns the unqualified result column name.
func ResultColumnName(column string) string {
	parts := strings.Split(strings.TrimSpace(column), ".")
	return parts[len(parts)-1]
}

// nextCursor extracts the composite cursor from the last row of a batch.
func nextCursor(job config.JobConfig, rows []model.SourceRow) (checkpoint.Cursor, error) {
	if len(rows) == 0 {
		return checkpoint.Cursor{}, nil
	}
	last := rows[len(rows)-1]
	cursorValue, err := timeValue(last[ResultColumnName(job.Reader.Cursor.Column)])
	if err != nil {
		return checkpoint.Cursor{}, fmt.Errorf("cursor column %s: %w", job.Reader.Cursor.Column, err)
	}
	tieValue := last[ResultColumnName(job.Reader.Cursor.TieBreakerColumn)]
	tie, err := checkpoint.CanonicalizeTie(tieValue)
	if err != nil {
		return checkpoint.Cursor{}, fmt.Errorf("tie breaker column %s: %w", job.Reader.Cursor.TieBreakerColumn, err)
	}
	if tie == nil {
		return checkpoint.Cursor{}, fmt.Errorf("tie breaker column %s is empty", job.Reader.Cursor.TieBreakerColumn)
	}
	return checkpoint.Cursor{Value: cursorValue, Tie: tie}, nil
}

// validateIncrementalJoinRows rejects join expansion of the same base row:
// duplicate composite keys within a batch mean the join expanded rows and
// the batch boundaries could duplicate or reorder rows.
func validateIncrementalJoinRows(job config.JobConfig, rows []model.SourceRow) error {
	if job.CanonicalMode() != "incremental" || len(job.Reader.Joins) == 0 || len(rows) < 2 {
		return nil
	}

	seen := map[string]struct{}{}
	for i, row := range rows {
		cursorValue, err := timeValue(row[ResultColumnName(job.Reader.Cursor.Column)])
		if err != nil {
			return fmt.Errorf("cursor column %s: %w", job.Reader.Cursor.Column, err)
		}
		tieValue := row[ResultColumnName(job.Reader.Cursor.TieBreakerColumn)]
		tie, err := checkpoint.CanonicalizeTie(tieValue)
		if err != nil {
			return fmt.Errorf("tie breaker column %s: %w", job.Reader.Cursor.TieBreakerColumn, err)
		}
		tieString, ok := checkpoint.TieString(tie)
		if !ok {
			return fmt.Errorf("tie breaker column %s is empty", job.Reader.Cursor.TieBreakerColumn)
		}
		key := fmt.Sprintf("%s|%s", cursorValue.UTC().Format(time.RFC3339Nano), tieString)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("join expanded base row; joined keys must be unique (row %d)", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func timeValue(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed, nil
		}
		return time.Time{}, fmt.Errorf("expected RFC3339 time string")
	case []byte:
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(typed))); err == nil {
			return parsed, nil
		}
		return time.Time{}, fmt.Errorf("expected RFC3339 time string")
	default:
		return time.Time{}, fmt.Errorf("expected time-compatible value, got %T", value)
	}
}

func toAnySlice(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// BuildQueryForTest exposes the query builder for tests.
func BuildQueryForTest(job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int, d dialect) (string, []any, bool, error) {
	return buildQuery(job, scope, cursor, defaultBatch, d)
}

// PostgresDialectForTest exposes the postgres dialect for tests.
func PostgresDialectForTest() dialect {
	return postgresDialect
}

// MySQLDialectForTest exposes the mysql dialect for tests.
func MySQLDialectForTest() dialect {
	return mysqlDialect
}
