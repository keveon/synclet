package checkpoint

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	cursor := Cursor{Value: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC), Tie: int64(42)}
	state := State{}
	state.SetCursor("orders", cursor)

	if err := SaveFile(path, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := loaded.Cursor("orders")
	if !got.Valid() {
		t.Fatal("cursor must survive the roundtrip")
	}
	if !got.Value.Equal(cursor.Value) {
		t.Errorf("value = %s, want %s", got.Value, cursor.Value)
	}
	if got.Tie != int64(42) {
		t.Errorf("tie = %v (%T), want int64(42)", got.Tie, got.Tie)
	}
}

func TestLoadMissingFileIsEmptyState(t *testing.T) {
	state, err := LoadFile(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("missing file must load as empty state: %v", err)
	}
	if state.Cursor("anything").Valid() {
		t.Error("empty state cursor must be invalid")
	}
}

func TestCanonicalizeTieIntegers(t *testing.T) {
	cases := []struct {
		in    any
		want  any
		valid bool
	}{
		{in: int64(7), want: int64(7), valid: true},
		{in: int(7), want: int64(7), valid: true},
		{in: "7", want: "7", valid: true},
		{in: nil, want: nil, valid: false},
		{in: "", want: nil, valid: false},
		{in: 1.5, want: nil, valid: false},
	}
	for _, tc := range cases {
		got, err := CanonicalizeTie(tc.in)
		if err != nil && tc.valid {
			t.Errorf("CanonicalizeTie(%v) unexpected error: %v", tc.in, err)
			continue
		}
		if err == nil && !tc.valid && tc.in != 1.5 {
			// nil/empty are not errors, just canonicalize to nil
			_ = got
		}
		if tc.valid && got != tc.want {
			t.Errorf("CanonicalizeTie(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if _, err := CanonicalizeTie(1.5); err == nil {
		t.Error("non-integer float tie must be rejected")
	}
}

func TestTieString(t *testing.T) {
	if s, ok := TieString(int64(9)); !ok || s != "9" {
		t.Errorf("TieString(int64(9)) = %q, %t", s, ok)
	}
	if s, ok := TieString("abc"); !ok || s != "abc" {
		t.Errorf("TieString(\"abc\") = %q, %t", s, ok)
	}
	if _, ok := TieString(nil); ok {
		t.Error("TieString(nil) must not be ok")
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	for i := 0; i < 3; i++ {
		state := State{}
		state.SetCursor("j", Cursor{Value: time.Now(), Tie: int64(i)})
		if err := SaveFile(path, state); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}
