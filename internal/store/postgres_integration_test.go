package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPostgresIntegration(t *testing.T) {
	if os.Getenv("ROWLIGHT_POSTGRES_INTEGRATION") == "" {
		t.Skip("set ROWLIGHT_POSTGRES_INTEGRATION=1 and ROWLIGHT_POSTGRES_* to run against PostgreSQL")
	}
	port, err := strconv.Atoi(envOr("ROWLIGHT_POSTGRES_PORT", "5433"))
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	t.Cleanup(s.CloseAll)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := ConnectionRequest{
		Engine: EnginePostgres,
		Host:   envOr("ROWLIGHT_POSTGRES_HOST", "127.0.0.1"), Port: port,
		User: envOr("ROWLIGHT_POSTGRES_USER", "rowlight"), Password: envOr("ROWLIGHT_POSTGRES_PASSWORD", "rowlight"),
		Database: envOr("ROWLIGHT_POSTGRES_DATABASE", "rowlight_test"), TLS: TLSRequest{Mode: "disabled"},
	}
	c, err := s.Connect(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []string{
		`DROP VIEW IF EXISTS active_customers`,
		`DROP TABLE IF EXISTS customers`,
		`CREATE TABLE customers (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT,
			credit_limit NUMERIC(12,2) NOT NULL DEFAULT 0,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO customers (name, email, credit_limit, active) VALUES
			('Ada Lovelace', 'ada@example.test', 1250.50, TRUE),
			('Grace Hopper', NULL, 500.00, FALSE)`,
		`CREATE VIEW active_customers AS SELECT id, name, email FROM customers WHERE active`,
	}
	for _, statement := range fixtures {
		if _, err := c.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create PostgreSQL fixtures: %v", err)
		}
	}

	health, err := s.Health(ctx, c.ID)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("Health() = %+v, %v", health, err)
	}
	if _, err := s.Reconnect(ctx, c.ID); err != nil {
		t.Fatalf("Reconnect() = %v", err)
	}

	info, err := s.Info(c.ID)
	if err != nil || info.Engine != EnginePostgres || info.Database != c.Database || info.ServerVersion == "" {
		t.Fatalf("Info() = %+v, %v", info, err)
	}
	schemas, err := s.Schemas(ctx, c.ID)
	if err != nil || !contains(schemas, "public") {
		t.Fatalf("Schemas() = %v, %v; expected public", schemas, err)
	}
	tables, err := s.Tables(ctx, c.ID, "public")
	if err != nil || !containsTable(tables, "customers", "table") || !containsTable(tables, "active_customers", "view") {
		t.Fatalf("Tables() = %v, %v", tables, err)
	}
	columns, err := s.Columns(ctx, c.ID, "public", "customers")
	if err != nil || len(columns) != 6 || columns[0].Key != "PRI" || columns[0].Extra != "" {
		t.Fatalf("Columns() = %+v, %v", columns, err)
	}

	browsed, err := s.Browse(ctx, c.ID, BrowseRequest{
		Schema: "public", Table: "customers", PageSize: 1,
		Sort: &BrowseSort{Column: "id", Direction: "asc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(browsed.Columns) != 6 || len(browsed.Rows) != 1 || !browsed.HasMore {
		t.Fatalf("Browse() = %+v", browsed)
	}
	filtered, err := s.Browse(ctx, c.ID, BrowseRequest{
		Schema: "public", Table: "customers", PageSize: 10,
		Sort: &BrowseSort{Column: "id", Direction: "asc"},
		Filters: []BrowseFilter{
			{Column: "name", Operator: "contains", Value: stringPointer("Love")},
			{Column: "email", Operator: "startsWith", Value: stringPointer("ada@")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Rows) != 1 || filtered.HasMore {
		t.Fatalf("filtered Browse() = %+v", filtered)
	}

	var events []map[string]any
	err = s.StreamQuery(ctx, c.ID, QueryRequest{SQL: "SELECT id, credit_limit, email, created_at FROM customers ORDER BY name"}, func(event any) error {
		events = append(events, event.(map[string]any))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 || events[len(events)-1]["type"] != "complete" {
		t.Fatalf("unexpected query events: %#v", events)
	}
	var binaryEvents []map[string]any
	err = s.StreamQuery(ctx, c.ID, QueryRequest{SQL: "SELECT decode('68656c6c6f', 'hex') AS payload"}, func(event any) error {
		binaryEvents = append(binaryEvents, event.(map[string]any))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binaryRows, ok := binaryEvents[2]["rows"].([][]any)
	if !ok || len(binaryRows) != 1 {
		t.Fatalf("unexpected binary rows: %#v", binaryEvents)
	}
	encoded, ok := binaryRows[0][0].(map[string]any)
	if !ok || encoded["encoding"] != "base64" || encoded["data"] != "aGVsbG8=" {
		t.Fatalf("BYTEA transport = %#v", binaryRows[0][0])
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE customers SET name = 'changed' WHERE id = 1"); err == nil {
		t.Fatal("PostgreSQL read-only transaction unexpectedly performed an update")
	}
	_ = tx.Rollback()

	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.StreamQuery(context.Background(), c.ID, QueryRequest{SQL: "SELECT pg_sleep(30)"}, func(event any) error {
			value := event.(map[string]any)
			if value["type"] == "started" {
				started <- value["queryId"].(string)
			}
			return nil
		})
	}()
	queryID := <-started
	if !s.Cancel(queryID) {
		t.Fatal("Cancel() did not find the active PostgreSQL query")
	}
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Logf("PostgreSQL cancellation returned driver error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled PostgreSQL query did not return within 5 seconds")
	}
	var followup []map[string]any
	if err := s.StreamQuery(ctx, c.ID, QueryRequest{SQL: "SELECT 1 AS ok"}, func(event any) error {
		followup = append(followup, event.(map[string]any))
		return nil
	}); err != nil {
		t.Fatalf("follow-up query after PostgreSQL cancellation failed: %v", err)
	}
	exerciseConnectionLifecycle(t, s, req)
}

func containsTable(tables []Table, name, tableType string) bool {
	for _, table := range tables {
		if table.Name == name && table.Type == tableType {
			return true
		}
	}
	return false
}
