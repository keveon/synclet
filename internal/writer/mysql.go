package writer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/dbutil"
	"github.com/keveon/synclet/internal/model"
)

// MySQLWriter upserts into a MySQL target.
type MySQLWriter struct {
	db *sql.DB
}

// OpenMySQL opens a pinged MySQL writer. The DSN may be native or a
// mysql:// URL; parseTime is forced on and CLIENT_FOUND_ROWS is NOT set,
// so affected-rows classification stays meaningful.
func OpenMySQL(ctx context.Context, dsn string) (*MySQLWriter, error) {
	cfg, err := dbutil.ParseMySQLDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse target mysql DSN: %w", err)
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open target mysql: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping target mysql: %w", err)
	}
	return &MySQLWriter{db: db}, nil
}

// NewMySQL wraps an existing *sql.DB.
func NewMySQL(db *sql.DB) *MySQLWriter {
	return &MySQLWriter{db: db}
}

// Close closes the pool.
func (w *MySQLWriter) Close() error {
	if w == nil || w.db == nil {
		return nil
	}
	return w.db.Close()
}

// Write upserts records in one transaction.
func (w *MySQLWriter) Write(ctx context.Context, job config.JobConfig, records []model.Record) (WriteResult, error) {
	result := WriteResult{AttemptedRows: len(records)}
	if len(records) == 0 {
		return result, nil
	}
	sqlText, columns, err := BuildMySQLUpsert(job.Writer)
	if err != nil {
		return result, err
	}
	location, err := writerLocation(job.Writer.Timezone)
	if err != nil {
		return result, err
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	return writeMySQLRecords(ctx, tx, sqlText, columns, location, records)
}

func writeMySQLRecords(ctx context.Context, tx transaction, sqlText string, columns []string, location *time.Location, records []model.Record) (WriteResult, error) {
	result := WriteResult{AttemptedRows: len(records)}
	defer tx.Rollback()

	var inserted, updated, unchanged int64
	for _, record := range records {
		args := make([]any, 0, len(columns))
		for _, column := range columns {
			args = append(args, formatArg(record.Fields[column], location))
		}
		execResult, err := tx.ExecContext(ctx, sqlText, args...)
		if err != nil {
			return result, err
		}
		affected, err := execResult.RowsAffected()
		if err != nil {
			return result, err
		}
		i, u, un := classifyMySQLAffected(affected)
		inserted += i
		updated += u
		unchanged += un
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Inserted = inserted
	result.Updated = updated
	result.Unchanged = unchanged
	return result, nil
}

// BuildMySQLUpsert renders the ON DUPLICATE KEY UPDATE statement.
func BuildMySQLUpsert(writerCfg config.WriterConfig) (string, []string, error) {
	table, err := dbutil.QuoteMySQLPath(writerCfg.Table)
	if err != nil {
		return "", nil, fmt.Errorf("writer.table: %w", err)
	}
	columns := append([]string{}, writerCfg.KeyColumns...)
	columns = append(columns, writerCfg.UpdateColumns...)
	columns = dedupe(columns)
	if len(columns) == 0 {
		return "", nil, fmt.Errorf("writer columns are required")
	}

	quotedColumns := make([]string, 0, len(columns))
	placeholders := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted, err := dbutil.QuoteMySQLPath(column)
		if err != nil {
			return "", nil, fmt.Errorf("writer column %s: %w", column, err)
		}
		quotedColumns = append(quotedColumns, quoted)
		placeholders = append(placeholders, "?")
	}

	jsonMerge := stringSet(writerCfg.JSONMergePatchColumns)
	keySet := stringSet(writerCfg.KeyColumns)
	keepExisting := strings.TrimSpace(writerCfg.NullUpdatePolicy) == "keep_existing"
	updates := make([]string, 0, len(writerCfg.UpdateColumns))
	for _, column := range writerCfg.UpdateColumns {
		if keySet[column] {
			continue
		}
		quoted, err := dbutil.QuoteMySQLPath(column)
		if err != nil {
			return "", nil, fmt.Errorf("writer update column %s: %w", column, err)
		}
		switch {
		case jsonMerge[column]:
			updates = append(updates, fmt.Sprintf("%s = COALESCE(JSON_MERGE_PATCH(%s.%s, VALUES(%s)), VALUES(%s))", quoted, table, quoted, quoted, quoted))
		case keepExisting:
			updates = append(updates, fmt.Sprintf("%s = COALESCE(VALUES(%s), %s.%s)", quoted, quoted, table, quoted))
		default:
			updates = append(updates, fmt.Sprintf("%s = VALUES(%s)", quoted, quoted))
		}
	}
	if len(updates) == 0 {
		return "", nil, fmt.Errorf("writer.update_columns must contain at least one non-key column")
	}

	sqlText := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON DUPLICATE KEY UPDATE %s",
		table,
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(updates, ", "),
	)
	return sqlText, columns, nil
}

// UpsertSQLForTest exposes the upsert builder for tests.
func UpsertSQLForTest(writerCfg config.WriterConfig) (string, []string, error) {
	return BuildMySQLUpsert(writerCfg)
}

func dedupe(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		set[strings.TrimSpace(value)] = true
	}
	return set
}
