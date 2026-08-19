// Package logging provides structured event logging.
//
// Contract:
//   - fixed colors for event tags (job start/read/complete, sync failed);
//     fields (name=, rows=) stay plain text so logs remain grep-friendly;
//   - color is controlled by SYNCLET_LOG_COLOR (auto/always/never), with
//     NO_COLOR as an override;
//   - never logs DSNs, SQL parameters, checkpoint values or business
//     payloads.
package logging
