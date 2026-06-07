// Package tfl is a small client for the parts of the TfL Unified API that
// traffic-forecaster depends on: the road disruption feed and its metadata
// vocabularies. See PLAN.md for the project framing.
package tfl

import (
	"encoding/json"
	"time"
)

// RoadDisruption is a typed view of a single record from
// GET /Road/all/Disruption. The field set was verified against the live API on
// 2026-06-06; notably the feed carries no creation timestamp (first-appearance
// must be reconstructed from our own daily snapshots) and `severity`/`category`
// are free-text descriptions, not the numeric levels from the Meta endpoints.
//
// Snapshots store records verbatim as raw JSON (see Snapshot), so this struct is
// a lossy convenience for EDA/forecasting consumers, not the canonical record.
type RoadDisruption struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Category string `json:"category"`
	// SubCategory refines Category (e.g. "Utility works" under "Works").
	SubCategory string `json:"subCategory"`
	// Severity is a description string ("Serious", "Moderate", "Minimal", ...),
	// NOT the non-monotonic numeric severityLevel from /Road/Meta/Severities.
	Severity string `json:"severity"`
	Ordinal  int    `json:"ordinal"`

	Comments      string `json:"comments"`
	CurrentUpdate string `json:"currentUpdate"`
	// Location is free text that reliably ends with the borough(s) in
	// parentheses, e.g. "... (E14 ) (Tower Hamlets)". It is present on every
	// record, unlike CorridorIds which is empty roughly half the time.
	Location string `json:"location"`
	// CorridorIds references /Road corridor ids but is sparse and free-text-ish
	// ("inner ring", "silvertown tunnel"); prefer Location-derived borough as the
	// primary forecasting key.
	CorridorIds []string `json:"corridorIds"`

	LevelOfInterest string `json:"levelOfInterest"`
	IsProvisional   bool   `json:"isProvisional"`
	HasClosures     bool   `json:"hasClosures"`

	StartDateTime         time.Time `json:"startDateTime"`
	EndDateTime           time.Time `json:"endDateTime"`
	LastModifiedTime      time.Time `json:"lastModifiedTime"`
	CurrentUpdateDateTime time.Time `json:"currentUpdateDateTime"`

	// Structured payloads we keep verbatim until a consumer needs them.
	Point                    string          `json:"point"`
	Geography                json.RawMessage `json:"geography"`
	RecurringSchedules       json.RawMessage `json:"recurringSchedules"`
	RoadDisruptionImpactArea json.RawMessage `json:"roadDisruptionImpactAreas"`
	RoadDisruptionLines      json.RawMessage `json:"roadDisruptionLines"`
}

// Severity is one entry from /Road/Meta/Severities.
type Severity struct {
	ModeName      string `json:"modeName"`
	SeverityLevel int    `json:"severityLevel"`
	Description   string `json:"description"`
}
