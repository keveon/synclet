package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/dbutil"
	"github.com/keveon/synclet/internal/model"
)

// PostgresWriter upserts into a PostgreSQL target.
//
// Inserted vs updated is classified by RETURNING (xmax = 0): fresh inserts
// report xmax = 0, rewrites of existing rows report non-zero. The
// unchanged count stays 0 on PostgreSQL: the server rewrites the row
// regardless, so conservatively classifying every conflict as updated is
// honest. null_update_policy=keep_existing maps to
// COALESCE(EXCLUDED.col, table.col) in the UPDATE clause. JSON
// merge-patch columns are merged in Go (RFC 7386): the current document
// is read with SELECT ... FOR UPDATE inside the same transaction, merged,
// and the merged document is written by the upsert.
type PostgresWriter struct {
	pool *pgxpool.Pool
}

// OpenPostgres opens a pooled, pinged PostgreSQL writer.
func OpenPostgres(ctx context.Context, dsn string) (*PostgresWriter, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open target postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping target postgres: %w", err)
	}
	return &PostgresWriter{pool: pool}, nil
}

// Close closes the pool.
func (w *PostgresWriter) Close() error {
	if w == nil || w.pool == nil {
		return nil
	}
	w.pool.Close()
	return nil
}

// pgStatements bundles the per-job SQL.
type pgStatements struct {
	upsertSQL string
	// selectSQL reads the current JSON of merge-patch columns for one
	// row, locked. Empty when the job has no merge-patch columns.
	selectSQL  string
	columns    []string
	keyColumns int
}

// Write upserts records in one transaction per round.
func (w *PostgresWriter) Write(ctx context.Context, job config.JobConfig, records []model.Record) (WriteResult, error) {
	result := WriteResult{AttemptedRows: len(records)}
	if len(records) == 0 {
		return result, nil
	}
	location, err := writerLocation(job.Writer.Timezone)
	if err != nil {
		return result, err
	}
	mergeSet := stringSet(job.Writer.JSONMergePatchColumns)

	stmts, err := buildPostgresStatements(job.Writer)
	if err != nil {
		return result, err
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)

	var inserted, updated int64
	for _, record := range records {
		args := make([]any, 0, len(stmts.columns))
		for _, column := range stmts.columns {
			args = append(args, formatArg(record.Fields[column], location))
		}

		if len(mergeSet) > 0 {
			merged, err := mergePostgresJSON(ctx, tx, stmts, args, record, mergeSet)
			if err != nil {
				return result, err
			}
			args = merged
		}

		var freshInsert bool
		if err := tx.QueryRow(ctx, stmts.upsertSQL, args...).Scan(&freshInsert); err != nil {
			return result, err
		}
		if freshInsert {
			inserted++
		} else {
			updated++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Inserted = inserted
	result.Updated = updated
	return result, nil
}

// mergePostgresJSON reads the current JSON documents of the merge-patch
// columns for one row (SELECT ... FOR UPDATE), applies RFC 7386
// merge-patch in Go, and rewrites the corresponding upsert args.
func mergePostgresJSON(ctx context.Context, tx pgx.Tx, stmts pgStatements, args []any, record model.Record, mergeSet map[string]bool) ([]any, error) {
	scan := make([]any, len(stmts.columns))
	ptrs := make([]any, len(stmts.columns))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	err := tx.QueryRow(ctx, stmts.selectSQL, args[:stmts.keyColumns]...).Scan(ptrs...)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read current row: %w", err)
	}

	for i, column := range stmts.columns {
		if !mergeSet[column] {
			continue
		}
		patch, _ := record.Fields[column].(map[string]any)
		var current []byte
		if err == nil {
			current, _ = scan[i].([]byte)
		}
		merged, mergeErr := MergeJSONPatch(current, patch)
		if mergeErr != nil {
			return nil, fmt.Errorf("column %s: %w", column, mergeErr)
		}
		args[i] = string(merged)
	}
	return args, nil
}

// buildPostgresStatements renders the upsert (and the locking select when
// merge-patch columns exist).
func buildPostgresStatements(writerCfg config.WriterConfig) (pgStatements, error) {
	table, err := dbutil.QuotePostgresPath(writerCfg.Table)
	if err != nil {
		return pgStatements{}, fmt.Errorf("writer.table: %w", err)
	}

	columns := append([]string{}, writerCfg.KeyColumns...)
	columns = append(columns, writerCfg.UpdateColumns...)
	columns = dedupe(columns)
	if len(columns) == 0 {
		return pgStatements{}, fmt.Errorf("writer columns are required")
	}

	quotedColumns := make([]string, 0, len(columns))
	placeholders := make([]string, 0, len(columns))
	for i, column := range columns {
		quoted, err := dbutil.QuotePostgresPath(column)
		if err != nil {
			return pgStatements{}, fmt.Errorf("writer column %s: %w", column, err)
		}
		quotedColumns = append(quotedColumns, quoted)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}

	mergeSet := stringSet(writerCfg.JSONMergePatchColumns)
	keySet := stringSet(writerCfg.KeyColumns)
	keepExisting := strings.TrimSpace(writerCfg.NullUpdatePolicy) == "keep_existing"

	quotedKeys := make([]string, 0, len(writerCfg.KeyColumns))
	for _, column := range writerCfg.KeyColumns {
		quoted, err := dbutil.QuotePostgresPath(column)
		if err != nil {
			return pgStatements{}, fmt.Errorf("writer key column %s: %w", column, err)
		}
		quotedKeys = append(quotedKeys, quoted)
	}

	updates := make([]string, 0, len(writerCfg.UpdateColumns))
	for _, column := range writerCfg.UpdateColumns {
		if keySet[column] {
			continue
		}
		quoted, err := dbutil.QuotePostgresPath(column)
		if err != nil {
			return pgStatements{}, fmt.Errorf("writer update column %s: %w", column, err)
		}
		switch {
		case mergeSet[column]:
			// The merged document replaces the column; merge happened in Go.
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", quoted, quoted))
		case keepExisting:
			updates = append(updates, fmt.Sprintf("%s = COALESCE(EXCLUDED.%s, %s.%s)", quoted, quoted, table, quoted))
		default:
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", quoted, quoted))
		}
	}
	if len(updates) == 0 {
		return pgStatements{}, fmt.Errorf("writer.update_columns must contain at least one non-key column")
	}

	upsertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s RETURNING (xmax = 0)",
		table,
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(quotedKeys, ", "),
		strings.Join(updates, ", "),
	)

	stmts := pgStatements{upsertSQL: upsertSQL, columns: columns, keyColumns: len(writerCfg.KeyColumns)}

	if len(mergeSet) > 0 {
		var b strings.Builder
		b.WriteString("SELECT ")
		first := true
		for _, column := range columns {
			if !mergeSet[column] {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			quoted, err := dbutil.QuotePostgresPath(column)
			if err != nil {
				return pgStatements{}, fmt.Errorf("writer column %s: %w", column, err)
			}
			fmt.Fprintf(&b, "%s::text", quoted)
		}
		b.WriteString(" FROM ")
		b.WriteString(table)
		b.WriteString(" WHERE ")
		for i := range quotedKeys {
			if i > 0 {
				b.WriteString(" AND ")
			}
			column := writerCfg.KeyColumns[i]
			quoted, err := dbutil.QuotePostgresPath(column)
			if err != nil {
				return pgStatements{}, fmt.Errorf("writer key column %s: %w", column, err)
			}
			fmt.Fprintf(&b, "%s = $%d", quoted, i+1)
		}
		b.WriteString(" FOR UPDATE")
		stmts.selectSQL = b.String()
	}

	return stmts, nil
}

// BuildPostgresUpsertForTest exposes the statement builder for tests.
func BuildPostgresUpsertForTest(writerCfg config.WriterConfig) (upsertSQL string, selectSQL string, columns []string, err error) {
	stmts, err := buildPostgresStatements(writerCfg)
	if err != nil {
		return "", "", nil, err
	}
	return stmts.upsertSQL, stmts.selectSQL, stmts.columns, nil
}
