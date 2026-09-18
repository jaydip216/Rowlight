// Package state persists connection profiles and query history.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxHistory = 100

// TLSProfile contains the reusable, non-secret portion of a TLS configuration.
// Client private keys are deliberately never represented in persisted state.
type TLSProfile struct {
	Mode          string `json:"mode"`
	ServerName    string `json:"serverName,omitempty"`
	CAPEM         string `json:"caPem,omitempty"`
	ClientCertPEM string `json:"clientCertPem,omitempty"`
}

// Profile is a saved database connection without credentials or private keys.
type Profile struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	User      string     `json:"user"`
	Database  string     `json:"database,omitempty"`
	TLS       TLSProfile `json:"tls"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// HistoryEntry records one query execution. Entries are returned newest first.
type HistoryEntry struct {
	ID         string    `json:"id"`
	SQL        string    `json:"sql"`
	Database   string    `json:"database,omitempty"`
	Server     string    `json:"server,omitempty"`
	ExecutedAt time.Time `json:"executedAt"`
	ElapsedMS  int64     `json:"elapsedMs"`
	RowCount   int64     `json:"rowCount"`
	Truncated  bool      `json:"truncated"`
	Error      string    `json:"error,omitempty"`
}

type diskState struct {
	Profiles []Profile      `json:"profiles"`
	History  []HistoryEntry `json:"history"`
}

// Store is safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	path string
	data diskState
}

// Open loads a state file. A missing file starts with empty state and is created
// on the first mutation.
func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("state path is required")
	}

	s := &Store{path: path, data: emptyState()}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(b) == 0 {
		return nil, errors.New("read state: file is empty")
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	if s.data.Profiles == nil {
		s.data.Profiles = []Profile{}
	}
	if s.data.History == nil {
		s.data.History = []HistoryEntry{}
	}
	if len(s.data.History) > maxHistory {
		s.data.History = s.data.History[:maxHistory]
	}
	return s, nil
}

// NewMemory creates a non-persistent store.
func NewMemory() *Store {
	return &Store{data: emptyState()}
}

func emptyState() diskState {
	return diskState{Profiles: []Profile{}, History: []HistoryEntry{}}
}

// Profiles returns saved profiles in their stable insertion order.
func (s *Store) Profiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.data.Profiles)
}

// SaveProfile inserts or updates a profile. It returns the values actually
// stored, including generated identifiers and timestamps.
func (s *Store) SaveProfile(profile Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		profile.ID = newID()
	}

	index := -1
	for i := range s.data.Profiles {
		if s.data.Profiles[i].ID == profile.ID {
			index = i
			break
		}
	}
	if profile.CreatedAt.IsZero() {
		if index >= 0 {
			profile.CreatedAt = s.data.Profiles[index].CreatedAt
		} else {
			profile.CreatedAt = now
		}
	}
	profile.CreatedAt = profile.CreatedAt.UTC()
	profile.UpdatedAt = now

	previous := slices.Clone(s.data.Profiles)
	if index >= 0 {
		s.data.Profiles[index] = profile
	} else {
		s.data.Profiles = append(s.data.Profiles, profile)
	}
	if err := s.persistLocked(); err != nil {
		s.data.Profiles = previous
		return Profile{}, err
	}
	return profile, nil
}

// DeleteProfile removes a profile. It returns true only when the profile was
// found and the updated state was persisted successfully.
func (s *Store) DeleteProfile(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Profiles {
		if s.data.Profiles[i].ID != id {
			continue
		}
		previous := slices.Clone(s.data.Profiles)
		s.data.Profiles = append(s.data.Profiles[:i:i], s.data.Profiles[i+1:]...)
		if err := s.persistLocked(); err != nil {
			s.data.Profiles = previous
			return false
		}
		return true
	}
	return false
}

// History returns query history from newest to oldest.
func (s *Store) History() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.data.History)
}

// AddHistory adds a query to the front of history and enforces the history cap.
func (s *Store) AddHistory(entry HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(entry.ID) == "" {
		entry.ID = newID()
	}
	if entry.ExecutedAt.IsZero() {
		entry.ExecutedAt = time.Now().UTC()
	} else {
		entry.ExecutedAt = entry.ExecutedAt.UTC()
	}

	previous := slices.Clone(s.data.History)
	s.data.History = append([]HistoryEntry{entry}, s.data.History...)
	if len(s.data.History) > maxHistory {
		s.data.History = s.data.History[:maxHistory]
	}
	if err := s.persistLocked(); err != nil {
		s.data.History = previous
		return err
	}
	return nil
}

// ClearHistory removes all query history.
func (s *Store) ClearHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := s.data.History
	s.data.History = []HistoryEntry{}
	if err := s.persistLocked(); err != nil {
		s.data.History = previous
		return err
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary state: %w", err)
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.data); err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	committed = true
	return nil
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	// crypto/rand failure is exceptionally rare. This fallback still avoids an
	// empty identifier, and uniqueness is reinforced by nanosecond resolution.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
