package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/mapping"
	"github.com/keveon/synclet/internal/model"
	"github.com/keveon/synclet/internal/reader"
	"github.com/keveon/synclet/internal/writer"
)

// fakeReader returns canned rows.
type fakeReader struct {
	rows []model.SourceRow
	next checkpoint.Cursor
}

func (f *fakeReader) Read(ctx context.Context, job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int) (reader.ReadResult, error) {
	return reader.ReadResult{Rows: f.rows, NextCheckpoint: f.next}, nil
}
func (f *fakeReader) Close() {}

// fakeWriter records writes.
type fakeWriter struct {
	records []model.Record
	result  writer.WriteResult
	err     error
}

func (f *fakeWriter) Write(ctx context.Context, job config.JobConfig, records []model.Record) (writer.WriteResult, error) {
	if f.err != nil {
		return writer.WriteResult{}, f.err
	}
	f.records = append(f.records, records...)
	return f.result, nil
}
func (f *fakeWriter) Close() error { return nil }

type memStore struct {
	state checkpoint.State
	saves int
}

func (m *memStore) Load() (checkpoint.State, error) { return m.state, nil }
func (m *memStore) Save(s checkpoint.State) error   { m.state = s; m.saves++; return nil }

type bufLogger struct{ lines []string }

func (b *bufLogger) Printf(format string, args ...any) {
	b.lines = append(b.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func jsonNumber(s string) json.Number {
	return json.Number(s)
}

func simpleJob(mode string) config.JobConfig {
	return config.JobConfig{
		Name: "orders",
		Mode: mode,
		Reader: config.ReaderConfig{
			Table:   "orders",
			Columns: []string{"id", "code"},
			Cursor:  config.CursorConfig{Column: "updated_at", TieBreakerColumn: "id"},
		},
		Mapping: mapping.Config{Fields: map[string]mapping.ValueMapping{
			"code":  {Type: "column", Column: "code", Required: true},
			"value": {Type: "column", Column: "value"},
		}},
		Writer: config.WriterConfig{Table: "orders", KeyColumns: []string{"code"}, UpdateColumns: []string{"value"}},
	}
}

func TestRunOnceHappyPath(t *testing.T) {
	rdr := &fakeReader{rows: []model.SourceRow{
		{"id": int64(1), "code": "C001", "value": jsonNumber("10")},
		{"id": int64(2), "code": "C002", "value": jsonNumber("20")},
	}}
	wtr := &fakeWriter{result: writer.WriteResult{Inserted: 2}}
	store := &memStore{}
	logger := &bufLogger{}

	eng, err := New(rdr, wtr, Options{
		CheckpointStore: store,
		Scope:           filter.New(filter.Config{AllowAll: true}),
		Jobs:            []config.JobConfig{simpleJob("snapshot")},
		Logger:          logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(wtr.records) != 2 {
		t.Fatalf("written records = %d, want 2", len(wtr.records))
	}
	if store.saves != 0 {
		t.Error("snapshot mode must not save checkpoints")
	}
	if len(logger.lines) != 3 {
		t.Errorf("log lines = %d: %v", len(logger.lines), logger.lines)
	}
}

func TestRunOnceIncrementalAdvancesCheckpoint(t *testing.T) {
	rdr := &fakeReader{
		rows: []model.SourceRow{{"id": int64(5), "code": "C001", "updated_at": time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)}},
		next: checkpoint.Cursor{Value: time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC), Tie: int64(5)},
	}
	wtr := &fakeWriter{result: writer.WriteResult{Updated: 1}}
	store := &memStore{}
	logger := &bufLogger{}

	eng, err := New(rdr, wtr, Options{
		CheckpointStore: store,
		Scope:           filter.New(filter.Config{AllowAll: true}),
		Jobs:            []config.JobConfig{simpleJob("incremental")},
		Logger:          logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.saves != 1 {
		t.Fatalf("checkpoint saves = %d, want 1", store.saves)
	}
	cursor := store.state.Cursor("orders")
	if !cursor.Valid() || cursor.Tie != int64(5) {
		t.Errorf("advanced cursor = %+v", cursor)
	}
}

func TestCheckpointOnlyAdvancesAfterWriteSuccess(t *testing.T) {
	rdr := &fakeReader{
		rows: []model.SourceRow{{"id": int64(5), "code": "C001", "updated_at": time.Now()}},
		next: checkpoint.Cursor{Value: time.Now(), Tie: int64(5)},
	}
	wtr := &fakeWriter{err: errors.New("target down")}
	store := &memStore{}
	logger := &bufLogger{}

	eng, err := New(rdr, wtr, Options{
		CheckpointStore: store,
		Scope:           filter.New(filter.Config{AllowAll: true}),
		Jobs:            []config.JobConfig{simpleJob("incremental")},
		Logger:          logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.RunOnce(context.Background()); err == nil {
		t.Fatal("write failure must surface")
	}
	if store.saves != 0 {
		t.Error("checkpoint must NOT advance after a failed write")
	}
}

func TestEmptyBatchSkipsWrite(t *testing.T) {
	rdr := &fakeReader{}
	wtr := &fakeWriter{}
	logger := &bufLogger{}
	eng, err := New(rdr, wtr, Options{
		CheckpointStore: &memStore{},
		Scope:           filter.New(filter.Config{AllowAll: true}),
		Jobs:            []config.JobConfig{simpleJob("incremental")},
		Logger:          logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(wtr.records) != 0 {
		t.Error("empty batch must not write")
	}
}

func TestMappingFailureSurfaces(t *testing.T) {
	rdr := &fakeReader{rows: []model.SourceRow{{"id": int64(1)}}}
	wtr := &fakeWriter{}
	logger := &bufLogger{}
	eng, err := New(rdr, wtr, Options{
		CheckpointStore: &memStore{},
		Scope:           filter.New(filter.Config{AllowAll: true}),
		Jobs:            []config.JobConfig{simpleJob("snapshot")},
		Logger:          logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = eng.RunOnce(context.Background())
	if err == nil {
		t.Fatal("missing required field must fail the round")
	}
	if !strings.Contains(ErrorSummary(err), "job=\"orders\" stage=map") {
		t.Errorf("summary = %s", ErrorSummary(err))
	}
}

func TestErrorSummaryRedactsPasswords(t *testing.T) {
	err := newJobError("orders", "read", errors.New("connect to postgres://user:secret@db:5432/x failed"))
	summary := ErrorSummary(err)
	if strings.Contains(summary, "secret") {
		t.Errorf("summary leaks password: %s", summary)
	}
	if !strings.Contains(summary, "job=\"orders\" stage=read") {
		t.Errorf("summary = %s", summary)
	}
}

func TestNewValidatesDependencies(t *testing.T) {
	logger := &bufLogger{}
	rdr := &fakeReader{}
	wtr := &fakeWriter{}
	store := &memStore{}
	if _, err := New(nil, wtr, Options{CheckpointStore: store, Jobs: []config.JobConfig{simpleJob("snapshot")}, Logger: logger}); err == nil {
		t.Error("nil reader must fail")
	}
	if _, err := New(rdr, nil, Options{CheckpointStore: store, Jobs: []config.JobConfig{simpleJob("snapshot")}, Logger: logger}); err == nil {
		t.Error("nil writer must fail")
	}
	if _, err := New(rdr, wtr, Options{Jobs: []config.JobConfig{simpleJob("snapshot")}, Logger: logger}); err == nil {
		t.Error("nil checkpoint store must fail")
	}
	if _, err := New(rdr, wtr, Options{CheckpointStore: store, Logger: logger}); err == nil {
		t.Error("no jobs must fail")
	}
	if _, err := New(rdr, wtr, Options{CheckpointStore: store, Jobs: []config.JobConfig{simpleJob("snapshot")}}); err == nil {
		t.Error("nil logger must fail")
	}
}
