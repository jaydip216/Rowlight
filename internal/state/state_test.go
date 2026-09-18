package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileRoundTripAndUpdate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := store.SaveProfile(Profile{
		Name: "Local MariaDB", Engine: "mysql", Host: "127.0.0.1", Port: 3307,
		User: "reader", Database: "rowlight_test",
		TLS: TLSProfile{Mode: "custom", ServerName: "db.local", CAPEM: "ca", ClientCertPEM: "cert"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Fatalf("generated fields missing: %+v", saved)
	}

	created := saved.CreatedAt
	saved.Name = "Updated"
	saved.CreatedAt = time.Time{}
	updated, err := store.SaveProfile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAt != created {
		t.Fatalf("createdAt changed: got %v, want %v", updated.CreatedAt, created)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles := reopened.Profiles()
	if len(profiles) != 1 || profiles[0].Name != "Updated" || profiles[0].Engine != "mysql" {
		t.Fatalf("profiles = %+v", profiles)
	}
	profiles[0].Name = "mutated copy"
	if reopened.Profiles()[0].Name != "Updated" {
		t.Fatal("Profiles returned internal storage")
	}
	if !reopened.DeleteProfile(updated.ID) || reopened.DeleteProfile(updated.ID) {
		t.Fatal("unexpected DeleteProfile result")
	}
	final, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Profiles()) != 0 {
		t.Fatal("deleted profile was persisted")
	}
}

func TestStateNeverContainsConnectionSecrets(t *testing.T) {
	t.Parallel()
	profileType, err := json.Marshal(Profile{
		Name: "name", Host: "host", User: "user",
		TLS: TLSProfile{Mode: "mutual", ClientCertPEM: "public-cert"},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(profileType)
	if !strings.Contains(encoded, `"database":""`) {
		t.Fatalf("profile JSON omitted empty database: %s", encoded)
	}
	for _, forbidden := range []string{"password", "clientKey", "privateKey"} {
		if strings.Contains(strings.ToLower(encoded), strings.ToLower(forbidden)) {
			t.Fatalf("profile JSON contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestHistoryNewestFirstCappedAndClearable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxHistory+5; i++ {
		if err := store.AddHistory(HistoryEntry{SQL: "SELECT " + string(rune('A'+i%26)), ExecutedAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}

	history := store.History()
	if len(history) != maxHistory {
		t.Fatalf("history length = %d, want %d", len(history), maxHistory)
	}
	if !history[0].ExecutedAt.After(history[len(history)-1].ExecutedAt) {
		t.Fatal("history is not newest first")
	}
	if history[0].ID == "" {
		t.Fatal("history ID was not generated")
	}
	history[0].SQL = "mutated copy"
	if store.History()[0].SQL == "mutated copy" {
		t.Fatal("History returned internal storage")
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.History()) != maxHistory {
		t.Fatalf("reopened history length = %d", len(reopened.History()))
	}
	if err := reopened.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	if len(reopened.History()) != 0 {
		t.Fatal("history was not cleared")
	}
}

func TestOpenSortsHistoryNewestFirst(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	b, err := json.Marshal(diskState{History: []HistoryEntry{
		{ID: "old", SQL: "SELECT 1", ExecutedAt: older},
		{ID: "new", SQL: "SELECT 2", ExecutedAt: newer},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if history := store.History(); len(history) != 2 || history[0].ID != "new" {
		t.Fatalf("history = %+v", history)
	}
}

func TestLegacyProfileDefaultsToMySQL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	b := []byte(`{"profiles":[{"id":"legacy","name":"Old profile","host":"127.0.0.1","port":3306,"user":"reader","database":"","tls":{"mode":"disabled"}}],"history":[]}`)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.Profiles()
	if len(profiles) != 1 || profiles[0].Engine != "mysql" {
		t.Fatalf("legacy profiles = %+v", profiles)
	}
}

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	store := NewMemory()
	if _, err := store.SaveProfile(Profile{Name: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddHistory(HistoryEntry{SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if len(store.Profiles()) != 1 || len(store.History()) != 1 {
		t.Fatal("memory store did not retain state")
	}
	if store.Profiles()[0].Engine != "mysql" {
		t.Fatal("profile without engine did not default to mysql")
	}
}

func TestOpenRejectsInvalidState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted invalid JSON")
	}
}
