// Package dbmanager provides a small connection registry for SQL-backed services.
//
// It caches database handles by key, applies driver-specific pool defaults, and
// supports callback-based access for library callers that want to keep ownership
// boundaries explicit.
package dbmanager
