package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var registerRecoveryDriver sync.Once
var recoveryPingCount atomic.Int64

type recoveryDriver struct{}
type recoveryConn struct{}
type recoveryTx struct{}

func (recoveryDriver) Open(string) (driver.Conn, error)  { return recoveryConn{}, nil }
func (recoveryConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (recoveryConn) Close() error                        { return nil }
func (recoveryConn) Begin() (driver.Tx, error)           { return recoveryTx{}, nil }
func (recoveryConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (recoveryConn) Ping(context.Context) error { recoveryPingCount.Add(1); return nil }
func (recoveryTx) Commit() error                { return nil }
func (recoveryTx) Rollback() error              { return nil }

type temporaryNetworkError struct{}

func (temporaryNetworkError) Error() string   { return "temporary network failure" }
func (temporaryNetworkError) Timeout() bool   { return false }
func (temporaryNetworkError) Temporary() bool { return true }

func recoveryDB(t *testing.T) *sql.DB {
	t.Helper()
	registerRecoveryDriver.Do(func() { sql.Register("rowlight-recovery-test", recoveryDriver{}) })
	db, err := sql.Open("rowlight-recovery-test", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRetrySafeReadRetriesConnectionFailureExactlyOnce(t *testing.T) {
	recoveryPingCount.Store(0)
	c := &Connection{db: recoveryDB(t)}
	calls := 0
	result, err := retrySafeRead(context.Background(), c, func(*sql.DB) (string, error) {
		calls++
		if calls == 1 {
			return "", temporaryNetworkError{}
		}
		return "recovered", nil
	})
	if err != nil || result != "recovered" {
		t.Fatalf("result = %q, error = %v", result, err)
	}
	if calls != 2 {
		t.Fatalf("operation calls = %d, want 2", calls)
	}
	if recoveryPingCount.Load() != 1 {
		t.Fatalf("ping calls = %d, want 1", recoveryPingCount.Load())
	}
}

func TestRetrySafeReadDoesNotReplayOrdinaryErrors(t *testing.T) {
	recoveryPingCount.Store(0)
	c := &Connection{db: recoveryDB(t)}
	calls := 0
	want := errors.New("invalid schema")
	_, err := retrySafeRead(context.Background(), c, func(*sql.DB) (string, error) {
		calls++
		return "", want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("operation calls = %d, want 1", calls)
	}
	if recoveryPingCount.Load() != 0 {
		t.Fatalf("ping calls = %d, want 0", recoveryPingCount.Load())
	}
}

func TestConnectionFailureClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"bad connection", driver.ErrBadConn, true},
		{"EOF", io.EOF, true},
		{"network", temporaryNetworkError{}, true},
		{"wrapped reset", errors.New("read: connection reset by peer"), true},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"query", errors.New("unknown column"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConnectionFailure(tt.err); got != tt.want {
				t.Fatalf("IsConnectionFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
	var _ net.Error = temporaryNetworkError{}
}

func TestDisconnectCancelsActiveQueryAndReleasesSlot(t *testing.T) {
	s := New()
	db := recoveryDB(t)
	connectionID := "connection-under-test"
	s.connections[connectionID] = &Connection{ID: connectionID, Engine: EngineMySQL, db: db}

	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.StreamQuery(context.Background(), connectionID, QueryRequest{SQL: "SELECT 1"}, func(event any) error {
			value := event.(map[string]any)
			if value["type"] == "started" {
				started <- struct{}{}
			}
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	if !s.Close(connectionID) {
		t.Fatal("Close() did not find connection")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("disconnect did not stop active query")
		}
	case <-time.After(time.Second):
		t.Fatal("active query did not stop after disconnect")
	}
	if got := len(s.querySlots); got != 0 {
		t.Fatalf("occupied query slots = %d, want 0", got)
	}
	s.mu.RLock()
	active := len(s.queries)
	s.mu.RUnlock()
	if active != 0 {
		t.Fatalf("active query registry entries = %d, want 0", active)
	}
}

func TestCloseAllCancelsConcurrentQueriesAndReleasesSlots(t *testing.T) {
	s := New()
	for index := 0; index < 2; index++ {
		id := string(rune('a' + index))
		s.connections[id] = &Connection{ID: id, Engine: EngineMySQL, db: recoveryDB(t)}
	}

	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	for _, connectionID := range []string{"a", "b"} {
		go func(id string) {
			done <- s.StreamQuery(context.Background(), id, QueryRequest{SQL: "SELECT 1"}, func(event any) error {
				if event.(map[string]any)["type"] == "started" {
					started <- struct{}{}
				}
				return nil
			})
		}(connectionID)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("concurrent query did not start")
		}
	}
	s.CloseAll()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("CloseAll did not stop an active query")
			}
		case <-time.After(time.Second):
			t.Fatal("active query did not stop after CloseAll")
		}
	}
	if got := len(s.querySlots); got != 0 {
		t.Fatalf("occupied query slots = %d, want 0", got)
	}
	s.mu.RLock()
	activeQueries, activeConnections := len(s.queries), len(s.connections)
	s.mu.RUnlock()
	if activeQueries != 0 || activeConnections != 0 {
		t.Fatalf("CloseAll leaked resources: queries=%d connections=%d", activeQueries, activeConnections)
	}
}
