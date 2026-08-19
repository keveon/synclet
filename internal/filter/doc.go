// Package filter resolves value sources for job filters.
//
// Contract:
//   - resolves values_from references (e.g. scope.allowed_codes) into value
//     sets for reader `in` filters;
//   - fail-closed by default: without an explicit allow_all, syncing
//     everything is rejected.
package filter
