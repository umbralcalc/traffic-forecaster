package tfl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// sampleRecord is one verbatim record from the live feed (2026-06-06), used to
// pin the typed decode against the real field shape.
const sampleRecord = `{
  "id": "TIMS-228980",
  "severity": "Serious",
  "category": "Works",
  "subCategory": "Utility works",
  "corridorIds": ["silvertown tunnel"],
  "location": "[A1020] LEAMOUTH ROAD ROUNDABOUT (E14 ) (Tower Hamlets)",
  "startDateTime": "2026-05-20T23:05:00Z",
  "endDateTime": "2026-06-02T21:00:00Z",
  "lastModifiedTime": "2026-05-28T14:47:36Z",
  "isProvisional": false,
  "hasClosures": false,
  "geography": {"type":"Point","coordinates":[0.000218,51.511099]}
}`

func TestRoadDisruptionDecode(t *testing.T) {
	var d RoadDisruption
	if err := json.Unmarshal([]byte(sampleRecord), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.ID != "TIMS-228980" || d.Severity != "Serious" || d.SubCategory != "Utility works" {
		t.Errorf("unexpected fields: %+v", d)
	}
	if got := d.StartDateTime.Format("2006-01-02"); got != "2026-05-20" {
		t.Errorf("startDateTime = %s, want 2026-05-20", got)
	}
	if len(d.CorridorIds) != 1 || d.CorridorIds[0] != "silvertown tunnel" {
		t.Errorf("corridorIds = %v", d.CorridorIds)
	}
	if len(d.Geography) == 0 {
		t.Error("geography raw payload was dropped")
	}
}

// TestDisruptionsRetries checks that a 429 is retried and the subsequent 200 is
// returned, and that app_key + User-Agent are sent.
func TestDisruptionsRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		if r.URL.Query().Get("app_key") != "testkey" {
			t.Errorf("app_key = %q", r.URL.Query().Get("app_key"))
		}
		if r.URL.Query().Get("stripContent") != "true" {
			t.Errorf("stripContent = %q", r.URL.Query().Get("stripContent"))
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("[" + sampleRecord + "]"))
	}))
	defer srv.Close()

	c := NewClient("testkey", 1)
	c.BaseURL = srv.URL
	c.BaseDelay = time.Millisecond

	recs, err := c.Disruptions(context.Background())
	if err != nil {
		t.Fatalf("Disruptions: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if calls != 2 {
		t.Errorf("server saw %d calls, want 2 (one retry)", calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Errorf("numeric Retry-After = %v, want 3s", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("empty Retry-After = %v, want 0", d)
	}
}
