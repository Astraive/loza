package query

import (
	"database/sql"
	"fmt"
	"strings"

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
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Disable external access to block read_csv, read_json, etc.
	if _, err := db.Exec("SET enable_external_access=false"); err != nil {
		return fmt.Errorf("set safety guard: %w", err)
	}

	rows, err := db.Query(sqlQuery)
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
