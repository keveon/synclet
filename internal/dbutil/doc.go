// Package dbutil provides database identifier validation and SQL building
// helpers.
//
// Contract:
//   - validates table, alias and column identifiers, refusing arbitrary SQL;
//   - builds parameterized query fragments (placeholders, ORDER BY, ...).
//
// Not implemented yet: skeleton stage.
package dbutil
