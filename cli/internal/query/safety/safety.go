// Package safety provides dependency-free guards for SQL accepted by the CLI's
// local DuckDB query path. It deliberately does not import the DuckDB driver so
// the security logic can be unit-tested without the native (CGO) dependency.
package safety

import "strings"

// IsSafeReadOnlyQuery reports whether q is safe to run under DuckDB's
// "enable_external_access=false" guard.
//
// That guard is connection-scoped and can be flipped by SQL itself, e.g.
//
//	SET enable_external_access=true; SELECT * FROM read_csv('/etc/passwd')
//
// so it must not be the only line of defense. We therefore require exactly one
// read-only statement that neither changes session settings nor reaches files,
// the network, attached databases or extension code.
func IsSafeReadOnlyQuery(q string) bool {
	lower := strings.ToLower(strings.TrimSpace(q))
	if lower == "" {
		return false
	}

	// A single statement only. Chained statements can re-enable external access
	// or run side effects after the safety guard has been applied, and are the
	// exact shape of the MASS-0061 bypass.
	if strings.Contains(lower, ";") ||
		strings.Contains(lower, "--") ||
		strings.Contains(lower, "/*") ||
		strings.Contains(lower, "*/") {
		return false
	}

	// Only read-only statement forms are permitted. A bare "SET ..." (or any
	// DDL/DML statement) does not match and is rejected.
	readOnly := false
	for _, prefix := range [...]string{"select", "with", "show", "describe", "explain"} {
		if strings.HasPrefix(lower, prefix) {
			readOnly = true
			break
		}
	}
	if !readOnly {
		return false
	}

	// Reject anything that can change session state, read local files, reach
	// the network, attach databases, or execute arbitrary/extension code.
	for _, blocked := range [...]string{
		"set ", "set\t", "reset ",
		"read_", "scan(", "glob(", "iceberg_scan", "delta_scan",
		"httpfs", "http://", "https://", "file:", "s3://",
		"install", "load ", "load\t", "attach", "copy ", "export",
		"call ", "prepare", "execute",
		"create ", "drop ", "alter ", "truncate",
		"insert ", "update ", "delete ", "merge ",
		"grant ", "revoke",
	} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}

	return true
}
