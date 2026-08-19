// Package writer upserts mapped records into the target database.
//
// Contract:
//   - MySQL writer: idempotent ON DUPLICATE KEY UPDATE upserts;
//   - null_update_policy=keep_existing keeps existing target values when a
//     round maps to NULL;
//   - json_merge_patch_columns merge instead of overwriting;
//   - DATETIME values are written as local literals per writer timezone;
//   - distinguishes attempted/inserted/updated/unchanged in write stats.
package writer
