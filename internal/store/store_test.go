package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	captured := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	path := Path(dir, captured)
	if got := filepath.Base(path); got != "2026-06-06.json.gz" {
		t.Fatalf("Path base = %s, want 2026-06-06.json.gz", got)
	}

	snap := Snapshot{
		CapturedAt:  captured,
		Source:      "test",
		Count:       1,
		AppKeyUsed:  true,
		Disruptions: []json.RawMessage{json.RawMessage(`{"id":"TIMS-1","severity":"Serious"}`)},
	}

	written, err := Write(path, snap, false)
	if err != nil || !written {
		t.Fatalf("Write: written=%v err=%v", written, err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.CapturedAt.Equal(captured) || got.Count != 1 || !got.AppKeyUsed {
		t.Errorf("envelope mismatch: %+v", got)
	}
	// The encoder reformats whitespace inside raw records, so compare compacted
	// (semantic) JSON rather than bytes — no field should be lost.
	if len(got.Disruptions) != 1 {
		t.Fatalf("got %d records, want 1", len(got.Disruptions))
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Disruptions[0]); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `{"id":"TIMS-1","severity":"Serious"}` {
		t.Errorf("record mismatch: %s", compact.String())
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir, time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC))
	snap := Snapshot{Source: "first"}

	if w, err := Write(path, snap, false); err != nil || !w {
		t.Fatalf("first Write: w=%v err=%v", w, err)
	}
	// Second write without force must be a no-op.
	if w, err := Write(path, Snapshot{Source: "second"}, false); err != nil || w {
		t.Fatalf("second Write: want no-op, got w=%v err=%v", w, err)
	}
	got, _ := Read(path)
	if got.Source != "first" {
		t.Errorf("file was overwritten: source=%q", got.Source)
	}
	// With force it should overwrite.
	if w, err := Write(path, Snapshot{Source: "second"}, true); err != nil || !w {
		t.Fatalf("forced Write: w=%v err=%v", w, err)
	}
	if got, _ := Read(path); got.Source != "second" {
		t.Errorf("force did not overwrite: source=%q", got.Source)
	}
}

// TestReadPlainJSON ensures non-gzipped snapshots still load (e.g. a hand-made
// file or the very first un-gzipped snapshot).
func TestReadPlainJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-06.json")
	if err := os.WriteFile(path, []byte(`{"source":"plain","count":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read plain: %v", err)
	}
	if got.Source != "plain" || got.Count != 2 {
		t.Errorf("plain decode mismatch: %+v", got)
	}
}
