package reader

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/dbutil"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/model"
)

// MySQLReader polls a MySQL source read-only.
type MySQLReader struct {
	db *sql.DB
}

// OpenMySQL opens a pinged MySQL reader. The DSN may be native or a
// mysql:// URL; parseTime is forced on so cursor columns decode as
// time.Time.
func OpenMySQL(ctx context.Context, dsn string) (*MySQLReader, error) {
	cfg, err := dbutil.ParseMySQLDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse source mysql DSN: %w", err)
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open source mysql: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping source mysql: %w", err)
	}
	return &MySQLReader{db: db}, nil
}

// Close closes the connection pool.
func (r *MySQLReader) Close() {
	if r != nil && r.db != nil {
		_ = r.db.Close()
	}
}

// Read executes one poll batch.
func (r *MySQLReader) Read(ctx context.Context, job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int) (ReadResult, error) {
	query, args, ok, err := buildQuery(job, scope, cursor, defaultBatch, mysqlDialect)
	if err != nil {
		return ReadResult{}, err
	}
	if !ok {
		return ReadResult{}, nil
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ReadResult{}, err
	}
	defer rows.Close()

	fields, err := rows.Columns()
	if err != nil {
		return ReadResult{}, err
	}
	scan := make([]any, len(fields))
	ptrs := make([]any, len(fields))
	for i := range scan {
		ptrs[i] = &scan[i]
	}

	var out []model.SourceRow
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return ReadResult{}, err
		}
		row := make(model.SourceRow, len(fields))
		for i, field := range fields {
			row[field] = normalizeMySQLValue(scan[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return ReadResult{}, err
	}
	if err := validateIncrementalJoinRows(job, out); err != nil {
		return ReadResult{}, err
	}

	result := ReadResult{Rows: out}
	if job.CanonicalMode() == "incremental" && len(out) > 0 {
		next, err := nextCursor(job, out)
		if err != nil {
			return ReadResult{}, err
		}
		result.NextCheckpoint = next
	}
	return result, nil
}

// normalizeMySQLValue passes raw scan values through; mapping.NormalizeMap
// handles JSON detection for []byte columns.
func normalizeMySQLValue(value any) any {
	return value
}
