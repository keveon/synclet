// Package writer upserts mapped records into the target database.
//
// Contract:
//   - MySQL writer: idempotent ON DUPLICATE KEY UPDATE upserts;
//   - PostgreSQL writer: idempotent ON CONFLICT DO UPDATE upserts;
//   - null_update_policy=keep_existing keeps existing target values when a
//     round maps to NULL;
//   - json_merge_patch_columns merge instead of overwriting (RFC 7386
//     semantics; MySQL uses JSON_MERGE_PATCH, PostgreSQL merges in Go and
//     writes the full value);
//   - DATETIME values are written as local literals per writer timezone;
//   - distinguishes attempted/inserted/updated/unchanged in write stats.
package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/model"
)

// WriteResult carries the write stats of one round.
type WriteResult struct {
	AttemptedRows int
	// Inserted counts rows that did not exist before this upsert.
	Inserted int64
	// Updated counts rows that existed and whose values changed.
	Updated int64
	// Unchanged counts rows that existed with identical values.
	Unchanged int64
}

// Writer upserts records into a target database.
type Writer interface {
	Write(ctx context.Context, job config.JobConfig, records []model.Record) (WriteResult, error)
	Close() error
}

// transaction abstracts a database transaction for the write loop.
type transaction interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Commit() error
	Rollback() error
}

// writerLocation resolves the writer timezone, defaulting to UTC.
func writerLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "UTC"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load writer timezone %s: %w", name, err)
	}
	return location, nil
}

// formatDateTime renders a time as a local literal with millisecond
// precision, for DATETIME/TIMESTAMP columns.
func formatDateTime(value time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	return value.In(location).Format("2006-01-02 15:04:05.000")
}

// formatArg converts a mapped value into a driver argument.
func formatArg(value any, location *time.Location) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case time.Time:
		return formatDateTime(typed, location)
	case decimal.Decimal:
		return typed.String()
	case *decimal.Decimal:
		if typed == nil {
			return nil
		}
		return typed.String()
	case json.Number:
		return typed.String()
	case map[string]any, []any:
		data, err := json.Marshal(typed)
		if err != nil {
			return "{}"
		}
		return string(data)
	default:
		return typed
	}
}

// MergeJSONPatch applies RFC 7386 merge-patch semantics in Go: null patch
// members delete keys; other members replace recursively.
func MergeJSONPatch(current []byte, patch map[string]any) ([]byte, error) {
	var currentMap map[string]any
	trimmed := bytes.TrimSpace(current)
	if len(trimmed) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&currentMap); err != nil {
			return nil, fmt.Errorf("decode current JSON: %w", err)
		}
	}
	if currentMap == nil {
		currentMap = map[string]any{}
	}
	return json.Marshal(mergeMaps(currentMap, patch))
}

func mergeMaps(current map[string]any, patch map[string]any) map[string]any {
	out := make(map[string]any, len(current)+len(patch))
	for key, value := range current {
		out[key] = value
	}
	for key, value := range patch {
		if value == nil {
			delete(out, key)
			continue
		}
		patchMap, patchIsMap := value.(map[string]any)
		if patchIsMap {
			currentMap, currentIsMap := out[key].(map[string]any)
			if currentIsMap {
				out[key] = mergeMaps(currentMap, patchMap)
				continue
			}
		}
		out[key] = value
	}
	return out
}

// classifyMySQLAffected classifies a MySQL affected-rows count: 0 =
// unchanged, 1 = inserted, 2 = updated (default for ON DUPLICATE KEY
// UPDATE with values changed; the driver must not set CLIENT_FOUND_ROWS).
func classifyMySQLAffected(affected int64) (inserted, updated, unchanged int64) {
	switch affected {
	case 0:
		return 0, 0, 1
	case 1:
		return 1, 0, 0
	default:
		return 0, 1, 0
	}
}
