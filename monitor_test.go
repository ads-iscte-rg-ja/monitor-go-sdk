package monitorsdk_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	monitorsdk "github.com/rmg-unicage/monitor-go-sdk"
)

// remoteSchema is the buffer schema owned by monitor-remote. The SDK writes into
// that schema, so the tests use the real file instead of a copy that could drift.
// CI clones monitor-remote next to this repository (sibling-repos).
var remoteSchema = filepath.Join("..", "monitor-remote", "database", "schema.sql")

// preparedBuffer creates a buffer the way monitor-remote does and returns its path
// plus a separate connection to inspect what the SDK wrote.
func preparedBuffer(t *testing.T) (path string, inspect *sql.DB) {
	t.Helper()
	schema, err := os.ReadFile(remoteSchema)
	if err != nil {
		t.Fatalf("read remote schema (monitor-remote must be cloned next to this repo): %v", err)
	}
	path = filepath.Join(t.TempDir(), "buffer.db")
	inspect, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inspect.Close() })
	if _, err := inspect.Exec(string(schema)); err != nil {
		t.Fatalf("apply remote schema: %v", err)
	}
	return path, inspect
}

func connect(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := monitorsdk.ConnectDB(path)
	if err != nil {
		t.Fatalf("ConnectDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestConnectDBRejectsUnpreparedBuffers(t *testing.T) {
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty.db")
	db, err := sql.Open("sqlite3", empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unrelated(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for _, test := range []struct {
		name, path, wantErr string
	}{
		{"blank path", "  ", "path is required"},
		// mode=rw: the SDK never creates the buffer; that is the remote's job.
		{"missing file", filepath.Join(dir, "missing.db"), "open prepared monitor database"},
		{"no remote schema", empty, "not initialized with the current remote schema"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := monitorsdk.ConnectDB(test.path)
			if err == nil {
				_ = db.Close()
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("want error containing %q, got %v", test.wantErr, err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.db")); !os.IsNotExist(err) {
		t.Fatalf("ConnectDB must not create the buffer file (stat err: %v)", err)
	}
}

func TestLogErrorRejectsInvalidInput(t *testing.T) {
	path, inspect := preparedBuffer(t)
	db := connect(t, path)
	ctx := context.Background()

	for _, test := range []struct {
		name                     string
		app, env, level, message string
		details                  []monitorsdk.ErrorDetails
	}{
		{"missing app", " ", "prod", "ERROR", "boom", nil},
		{"missing environment", "app", "", "ERROR", "boom", nil},
		{"missing message", "app", "prod", "ERROR", "  ", nil},
		{"unknown level", "app", "prod", "DEBUG", "boom", nil},
		{"invalid metadata", "app", "prod", "ERROR", "boom", []monitorsdk.ErrorDetails{{Metadata: json.RawMessage(`{`)}}},
		{"two details", "app", "prod", "ERROR", "boom", []monitorsdk.ErrorDetails{{}, {}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := monitorsdk.LogError(ctx, db, test.app, test.env, test.level, test.message, test.details...); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
	if _, err := monitorsdk.LogError(ctx, nil, "app", "prod", "ERROR", "boom"); err == nil {
		t.Fatal("nil database: want error, got nil")
	}

	var rows int
	if err := inspect.QueryRow("SELECT COUNT(*) FROM error_logs").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rejected input must not be written: rows=%d err=%v", rows, err)
	}
}

func TestLogErrorQueuesRecordForDelivery(t *testing.T) {
	path, inspect := preparedBuffer(t)
	db := connect(t, path)
	createdAt := time.Date(2026, 11, 19, 10, 30, 0, 0, time.FixedZone("WET", 0))

	id, err := monitorsdk.LogError(context.Background(), db, " app ", " prod ", " warn ", "disk almost full",
		monitorsdk.ErrorDetails{CreatedAt: createdAt})
	if err != nil {
		t.Fatalf("LogError: %v", err)
	}

	var app, env, level, metadata, storedCreatedAt string
	var pending int
	err = inspect.QueryRow(`SELECT e.app_name, e.environment, e.level, e.metadata, e.created_at,
		(SELECT COUNT(*) FROM delivery_status d WHERE d.record_type='error_log' AND d.record_id=e.id AND d.sent_at IS NULL)
		FROM error_logs e WHERE e.id=?`, id).Scan(&app, &env, &level, &metadata, &storedCreatedAt, &pending)
	if err != nil {
		t.Fatal(err)
	}
	if app != "app" || env != "prod" || level != "WARN" {
		t.Fatalf("input not normalized: app=%q env=%q level=%q", app, env, level)
	}
	if metadata != "{}" {
		t.Fatalf("missing metadata must default to {}, got %q", metadata)
	}
	if storedCreatedAt != createdAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("original event time not kept: got %q", storedCreatedAt)
	}
	if pending != 1 {
		t.Fatalf("record must be queued exactly once for delivery, got %d", pending)
	}
}

func TestLogErrorIsAtomic(t *testing.T) {
	path, inspect := preparedBuffer(t)
	db := connect(t, path)
	if _, err := inspect.Exec(`CREATE TRIGGER fail_queue BEFORE INSERT ON delivery_status
		BEGIN SELECT RAISE(ABORT, 'injected queue failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := monitorsdk.LogError(context.Background(), db, "app", "prod", "ERROR", "boom"); err == nil {
		t.Fatal("queue failure was ignored")
	}
	var rows int
	if err := inspect.QueryRow("SELECT COUNT(*) FROM error_logs").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("an error without a delivery entry would never be sent: rows=%d err=%v", rows, err)
	}
}
