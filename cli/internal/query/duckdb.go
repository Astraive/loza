package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/astraive/loza/cli/internal/query/safety"
	_ "github.com/marcboeker/go-duckdb"
)

// rowScanner is the subset of *sql.Rows used by printRows. Keeping the
// iteration behind this small interface lets the rows.Err() handling be
// exercised with a fake that fails mid-stream.
type rowScanner interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func RunDuckDB(dbPath, sqlQuery string) error {
	// DuckDB's enable_external_access guard is connection-scoped and can be
	// re-enabled from SQL itself (e.g. "SET enable_external_access=true;
	// SELECT * FROM read_csv('/etc/passwd')"). Reject such input before the
	// driver ever sees it so the guard cannot be toggled from the query.
	if !safety.IsSafeReadOnlyQuery(sqlQuery) {
		return fmt.Errorf("rejected query: only a single read-only statement without session changes or external-access operations is allowed")
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Use a dedicated connection so the safety guard and the caller's query
	// execute on the same physical connection. On the pool directly, the SET
	// could apply to one connection while the query runs on another.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	// Disable external access to block read_csv, read_json, etc. The query is
	// additionally constrained by safety.IsSafeReadOnlyQuery above, so it
	// cannot re-enable access on this connection.
	if _, err := conn.ExecContext(ctx, "SET enable_external_access=false"); err != nil {
		return fmt.Errorf("set safety guard: %w", err)
	}

	rows, err := conn.QueryContext(ctx, sqlQuery)
	if err != nil {
		return fmt.Errorf("execute query: %w", err)
	}
	defer rows.Close()

	return printRows(rows)
}

func printRows(rows rowScanner) error {
	columns, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("get columns: %w", err)
	}
	fmt.Println(strings.Join(columns, "\t"))

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		for i, value := range values {
			if i > 0 {
				fmt.Print("\t")
			}
			fmt.Print(fmt.Sprint(value))
		}
		fmt.Println()
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	return nil
}
