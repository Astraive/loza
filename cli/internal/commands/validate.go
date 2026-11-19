package commands

import (
	"fmt"
	"strings"
)

// validatePathID rejects identifiers that could break out of a single URL
// path segment or inject a query/fragment when interpolated into a request
// path. The client also escapes path parameters; this check fails early with
// a clear message for input that is never a valid opaque ID.
func validatePathID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\?#") {
		return fmt.Errorf("invalid id %q: must not contain path or query delimiters", id)
	}
	return nil
}
