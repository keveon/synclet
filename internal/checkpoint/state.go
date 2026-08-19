package checkpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State holds the persisted cursor of every incremental job.
type State struct {
	Jobs map[string]Cursor `json:"jobs,omitempty"`
}

// Cursor is the composite (cursor, tie_breaker) position of a job. The
// tie breaker is an integer or a string, preserved exactly as read from the
// source so it can be compared natively in the next query.
type Cursor struct {
	Value time.Time `json:"value,omitempty"`
	Tie   any       `json:"tie,omitempty"`
}

// Valid reports whether the cursor can resume from a real position.
func (c Cursor) Valid() bool {
	return !c.Value.IsZero() && c.Tie != nil
}

// Cursor returns the stored cursor of a job, or the zero cursor.
func (s State) Cursor(job string) Cursor {
	if s.Jobs == nil {
		return Cursor{}
	}
	return s.Jobs[job]
}

// SetCursor stores a job cursor.
func (s *State) SetCursor(job string, cursor Cursor) {
	if s.Jobs == nil {
		s.Jobs = map[string]Cursor{}
	}
	s.Jobs[job] = cursor
}

// Store persists checkpoint state across restarts.
type Store interface {
	Load() (State, error)
	Save(State) error
}

// FileStore is the file-backed Store: atomically-written JSON.
type FileStore struct {
	Path string
}

// Load reads the state file. A missing or empty file is an empty state.
func (s FileStore) Load() (State, error) {
	return LoadFile(s.Path)
}

// Save writes the state file atomically (temp file + rename + fsync).
func (s FileStore) Save(state State) error {
	return SaveFile(s.Path, state)
}

// LoadFile reads state from a path.
func LoadFile(path string) (State, error) {
	var state State
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode checkpoint file %s: %w", path, err)
	}
	if err := state.canonicalize(); err != nil {
		return State{}, fmt.Errorf("decode checkpoint file %s: %w", path, err)
	}
	return state, nil
}

// SaveFile writes state to a path atomically.
func SaveFile(path string, state State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".checkpoint-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	dirFile, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer dirFile.Close()
	_ = dirFile.Sync()

	return nil
}

// canonicalize normalizes tie breakers decoded from JSON (json.Number,
// float64) into their canonical in-memory form (int64 or string).
func (s *State) canonicalize() error {
	for job, cursor := range s.Jobs {
		tie, err := CanonicalizeTie(cursor.Tie)
		if err != nil {
			return fmt.Errorf("job %s: %w", job, err)
		}
		cursor.Tie = tie
		s.Jobs[job] = cursor
	}
	return nil
}

// CanonicalizeTie converts a tie-breaker value into its canonical form:
// int64 for integers, string for text. Floats are rejected as inexact.
func CanonicalizeTie(value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, nil
		}
		return trimmed, nil
	case []byte:
		trimmed := string(bytes.TrimSpace(typed))
		if trimmed == "" {
			return nil, nil
		}
		return trimmed, nil
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i, nil
		}
		return nil, fmt.Errorf("tie breaker %s is not an integer", typed.String())
	case float64:
		if typed != math.Trunc(typed) {
			return nil, fmt.Errorf("tie breaker %v is not an integer", typed)
		}
		return int64(typed), nil
	case int:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint:
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint64:
		if typed > math.MaxInt64 {
			return nil, fmt.Errorf("tie breaker %d overflows int64", typed)
		}
		return int64(typed), nil
	default:
		return nil, fmt.Errorf("tie breaker has unsupported type %T", value)
	}
}

// TieString renders a canonical tie breaker for comparison and logs.
func TieString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	case int64:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}
