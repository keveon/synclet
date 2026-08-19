// Package jsonpath implements restricted, safe dot-path evaluation.
//
// Contract:
//   - supports `$` and `$.a.b.c` paths, including numeric array indices
//     (`$.items.0.value`);
//   - map-key traversal only — no wildcards, filter expressions or bracket
//     syntax;
//   - used by mapping json_path fields and selectors.
package jsonpath
