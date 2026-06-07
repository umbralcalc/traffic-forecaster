// Package disruptions holds the domain logic for turning raw TfL records into
// the quantities the forecast targets: borough attribution, a severity ordering,
// and the planned/unplanned split. These are deliberately small, pure, and
// tested, because the resolution criterion is defined in their terms.
package disruptions

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/umbralcalc/traffic-forecaster/internal/store"
	"github.com/umbralcalc/traffic-forecaster/internal/tfl"
)

// Decode turns a snapshot's verbatim records into typed disruptions. Records
// that fail to decode are skipped and counted in the returned skip total, so a
// caller can report data quality rather than silently lose events.
func Decode(snap store.Snapshot) (out []tfl.RoadDisruption, skipped int) {
	for _, raw := range snap.Disruptions {
		var d tfl.RoadDisruption
		if err := json.Unmarshal(raw, &d); err != nil {
			skipped++
			continue
		}
		out = append(out, d)
	}
	return out, skipped
}

// severityRank orders the road severity descriptions by disruptiveness, highest
// = worst. It follows TfL's own severityLevel numbering (from /Road/Meta/
// Severities), where 5..10 is a monotonic band — 5 "Closure" is the most
// disruptive, 10 "No Exceptional Delays" the least — and 0 "No Issues" is a
// separate baseline. The "severity >= threshold" target keys off this rank.
var severityRank = map[string]int{
	"No Issues":             0,
	"No Exceptional Delays": 1,
	"Minimal":               2,
	"Moderate":              3,
	"Serious":               4,
	"Severe":                5,
	"Closure":               6,
}

// SeverityRank returns the disruptiveness rank of a feed severity description
// (higher = worse) and whether it was recognised. Unknown values rank 0 so they
// never spuriously clear a threshold.
func SeverityRank(description string) (int, bool) {
	r, ok := severityRank[strings.TrimSpace(description)]
	return r, ok
}

// plannedCategories are the feed `category` values that represent scheduled
// activity (works, announced events) rather than emergent incidents. Verified
// against the live feed 2026-06-06; the feed's category vocabulary differs from
// /Road/Meta/Categories, so this keys off the values the feed actually emits.
var plannedCategories = map[string]bool{
	"Works":          true,
	"Planned events": true,
}

// IsPlanned reports whether a category is scheduled activity. The complement —
// network delays, emergency service incidents, asset issues — is the genuine
// forecasting challenge; planned works are partly knowable in advance.
func IsPlanned(category string) bool {
	return plannedCategories[strings.TrimSpace(category)]
}

// boroughParen pulls parenthetical groups out of a Location string.
var boroughParen = regexp.MustCompile(`\(([^()]*)\)`)

// Boroughs extracts the London borough(s) a disruption sits in from its Location
// field, which reliably ends with the borough(s) in parentheses, e.g.
// "... (E14 ) (Tower Hamlets)" or "... (Hammersmith & Fulham,Kensington &
// Chelsea)". Returns nil if nothing parseable is found. A record can span
// several boroughs, so the caller decides whether to count it once or per
// borough.
func Boroughs(location string) []string {
	groups := boroughParen.FindAllStringSubmatch(location, -1)
	if len(groups) == 0 {
		return nil
	}
	last := strings.TrimSpace(groups[len(groups)-1][1])
	if last == "" {
		return nil
	}
	var out []string
	for _, b := range strings.Split(last, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}
