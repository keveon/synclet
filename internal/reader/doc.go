// Package reader polls the source database read-only.
//
// Contract:
//   - PostgreSQL reader: builds parameterized queries from
//     table + alias + columns + joins + filters; every identifier is
//     validated; arbitrary SQL is rejected;
//   - JOINs are restricted: inner/left equi-joins; filter conditions
//     reference the primary alias only;
//   - incremental mode pages by composite (cursor, tie_breaker) keyset;
//     the predicate keeps a sargable cursor >= conjunct so indexes can
//     bound the scan at the cursor position;
//     JOINs apply after the fact batch is selected so LIMIT never truncates
//     expanded rows; the fact subquery projects only the selected base
//     columns, never alias.*.
package reader
