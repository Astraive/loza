package client

import (
	"net/url"
	"strings"
)

// buildURL constructs a URL from a base URL, a list of raw path segments, and
// optional query parameters. It is the single construction point for CLI
// request URLs so no call site has to concatenate untrusted input itself.
//
// Every path segment is escaped with url.PathEscape, which prevents an ID such
// as "../", "?", or "#" from terminating its segment or starting a query or
// fragment. Query keys and values are encoded with url.Values, which escapes
// "&", "=", "/", and spaces so a filter cannot inject additional parameters.
func buildURL(baseURL string, segments []string, query url.Values) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(baseURL, "/"))
	for _, segment := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(segment))
	}
	if len(query) > 0 {
		b.WriteByte('?')
		b.WriteString(query.Encode())
	}
	return b.String()
}

// valuesFromStringMap converts a map of string filters into url.Values so the
// keys are encoded just as carefully as the values.
func valuesFromStringMap(filters map[string]string) url.Values {
	values := make(url.Values, len(filters))
	for key, value := range filters {
		values.Set(key, value)
	}
	return values
}
