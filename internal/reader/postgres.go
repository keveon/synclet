package reader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/model"
)

// PostgresReader polls a PostgreSQL source read-only.
type PostgresReader struct {
	pool *pgxpool.Pool
}

// OpenPostgres opens a pooled, pinged PostgreSQL reader.
func OpenPostgres(ctx context.Context, dsn string) (*PostgresReader, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse source postgres dsn: %w", err)
	}

	previousAfterConnect := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previousAfterConnect != nil {
			if err := previousAfterConnect(ctx, conn); err != nil {
				return err
			}
		}
		registerExactJSONCodecs(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open source postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping source postgres: %w", err)
	}
	return &PostgresReader{pool: pool}, nil
}

func registerExactJSONCodecs(typeMap *pgtype.Map) {
	typeMap.RegisterType(&pgtype.Type{
		Name:  "json",
		OID:   pgtype.JSONOID,
		Codec: &pgtype.JSONCodec{Marshal: json.Marshal, Unmarshal: unmarshalJSONUseNumber},
	})
	typeMap.RegisterType(&pgtype.Type{
		Name:  "jsonb",
		OID:   pgtype.JSONBOID,
		Codec: &pgtype.JSONBCodec{Marshal: json.Marshal, Unmarshal: unmarshalJSONUseNumber},
	})
}

func unmarshalJSONUseNumber(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// Close closes the pool.
func (r *PostgresReader) Close() {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
}

// Read executes one poll batch.
func (r *PostgresReader) Read(ctx context.Context, job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int) (ReadResult, error) {
	query, args, ok, err := buildQuery(job, scope, cursor, defaultBatch, postgresDialect)
	if err != nil {
		return ReadResult{}, err
	}
	if !ok {
		return ReadResult{}, nil
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return ReadResult{}, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var out []model.SourceRow
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return ReadResult{}, err
		}
		row := make(model.SourceRow, len(values))
		for i, value := range values {
			row[string(fields[i].Name)] = value
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
