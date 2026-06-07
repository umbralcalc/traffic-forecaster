// Package streetmanager reads DfT Street Manager open-data archives — the
// forward-dated planned-roadworks source for the burden forecast. The archive is
// a public, OGL-licensed S3 bucket; this code streams and filters it on demand
// rather than committing any of it to the repo (we cite the source instead).
//
// Source: https://opendata.manage-roadworks.service.gov.uk/{type}/{YYYY}/{MM}.zip
// Licence: Open Government Licence v3.0 (DfT / Crown copyright).
//
// The archive files are "streaming" zips: a sequence of local file headers each
// followed by a raw DEFLATE stream and a trailing data descriptor, with no
// central directory. Standard zip readers (including Go's archive/zip and
// Python's zipfile) reject them, so we walk the local headers ourselves.
package streetmanager

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// BucketURL is the public archive root.
const BucketURL = "https://opendata.manage-roadworks.service.gov.uk"

// Attribution is the OGL credit to record on anything derived from this data.
const Attribution = "Contains public sector information licensed under the Open Government Licence v3.0 (DfT Street Manager)."

// ObjectURL returns the archive URL for a given record type, year and month,
// e.g. ObjectURL("permit", 2026, 5) -> ".../permit/2026/05.zip".
func ObjectURL(recordType string, year, month int) string {
	return fmt.Sprintf("%s/%s/%04d/%02d.zip", BucketURL, recordType, year, month)
}

// Record is the notification envelope. ObjectData is left raw so callers pick
// the fields they need without this package tracking the full permit schema.
type Record struct {
	EventType  string          `json:"event_type"`
	EventTime  string          `json:"event_time"`
	ObjectType string          `json:"object_type"`
	ObjectData json.RawMessage `json:"object_data"`
}

// HighwayAuthority pulls just the highway_authority string from a record's
// object_data without fully decoding it, for cheap filtering during a stream.
func (r Record) field(name string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(r.ObjectData, &m); err != nil {
		return ""
	}
	raw, ok := m[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// HighwayAuthority returns the record's highway_authority.
func (r Record) HighwayAuthority() string { return r.field("highway_authority") }

// MarshalLine re-serialises the record for an NDJSON extract.
func (r Record) MarshalLine() ([]byte, error) { return json.Marshal(r) }

// Field returns an arbitrary string field from object_data (e.g. "work_category",
// "proposed_start_date", "traffic_management_type").
func (r Record) Field(name string) string { return r.field(name) }

// IsLondon reports whether a highway_authority belongs to a London authority
// (the 32 boroughs, the City, and Transport for London). Most boroughs are
// "LONDON BOROUGH OF X"; the rest are named individually.
func IsLondon(highwayAuthority string) bool {
	ha := strings.ToUpper(strings.TrimSpace(highwayAuthority))
	if strings.HasPrefix(ha, "LONDON BOROUGH OF ") {
		return true
	}
	return londonSpecials[ha]
}

var londonSpecials = map[string]bool{
	"TRANSPORT FOR LONDON":                    true,
	"CITY OF LONDON CORPORATION":              true,
	"CITY OF LONDON":                          true,
	"CITY OF WESTMINSTER":                     true,
	"ROYAL BOROUGH OF KENSINGTON AND CHELSEA": true,
	"ROYAL BOROUGH OF KINGSTON UPON THAMES":   true,
	"ROYAL BOROUGH OF GREENWICH":              true,
	"LONDON LEGACY DEVELOPMENT CORPORATION":   true,
}

// Stream walks a Street Manager archive, decoding each entry and invoking fn.
// Returning an error from fn aborts the stream. r is typically an HTTP response
// body, so nothing is buffered to disk.
func Stream(r io.Reader, fn func(Record) error) error {
	br := bufio.NewReaderSize(r, 1<<20)

	sig, err := nextSignature(br)
	for {
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch sigKind(sig) {
		case sigLocalHeader:
			data, derr := readEntry(br)
			if derr != nil {
				return derr
			}
			var rec Record
			if json.Unmarshal(data, &rec) == nil {
				if ferr := fn(rec); ferr != nil {
					return ferr
				}
			}
			sig, err = nextSignature(br)
		case sigCentralDir, sigEndOfDir:
			// Reached the central directory (if present) — no more entries.
			return nil
		default:
			// Stray data-descriptor signature; resync to the next one.
			sig, err = nextSignature(br)
		}
	}
}

const (
	sigLocalHeader = iota
	sigCentralDir
	sigEndOfDir
	sigDataDescriptor
	sigUnknown
)

func sigKind(sig []byte) int {
	if len(sig) != 4 || sig[0] != 'P' || sig[1] != 'K' {
		return sigUnknown
	}
	switch {
	case sig[2] == 0x03 && sig[3] == 0x04:
		return sigLocalHeader
	case sig[2] == 0x01 && sig[3] == 0x02:
		return sigCentralDir
	case sig[2] == 0x05 && sig[3] == 0x06:
		return sigEndOfDir
	case sig[2] == 0x07 && sig[3] == 0x08:
		return sigDataDescriptor
	}
	return sigUnknown
}

// readEntry consumes a local file header (the 4-byte signature already read) and
// returns the inflated entry bytes. Because these are streaming zips with the
// compressed size unknown up front, we rely on the DEFLATE stream ending itself;
// passing the *bufio.Reader (an io.ByteReader) to flate keeps it from reading
// past the stream, so the reader is left positioned at the data descriptor.
func readEntry(br *bufio.Reader) ([]byte, error) {
	hdr := make([]byte, 26) // local header minus the 4-byte signature
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, err
	}
	method := binary.LittleEndian.Uint16(hdr[4:6])
	nameLen := binary.LittleEndian.Uint16(hdr[22:24])
	extraLen := binary.LittleEndian.Uint16(hdr[24:26])
	if _, err := io.CopyN(io.Discard, br, int64(nameLen)+int64(extraLen)); err != nil {
		return nil, err
	}
	if method != 8 {
		return nil, fmt.Errorf("streetmanager: unsupported compression method %d", method)
	}
	fr := flate.NewReader(br)
	data, err := io.ReadAll(fr)
	fr.Close()
	if err != nil {
		return nil, fmt.Errorf("streetmanager: inflate: %w", err)
	}
	return data, nil
}

// nextSignature scans byte-by-byte until it finds a PK?? zip signature, consumes
// it, and returns it. This skips over any data descriptor between entries.
func nextSignature(br *bufio.Reader) ([]byte, error) {
	window := make([]byte, 0, 4)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if len(window) == 4 {
			window = window[1:]
		}
		window = append(window, b)
		if len(window) == 4 && window[0] == 'P' && window[1] == 'K' &&
			(window[2] == 0x03 || window[2] == 0x01 || window[2] == 0x05 || window[2] == 0x07) {
			sig := make([]byte, 4)
			copy(sig, window)
			return sig, nil
		}
	}
}
