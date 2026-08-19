// Package redact masks sensitive information in logs and errors.
//
// Contract:
//   - masks password/token/secret key-value pairs;
//   - masks URL userinfo (scheme://user:***@host) and MySQL userinfo
//     (user:***@tcp(...)) credentials.
//
// Not implemented yet: skeleton stage.
package redact
