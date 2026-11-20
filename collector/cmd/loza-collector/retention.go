package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *collectorState) initRetention() {
	if !s.config().retentionEnabled {
		return
	}
	if s.config().retentionDays <= 0 && s.config().retentionMaxSize <= 0 {
		return
	}
	s.retentionStop = make(chan struct{})
	go s.retentionWorker()
}

func (s *collectorState) retentionWorker() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.executeRetention(); err != nil {
				logJSON("error", "retention_execution_failed", map[string]any{"error": err.Error()})
			}
		case <-s.retentionStop:
			return
		}
	}
}

func (s *collectorState) executeRetention() error {
	if s.queryDB == nil {
		return fmt.Errorf("query db not available for retention")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var retentionErrs []error
	if s.config().retentionDays > 0 {
		cutoffTime := time.Now().UTC().AddDate(0, 0, -s.config().retentionDays)
		if err := s.deleteByAge(ctx, s.queryDB, cutoffTime); err != nil {
			logJSON("warn", "retention_age_delete_failed", map[string]any{"error": err.Error()})
			retentionErrs = append(retentionErrs, fmt.Errorf("delete events by age: %w", err))
		}
	}

	if s.config().retentionMaxSize > 0 {
		if err := s.deleteBySize(ctx, s.queryDB); err != nil {
			logJSON("warn", "retention_size_delete_failed", map[string]any{"error": err.Error()})
			retentionErrs = append(retentionErrs, fmt.Errorf("delete events by size: %w", err))
		}
	}

	logJSON("info", "retention_executed", map[string]any{
		"retention_days": s.config().retentionDays,
		"retention_size": s.config().retentionMaxSize,
	})
	return errors.Join(retentionErrs...)
}

func (s *collectorState) deleteByAge(ctx context.Context, db *sql.DB, cutoffTime time.Time) error {
	tableIdent, err := quoteSQLIdent(s.config().duckDBTable)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		DELETE FROM %s
		WHERE timestamp IS NOT NULL AND timestamp < $1
	`, tableIdent)

	result, err := db.ExecContext(ctx, query, cutoffTime.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	logJSON("info", "retention_age_deleted", map[string]any{
		"rows_deleted": rowsAffected,
		"cutoff_time":  cutoffTime.Format(time.RFC3339),
	})
	return nil
}

func (s *collectorState) deleteBySize(ctx context.Context, db *sql.DB) error {
	tableIdent, err := quoteSQLIdent(s.config().duckDBTable)
	if err != nil {
		return err
	}
	// SUM over an empty table returns a single NULL row, which cannot be scanned
	// into int64. A nullable target leaves Int64 at zero in that case, which the
	// limit check below treats as "under the limit".
	var currentSize sql.NullInt64
	err = db.QueryRowContext(ctx, "SELECT SUM(octet_length(raw)) FROM "+tableIdent).Scan(&currentSize)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if currentSize.Int64 <= s.config().retentionMaxSize {
		return nil
	}

	excessSize := currentSize.Int64 - s.config().retentionMaxSize
	query := fmt.Sprintf(`
		DELETE FROM %s
		WHERE event_id IN (
			SELECT event_id FROM %s
			ORDER BY timestamp ASC
			LIMIT (SELECT COUNT(*) FROM %s WHERE octet_length(raw) > 0 LIMIT CEIL($1 / (SELECT AVG(octet_length(raw)) FROM %s)))
		)
	`, tableIdent, tableIdent, tableIdent, tableIdent)

	result, err := db.ExecContext(ctx, query, excessSize)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	logJSON("info", "retention_size_deleted", map[string]any{
		"rows_deleted": rowsAffected,
		"excess_size":  excessSize,
	})
	return nil
}
