package env

import (
	"os"
	"strconv"
)

// GetString returns the value of the named environment variable,
// or def if the variable is unset or empty.
func GetString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// GetBool returns the boolean value of the named env var. It returns def if
// the variable is unset, empty, or holds a value that strconv.ParseBool cannot
// parse (for example a typo such as "ture").
func GetBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// GetInt returns the integer value of the named env var. It returns def if
// the variable is unset, empty, or holds a value that strconv.Atoi cannot parse
// (for example a typo such as "4a2").
func GetInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
