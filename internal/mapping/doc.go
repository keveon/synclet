// Package mapping turns source rows into target write records.
//
// Contract:
//   - field mapping types: column / literal / json_path / json_object /
//     selector, with required (fail on missing, no default) and default;
//   - selectors try paths in order and take the first numeric value;
//   - ordered transforms (negative_to_zero, require_column_in, add_column);
//     decimal arithmetic goes through decimal — floats are rejected as
//     inexact.
//
// Not implemented yet: skeleton stage.
package mapping
