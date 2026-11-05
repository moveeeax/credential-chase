package civil_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/moveeeax/credential-chase/internal/civil"
)

func TestParseRejectsAnythingButISODates(t *testing.T) {
	for _, s := range []string{"", "01/08/2026", "2026-8-1", "2026-13-01", "2026-02-30", "next tuesday"} {
		if d, err := civil.Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted, got %s", s, d)
		}
	}
}

func TestDaysUntilCrossesMonthAndLeapBoundaries(t *testing.T) {
	tests := []struct {
		from, to string
		want     int
	}{
		{"2026-08-01", "2026-08-01", 0},
		{"2026-08-01", "2026-08-05", 4},
		{"2026-08-31", "2026-09-01", 1},
		{"2026-12-31", "2027-01-01", 1},
		{"2028-02-28", "2028-03-01", 2}, // 2028 is a leap year
		{"2027-02-28", "2027-03-01", 1},
		{"2026-08-05", "2026-08-01", -4},
		{"2026-08-01", "2027-08-01", 365},
	}
	for _, tc := range tests {
		got := civil.MustParse(tc.from).DaysUntil(civil.MustParse(tc.to))
		if got != tc.want {
			t.Errorf("%s -> %s = %d days, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestAddDaysIsTheInverseOfDaysUntil(t *testing.T) {
	base := civil.MustParse("2026-08-01")
	for _, n := range []int{-400, -31, -1, 0, 1, 21, 365, 1000} {
		if got := base.DaysUntil(base.AddDays(n)); got != n {
			t.Errorf("AddDays(%d) then DaysUntil = %d", n, got)
		}
	}
}

func TestYAMLRoundTrip(t *testing.T) {
	type doc struct {
		Start civil.Date `yaml:"start_date"`
	}
	var d doc
	if err := yaml.Unmarshal([]byte("start_date: 2026-09-14\n"), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Start.String() != "2026-09-14" {
		t.Fatalf("got %s, want 2026-09-14", d.Start)
	}
	out, err := yaml.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "start_date: \"2026-09-14\"\n" {
		t.Errorf("marshalled to %q", out)
	}
}

func TestYAMLRejectsABadDateWithItsLineNumber(t *testing.T) {
	type doc struct {
		Start civil.Date `yaml:"start_date"`
	}
	var d doc
	err := yaml.Unmarshal([]byte("# comment\nstart_date: 14/09/2026\n"), &d)
	if err == nil {
		t.Fatal("a non-ISO date was accepted")
	}
	if got := err.Error(); !strings.Contains(got, "line 2") {
		t.Errorf("error %q does not point at line 2", got)
	}
}

func TestJSONRoundTripIncludingZero(t *testing.T) {
	b, err := json.Marshal(civil.MustParse("2026-08-01"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"2026-08-01"` {
		t.Errorf("got %s", b)
	}

	zero, err := json.Marshal(civil.Date{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(zero) != "null" {
		t.Errorf("zero date marshalled to %s, want null", zero)
	}

	var back civil.Date
	if err := json.Unmarshal([]byte("null"), &back); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !back.IsZero() {
		t.Errorf("null decoded to %s", back)
	}
}
