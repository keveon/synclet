// Package engine is the sync engine: it orchestrates the
// reader -> mapping -> writer pipeline.
//
// Contract:
//   - drives one sync round per job: read a batch, map fields, upsert;
//   - snapshot jobs pull fully and upsert each round; incremental jobs
//     advance by composite cursor and persist the checkpoint only after a
//     successful write;
//   - loop mode schedules rounds by poll_interval and handles signals;
//   - emits a structured ErrorSummary (job + stage) without leaking
//     sensitive detail.
package engine
