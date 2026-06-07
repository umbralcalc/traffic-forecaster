package streetmanager

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"testing"
)

// writeStreamingEntry appends one streaming-zip entry (local header with the
// data-descriptor flag set, raw DEFLATE payload, then a PK\x07\x08 data
// descriptor) — mimicking the Street Manager archive layout.
func writeStreamingEntry(buf *bytes.Buffer, name string, payload []byte) {
	var comp bytes.Buffer
	w, _ := flate.NewWriter(&comp, flate.DefaultCompression)
	w.Write(payload)
	w.Close()

	buf.Write([]byte("PK\x03\x04"))
	hdr := make([]byte, 26)
	binary.LittleEndian.PutUint16(hdr[2:4], 0x08) // general purpose flag: data descriptor present
	binary.LittleEndian.PutUint16(hdr[4:6], 8)    // method: deflate
	binary.LittleEndian.PutUint16(hdr[22:24], uint16(len(name)))
	buf.Write(hdr)
	buf.WriteString(name)
	buf.Write(comp.Bytes())

	// Data descriptor with explicit signature.
	buf.Write([]byte("PK\x07\x08"))
	dd := make([]byte, 12) // crc32, compressed size, uncompressed size (values unused by reader)
	binary.LittleEndian.PutUint32(dd[4:8], uint32(comp.Len()))
	binary.LittleEndian.PutUint32(dd[8:12], uint32(len(payload)))
	buf.Write(dd)
}

func TestStreamParsesStreamingZip(t *testing.T) {
	var buf bytes.Buffer
	payloads := [][]byte{
		[]byte(`{"event_type":"PERMIT_GRANTED","object_data":{"highway_authority":"LONDON BOROUGH OF SOUTHWARK","work_category":"Standard"}}`),
		[]byte(`{"event_type":"PERMIT_GRANTED","object_data":{"highway_authority":"DERBY CITY COUNCIL"}}`),
		[]byte(`{"event_type":"PERMIT_GRANTED","object_data":{"highway_authority":"TRANSPORT FOR LONDON","work_category":"Major"}}`),
	}
	for i, p := range payloads {
		writeStreamingEntry(&buf, "e"+string(rune('0'+i))+".json", p)
	}
	// Append a (fake) central directory signature to prove we stop cleanly.
	buf.Write([]byte("PK\x01\x02"))

	var got []Record
	if err := Stream(&buf, func(r Record) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3", len(got))
	}
	if got[0].HighwayAuthority() != "LONDON BOROUGH OF SOUTHWARK" {
		t.Errorf("rec0 HA = %q", got[0].HighwayAuthority())
	}
	if got[0].Field("work_category") != "Standard" {
		t.Errorf("rec0 work_category = %q", got[0].Field("work_category"))
	}
	london := 0
	for _, r := range got {
		if IsLondon(r.HighwayAuthority()) {
			london++
		}
	}
	if london != 2 {
		t.Errorf("london count = %d, want 2", london)
	}
}

func TestIsLondon(t *testing.T) {
	in := map[string]bool{
		"LONDON BOROUGH OF HACKNEY":               true,
		"london borough of camden":                true, // case-insensitive
		"TRANSPORT FOR LONDON":                    true,
		"CITY OF LONDON CORPORATION":              true,
		"CITY OF WESTMINSTER":                     true,
		"ROYAL BOROUGH OF KENSINGTON AND CHELSEA": true,
		"DERBY CITY COUNCIL":                      false,
		"SURREY COUNTY COUNCIL":                   false,
		"":                                        false,
	}
	for ha, want := range in {
		if got := IsLondon(ha); got != want {
			t.Errorf("IsLondon(%q) = %v, want %v", ha, got, want)
		}
	}
}

func TestObjectURL(t *testing.T) {
	if got := ObjectURL("permit", 2026, 5); got != BucketURL+"/permit/2026/05.zip" {
		t.Errorf("ObjectURL = %q", got)
	}
}
