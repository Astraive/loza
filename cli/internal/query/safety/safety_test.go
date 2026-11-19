package safety

import "testing"

func TestIsSafeReadOnlyQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		// The exact MASS-0061 bypass: re-enable external access, then read a
		// local file in the same session.
		{
			name:  "re-enable external access and read local file",
			query: "SET enable_external_access=true; SELECT * FROM read_csv('/etc/passwd')",
			want:  false,
		},
		{
			name:  "bare set statement",
			query: "SET enable_external_access=true",
			want:  false,
		},
		{
			name:  "single read_csv statement",
			query: "SELECT * FROM read_csv('/etc/passwd')",
			want:  false,
		},
		{
			name:  "single read_json statement",
			query: "SELECT * FROM read_json('/etc/passwd')",
			want:  false,
		},
		{
			name:  "http fetch",
			query: "SELECT * FROM read_csv('https://example.com/data.csv')",
			want:  false,
		},
		{
			name:  "chained statements with dml",
			query: "SELECT 1; DROP TABLE events",
			want:  false,
		},
		{
			name:  "comment used to hide a statement",
			query: "SELECT 1 -- ; SET enable_external_access=true",
			want:  false,
		},
		{
			name:  "empty query",
			query: "   ",
			want:  false,
		},
		{
			name:  "dml statement",
			query: "DELETE FROM events WHERE id = 1",
			want:  false,
		},

		// Legitimate read-only queries must still be accepted.
		{
			name:  "simple select",
			query: "SELECT 1",
			want:  true,
		},
		{
			name:  "select from a table",
			query: "SELECT * FROM events WHERE level = 'error' ORDER BY ts DESC LIMIT 10",
			want:  true,
		},
		{
			name:  "cte",
			query: "WITH recent AS (SELECT * FROM events WHERE ts > 0) SELECT count(*) FROM recent",
			want:  true,
		},
		{
			name:  "show tables",
			query: "SHOW TABLES",
			want:  true,
		},
		{
			name:  "describe table",
			query: "DESCRIBE events",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSafeReadOnlyQuery(tt.query); got != tt.want {
				t.Fatalf("IsSafeReadOnlyQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
