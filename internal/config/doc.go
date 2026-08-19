// Package config loads and validates the synclet YAML configuration.
//
// Contract:
//   - defines connections / checkpoint / sync / scope / jobs;
//   - credentials may only reference environment variable names (dsn_env),
//     never literal DSNs;
//   - validates the reader/mapping/writer contract of every job and fails
//     closed: an empty scope without an explicit allow_all is a config error.
package config
