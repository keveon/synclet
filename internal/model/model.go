// Package model defines core types shared across packages.
package model

// SourceRow is one row read from the source database, keyed by result
// column name.
type SourceRow map[string]any

// Record is the neutral row shape flowing through reader -> mapping ->
// writer. Fields contains target-column values after mapping.
type Record struct {
	Fields map[string]any
}
