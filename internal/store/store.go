// Package store reads and writes the canonical daily snapshot files. Snapshots
// are gzipped JSON (data/raw/YYYY-MM-DD.json.gz): lossless, append-only, and
// never rewritten. Gzip keeps the archive ~11x smaller than plain JSON while
// preserving every field, since the snapshots are the evidence every resolution
// is later derived from.
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileExt is the suffix for snapshot files written by this package.
const FileExt = ".json.gz"

// Snapshot is the on-disk envelope. Records are stored verbatim (raw JSON) so no
// field is ever lost, whatever the typed model in internal/tfl knows about.
type Snapshot struct {
	// CapturedAt is the UTC instant the feed was fetched. Resolution rules key
	// off this, so it is recorded on every snapshot.
	CapturedAt time.Time `json:"captured_at"`
	Source     string    `json:"source"`
	Count      int       `json:"count"`
	// AppKeyUsed records whether the pull was authenticated, since an
	// unauthenticated pull is likelier to have been rate-limited or truncated.
	AppKeyUsed  bool              `json:"app_key_used"`
	Disruptions []json.RawMessage `json:"disruptions"`
}

// Path returns the snapshot path for the given capture time under dir, e.g.
// dir/2026-06-06.json.gz. The date is taken in UTC so a file's name always
// matches the instant it was captured.
func Path(dir string, capturedAt time.Time) string {
	return filepath.Join(dir, capturedAt.UTC().Format("2006-01-02")+FileExt)
}

// Write gzips and writes snap to path, creating parent directories. It does not
// overwrite an existing file unless force is true; the bool reports whether a
// file was actually written.
func Write(path string, snap Snapshot, force bool) (written bool, err error) {
	if !force {
		if _, statErr := os.Stat(path); statErr == nil {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	enc.SetIndent("", " ")
	if err := enc.Encode(snap); err != nil {
		return false, fmt.Errorf("encoding snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return false, fmt.Errorf("finishing gzip: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// Read decodes a snapshot from path, transparently handling both gzipped
// (.json.gz) and plain (.json) files so older or hand-made snapshots still load.
func Read(path string) (Snapshot, error) {
	var snap Snapshot
	f, err := os.Open(path)
	if err != nil {
		return snap, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return snap, fmt.Errorf("opening gzip %s: %w", path, err)
		}
		defer zr.Close()
		r = zr
	}
	if err := json.NewDecoder(r).Decode(&snap); err != nil {
		return snap, fmt.Errorf("decoding %s: %w", path, err)
	}
	return snap, nil
}
