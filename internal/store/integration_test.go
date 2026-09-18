package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestMariaDBIntegration(t *testing.T) {
	if os.Getenv("DB0_INTEGRATION") == "" {
		t.Skip("set DB0_INTEGRATION=1 and DB0_TEST_* to run against MySQL/MariaDB")
	}
	port, err := strconv.Atoi(envOr("DB0_TEST_PORT", "3306"))
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	t.Cleanup(s.CloseAll)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := s.Connect(ctx, ConnectionRequest{
		Host: envOr("DB0_TEST_HOST", "127.0.0.1"), Port: port,
		User: os.Getenv("DB0_TEST_USER"), Password: os.Getenv("DB0_TEST_PASSWORD"),
		Database: os.Getenv("DB0_TEST_DATABASE"), TLS: TLSRequest{Mode: envOr("DB0_TEST_TLS", "disabled")},
	})
	if err != nil {
		t.Fatal(err)
	}

	schemas, err := s.Schemas(ctx, c.ID)
	if err != nil || !contains(schemas, c.Database) {
		t.Fatalf("Schemas() = %v, %v; expected %q", schemas, err, c.Database)
	}
	tables, err := s.Tables(ctx, c.ID, c.Database)
	if err != nil || len(tables) < 2 {
		t.Fatalf("Tables() = %v, %v", tables, err)
	}
	columns, err := s.Columns(ctx, c.ID, c.Database, "customers")
	if err != nil || len(columns) < 6 {
		t.Fatalf("Columns() count = %d, error = %v", len(columns), err)
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

	var limited []map[string]any
	err = s.StreamQuery(ctx, c.ID, QueryRequest{
		SQL: "SELECT a.COLUMN_NAME FROM information_schema.COLUMNS a JOIN information_schema.COLUMNS b LIMIT 100", MaxRows: 10,
	}, func(event any) error {
		limited = append(limited, event.(map[string]any))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := limited[len(limited)-1]
	if terminal["rowCount"] != 10 || terminal["truncated"] != true {
		t.Fatalf("bounded result terminal event = %#v", terminal)
	}

	if err := ValidateReadOnlySQL("DELETE FROM customers"); err == nil {
		t.Fatal("write SQL passed the application policy")
	}
	if _, err := c.db.ExecContext(ctx, "UPDATE customers SET name = 'changed' WHERE id = 1"); err == nil {
		t.Fatal("read-only database user unexpectedly performed an update")
	}

	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.StreamQuery(context.Background(), c.ID, QueryRequest{SQL: "SELECT SLEEP(30) AS wait_value"}, func(event any) error {
			value := event.(map[string]any)
			if value["type"] == "started" {
				started <- value["queryId"].(string)
			}
			return nil
		})
	}()
	queryID := <-started
	if !s.Cancel(queryID) {
		t.Fatal("Cancel() did not find the active query")
	}
	select {
	case err := <-done:
		if err == nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			// The driver wraps cancellation as a connection error on some versions.
			t.Logf("cancellation returned driver error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled query did not return within 5 seconds")
	}

	var followup []map[string]any
	if err := s.StreamQuery(ctx, c.ID, QueryRequest{SQL: "SELECT 1 AS ok"}, func(event any) error {
		followup = append(followup, event.(map[string]any))
		return nil
	}); err != nil {
		t.Fatalf("follow-up query after cancellation failed: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
