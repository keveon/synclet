// Package checkpoint persists sync cursors for incremental jobs.
//
// Contract:
//   - cursor state is stored as a composite (cursor, tie_breaker) key so
//     rows sharing the same cursor value can never be skipped;
//   - the checkpoint advances only after the target write succeeds;
//   - the file backend writes atomically (temp file + rename).
//
// Not implemented yet: skeleton stage.
package checkpoint
