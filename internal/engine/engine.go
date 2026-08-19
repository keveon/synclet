package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keveon/synclet/internal/checkpoint"
	"github.com/keveon/synclet/internal/config"
	"github.com/keveon/synclet/internal/filter"
	"github.com/keveon/synclet/internal/mapping"
	"github.com/keveon/synclet/internal/model"
	"github.com/keveon/synclet/internal/reader"
	"github.com/keveon/synclet/internal/redact"
	"github.com/keveon/synclet/internal/writer"
)

// Reader is the engine's view of a source poller.
type Reader interface {
	Read(ctx context.Context, job config.JobConfig, scope filter.Filter, cursor checkpoint.Cursor, defaultBatch int) (reader.ReadResult, error)
	Close()
}

// Writer is the engine's view of a target upserter.
type Writer interface {
	Write(ctx context.Context, job config.JobConfig, records []model.Record) (writer.WriteResult, error)
	Close() error
}

// Logger is the engine's view of an event sink.
type Logger interface {
	Printf(format string, args ...any)
}

// Options configures the engine.
type Options struct {
	CheckpointStore checkpoint.Store
	Scope           filter.Filter
	PollInterval    time.Duration
	BatchSize       int
	Jobs            []config.JobConfig
	Logger          Logger
}

// Engine orchestrates reader -> mapping -> writer for every job.
type Engine struct {
	reader Reader
	writer Writer
	opts   Options
	logger Logger
}

// New validates dependencies and constructs the engine.
func New(rdr Reader, wtr Writer, opts Options) (*Engine, error) {
	if rdr == nil {
		return nil, fmt.Errorf("reader is required")
	}
	if wtr == nil {
		return nil, fmt.Errorf("writer is required")
	}
	if opts.CheckpointStore == nil {
		return nil, fmt.Errorf("checkpoint store is required")
	}
	if len(opts.Jobs) == 0 {
		return nil, fmt.Errorf("jobs are required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	return &Engine{reader: rdr, writer: wtr, opts: opts, logger: logger}, nil
}

// jobError carries the job/stage context of a failure for ErrorSummary.
type jobError struct {
	job   string
	stage string
	inner error
}

func (e *jobError) Error() string {
	return fmt.Sprintf("job %s %s: %v", e.job, e.stage, e.inner)
}

func (e *jobError) Unwrap() error { return e.inner }

func newJobError(job, stage string, err error) error {
	return &jobError{job: job, stage: stage, inner: err}
}

// ErrorSummary renders a redacted, grep-friendly one-line summary of an
// engine error.
func ErrorSummary(err error) string {
	var operational *jobError
	if errors.As(err, &operational) {
		return fmt.Sprintf("job=%q stage=%s err=%s", operational.job, operational.stage, redact.Error(operational.inner))
	}
	return redact.Error(err)
}

// RunOnce runs one pass over all jobs.
func (e *Engine) RunOnce(ctx context.Context) error {
	state, err := e.opts.CheckpointStore.Load()
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}

	for _, job := range e.opts.Jobs {
		if err := e.runJob(ctx, job, &state); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) runJob(ctx context.Context, job config.JobConfig, state *checkpoint.State) error {
	jobName := strings.TrimSpace(job.Name)
	var cursor checkpoint.Cursor
	if job.CanonicalMode() == "incremental" {
		cursor = state.Cursor(jobName)
	}
	e.logger.Printf("job start name=%q mode=%s checkpoint_valid=%t", jobName, job.CanonicalMode(), cursor.Valid())

	readResult, err := e.reader.Read(ctx, job, e.opts.Scope, cursor, e.opts.BatchSize)
	if err != nil {
		return newJobError(jobName, "read", err)
	}
	e.logger.Printf("job read name=%q rows=%d", jobName, len(readResult.Rows))
	if len(readResult.Rows) == 0 {
		e.logger.Printf("job complete name=%q read_rows=0 attempted_rows=0 inserted=0 updated=0 unchanged=0 checkpoint_advanced=false", jobName)
		return nil
	}

	mapper, err := mapping.NewMapper(job.Mapping)
	if err != nil {
		return newJobError(jobName, "map", err)
	}
	records := make([]model.Record, 0, len(readResult.Rows))
	for i, row := range readResult.Rows {
		record, err := mapper.Map(row)
		if err != nil {
			return newJobError(jobName, "map", fmt.Errorf("map row %d: %w", i, err))
		}
		records = append(records, record)
	}

	writeResult, err := e.writer.Write(ctx, job, records)
	if err != nil {
		return newJobError(jobName, "write", err)
	}

	checkpointAdvanced := false
	if job.CanonicalMode() == "incremental" && readResult.NextCheckpoint.Valid() {
		state.SetCursor(jobName, readResult.NextCheckpoint)
		if err := e.opts.CheckpointStore.Save(*state); err != nil {
			return newJobError(jobName, "checkpoint", err)
		}
		checkpointAdvanced = true
	}
	e.logger.Printf("job complete name=%q read_rows=%d attempted_rows=%d inserted=%d updated=%d unchanged=%d checkpoint_advanced=%t",
		jobName, len(readResult.Rows), writeResult.AttemptedRows, writeResult.Inserted, writeResult.Updated, writeResult.Unchanged, checkpointAdvanced)
	return nil
}

// Run loops RunOnce by poll_interval until the context is cancelled.
// Round errors are logged (redacted) and the loop continues; shutdown
// errors (context cancelled) propagate.
func (e *Engine) Run(ctx context.Context) error {
	for {
		if err := e.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			e.logger.Printf("sync failed: %s", ErrorSummary(err))
		}

		timer := time.NewTimer(e.opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
