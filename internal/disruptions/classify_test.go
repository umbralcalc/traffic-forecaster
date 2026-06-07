package disruptions

import (
	"reflect"
	"testing"
)

func TestBoroughs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"[A1020] LEAMOUTH ROAD ROUNDABOUT (E14 ) (Tower Hamlets)", []string{"Tower Hamlets"}},
		{"SOME ROAD (Hammersmith & Fulham,Kensington & Chelsea)", []string{"Hammersmith & Fulham", "Kensington & Chelsea"}},
		{"NO PARENS HERE", nil},
		{"TRAILING EMPTY ()", nil},
		{"ONLY POSTCODE (SW1A)", []string{"SW1A"}}, // last paren is all we can do; flagged in EDA
	}
	for _, c := range cases {
		if got := Boroughs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Boroughs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSeverityRankOrdering(t *testing.T) {
	closure, _ := SeverityRank("Closure")
	serious, _ := SeverityRank("Serious")
	minimal, _ := SeverityRank("Minimal")
	none, _ := SeverityRank("No Issues")
	if !(closure > serious && serious > minimal && minimal > none) {
		t.Errorf("ranks not ordered: closure=%d serious=%d minimal=%d none=%d", closure, serious, minimal, none)
	}
	if _, ok := SeverityRank("Nonsense"); ok {
		t.Error("unknown severity reported as recognised")
	}
	if r, _ := SeverityRank("Nonsense"); r != 0 {
		t.Errorf("unknown severity rank = %d, want 0", r)
	}
}

func TestIsPlanned(t *testing.T) {
	for _, c := range []string{"Works", "Planned events"} {
		if !IsPlanned(c) {
			t.Errorf("IsPlanned(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"Network delays", "Emergency service incidents", "Asset issues"} {
		if IsPlanned(c) {
			t.Errorf("IsPlanned(%q) = true, want false", c)
		}
	}
}
