// Package monitorsdk opens a remote-initialized SQLite buffer and records errors.
// Collection, HTTP delivery, retries, and housekeeping belong to monitor-remote.
package monitorsdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ConnectDB opens an existing buffer without creating tables or running migrations.
// The remote agent (or its initialization command) must prepare it first.
func ConnectDB(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("SQLite path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "rw")
	q.Set("_busy_timeout", "5000")
	q.Set("_foreign_keys", "on")
	q.Set("_synchronous", "FULL")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open prepared monitor database: %w", err)
	}
	for _, table := range []struct{ name, column string }{{"error_logs", "id"}, {"delivery_status", "record_id"}} {
		if err := requireIntegerColumn(ctx, db, table.name, table.column); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func requireIntegerColumn(ctx context.Context, db *sql.DB, table, column string) error {
	// Table names are fixed application constants, not caller input.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column && strings.EqualFold(kind, "INTEGER") && (table != "error_logs" || primaryKey == 1) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return fmt.Errorf("monitor database is not initialized with the current remote schema (%s.%s must be INTEGER)", table, column)
}

// ErrorDetails supplies optional diagnostics and the original event timestamp.
type ErrorDetails struct {
	StackTrace *string
	Metadata   json.RawMessage
	CreatedAt  time.Time
}

// LogError atomically writes the error and its pending-delivery entry. It does
// not contact remote/central, hold credentials, or start background workers.
func LogError(ctx context.Context, db *sql.DB, app, environment, level, message string, details ...ErrorDetails) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database is required")
	}
	if len(details) > 1 {
		return 0, fmt.Errorf("supply at most one ErrorDetails value")
	}
	app, environment = strings.TrimSpace(app), strings.TrimSpace(environment)
	level = strings.ToUpper(strings.TrimSpace(level))
	if app == "" || environment == "" || strings.TrimSpace(message) == "" {
		return 0, fmt.Errorf("application, environment, and message are required")
	}
	switch level {
	case "INFO", "WARN", "ERROR", "CRITICAL", "PANIC":
	default:
		return 0, fmt.Errorf("invalid log level")
	}
	var options ErrorDetails
	if len(details) == 1 {
		options = details[0]
	}
	if len(options.Metadata) == 0 {
		options.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(options.Metadata) {
		return 0, fmt.Errorf("metadata must be valid JSON")
	}
	now := time.Now().UTC()
	if options.CreatedAt.IsZero() {
		options.CreatedAt = now
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO error_logs(app_name,environment,level,message,stack_trace,metadata,created_at) VALUES(?,?,?,?,?,?,?)`, app, environment, level, message, options.StackTrace, string(options.Metadata), options.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_status(record_type,record_id,queued_at) VALUES('error_log',?,?)`, id, now.Format(time.RFC3339Nano)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
