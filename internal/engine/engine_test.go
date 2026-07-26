package engine_test

import (
	"testing"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/engine"
	"github.com/moveeeax/credential-chase/internal/matrix"
	"github.com/moveeeax/credential-chase/internal/placement"
)

const (
	facilities = "../../examples/facilities"
	placements = "../../examples/placements"
	// demoDate is the date the README example is pinned to.
	demoDate = "2026-08-01"
)

func loadBoard(t *testing.T, asOf string) engine.Board {
	t.Helper()
	set, err := matrix.Load(facilities)
	if err != nil {
		t.Fatalf("load matrices: %v", err)
	}
	ps, err := placement.Load(placements)
	if err != nil {
		t.Fatalf("load placements: %v", err)
	}
	b, err := engine.EvaluateAll(ps, set, engine.Options{AsOf: civil.MustParse(asOf)})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return b
}

func result(t *testing.T, b engine.Board, id string) engine.Result {
	t.Helper()
	for _, r := range b.Results {
		if r.PlacementID == id {
			return r
		}
	}
	t.Fatalf("placement %s not on the board", id)
	return engine.Result{}
}

func item(t *testing.T, r engine.Result, code string) engine.Item {
	t.Helper()
	for _, it := range r.Items {
		if it.Code == code {
			return it
		}
	}
	t.Fatalf("%s: requirement %s not evaluated", r.PlacementID, code)
	return engine.Item{}
}

func TestBoardRAGForExamplePlacements(t *testing.T) {
	b := loadBoard(t, demoDate)

	want := []struct {
		id     string
		status engine.RAG
		open   int
	}{
		{"PL-1042", engine.Red, 3},
		{"PL-1045", engine.Red, 3},
		{"PL-1043", engine.Amber, 1},
		{"PL-1044", engine.Green, 0},
	}
	if len(b.Results) != len(want) {
		t.Fatalf("board has %d placements, want %d", len(b.Results), len(want))
	}
	for i, w := range want {
		got := b.Results[i]
		if got.PlacementID != w.id {
			t.Errorf("row %d: placement %s, want %s (board must sort by start date)", i, got.PlacementID, w.id)
			continue
		}
		if got.Status != w.status {
			t.Errorf("%s: status %s, want %s", w.id, got.Status, w.status)
		}
		if n := len(got.OpenItems()); n != w.open {
			t.Errorf("%s: %d open items, want %d", w.id, n, w.open)
		}
	}
}

// A card that is valid today but lapses partway through the assignment is
// the failure a checklist spreadsheet never catches. It is the reason the
// engine measures coverage from the start date, not from today.
func TestDocumentValidTodayButShortOfLeadDaysIsAGap(t *testing.T) {
	b := loadBoard(t, demoDate)
	it := item(t, result(t, b, "PL-1042"), "bls-aha")

	if it.Status != engine.StatusShort {
		t.Fatalf("status = %s, want %s", it.Status, engine.StatusShort)
	}
	if it.CoverageDays == nil || *it.CoverageDays != 26 {
		t.Fatalf("coverage days = %v, want 26", it.CoverageDays)
	}
	if it.RequiredDays != 30 {
		t.Errorf("required days = %d, want 30", it.RequiredDays)
	}
	if it.Severity != engine.SeverityCritical {
		t.Errorf("severity = %s, want critical four days out", it.Severity)
	}
}

func TestExpiredDocumentIsAlwaysCritical(t *testing.T) {
	// Far enough out that an open item would only be a warning; an already
	// expired document still has to be critical.
	b := loadBoard(t, "2026-07-20")
	r := result(t, b, "PL-1045")
	it := item(t, r, "cna-certification")

	if r.DaysToStart <= engine.DefaultCriticalDays {
		t.Fatalf("test setup: %d days to start is inside the critical window", r.DaysToStart)
	}
	if it.Status != engine.StatusExpired {
		t.Errorf("status = %s, want %s", it.Status, engine.StatusExpired)
	}
	if it.Severity != engine.SeverityCritical {
		t.Errorf("severity = %s, want critical", it.Severity)
	}
}

func TestMissingItemEscalatesFromWarnToCriticalAsStartApproaches(t *testing.T) {
	tests := []struct {
		asOf string
		want engine.Severity
	}{
		{"2026-08-01", engine.SeverityWarn},     // T-18
		{"2026-08-11", engine.SeverityWarn},     // T-8, one day of slack left
		{"2026-08-12", engine.SeverityCritical}, // T-7, the threshold itself
		{"2026-08-20", engine.SeverityCritical}, // start already passed
	}
	for _, tc := range tests {
		t.Run(tc.asOf, func(t *testing.T) {
			b := loadBoard(t, tc.asOf)
			it := item(t, result(t, b, "PL-1043"), "n95-fit-test")
			if it.Status != engine.StatusMissing {
				t.Fatalf("status = %s, want missing", it.Status)
			}
			if it.Severity != tc.want {
				t.Errorf("severity = %s, want %s", it.Severity, tc.want)
			}
		})
	}
}

// The critical threshold is inclusive: a placement flips from amber to red
// on the day it reaches CriticalDays, not the day after.
func TestCriticalThresholdIsInclusive(t *testing.T) {
	amber := result(t, loadBoard(t, "2026-08-11"), "PL-1043")
	if amber.DaysToStart != 8 || amber.Status != engine.Amber {
		t.Errorf("T-%d = %s, want T-8 = amber", amber.DaysToStart, amber.Status)
	}
	red := result(t, loadBoard(t, "2026-08-12"), "PL-1043")
	if red.DaysToStart != 7 || red.Status != engine.Red {
		t.Errorf("T-%d = %s, want T-7 = red", red.DaysToStart, red.Status)
	}
}

func TestRequirementScopedToOtherUnitIsNotEvaluated(t *testing.T) {
	b := loadBoard(t, demoDate)

	medSurg := result(t, b, "PL-1043") // Med-Surg
	for _, it := range medSurg.Items {
		if it.Code == "acls-aha" {
			t.Errorf("ACLS is ICU/ED only but was evaluated for a Med-Surg placement")
		}
	}
	icu := result(t, b, "PL-1042") // ICU
	if got := item(t, icu, "acls-aha"); got.Status != engine.StatusMissing {
		t.Errorf("ICU acls-aha status = %s, want missing", got.Status)
	}
}

func TestWaivedRequirementPassesWithoutADocument(t *testing.T) {
	b := loadBoard(t, demoDate)
	it := item(t, result(t, b, "PL-1042"), "flu-vaccination")

	if it.Status != engine.StatusWaived {
		t.Fatalf("status = %s, want %s", it.Status, engine.StatusWaived)
	}
	if it.Severity != engine.SeverityOK {
		t.Errorf("severity = %s, want ok", it.Severity)
	}
	if it.Detail == "" {
		t.Error("a waiver with no note recorded is worthless at audit time")
	}
}

func TestDocumentFiledUnderUnknownCodeIsAnOrphan(t *testing.T) {
	b := loadBoard(t, demoDate)
	r := result(t, b, "PL-1043")

	if len(r.Orphans) != 1 || r.Orphans[0] != "ppd-test" {
		t.Fatalf("orphans = %v, want [ppd-test]", r.Orphans)
	}
	// An orphan must not be silently credited towards a real requirement.
	if it := item(t, r, "tb-screening"); it.Status != engine.StatusOK {
		t.Errorf("tb-screening status = %s, want ok from its own document", it.Status)
	}
}

func TestExpiringRequirementWithoutARecordedExpiryIsAGap(t *testing.T) {
	m := &matrix.Matrix{
		Facility: matrix.Facility{ID: "f", Name: "F"},
		Requirements: []matrix.Requirement{
			{Code: "bls-aha", Name: "BLS", Category: matrix.CategoryCertification, Expires: true, LeadDays: 30},
		},
	}
	p := &placement.Placement{
		ID: "P-1", Facility: "f", StartDate: civil.MustParse("2026-10-01"),
		Nurse:     placement.Nurse{Name: "A B", Discipline: "RN"},
		Documents: []placement.Document{{Requirement: "bls-aha", Received: civil.MustParse("2026-07-01")}},
	}
	res := engine.Evaluate(p, m, engine.Options{AsOf: civil.MustParse("2026-08-01")})

	it := res.Items[0]
	if it.Status != engine.StatusMissing {
		t.Errorf("status = %s, want missing: a card with no expiry date cannot be proved valid", it.Status)
	}
	if res.Status != engine.Amber {
		t.Errorf("board status = %s, want amber", res.Status)
	}
}

func TestPlacementWithoutAMatrixIsAnError(t *testing.T) {
	set, err := matrix.Load(facilities)
	if err != nil {
		t.Fatalf("load matrices: %v", err)
	}
	p := &placement.Placement{
		ID: "P-9", Facility: "no-such-facility", Path: "memory",
		StartDate: civil.MustParse("2026-10-01"),
		Nurse:     placement.Nurse{Name: "A B", Discipline: "RN"},
	}
	if _, err := engine.EvaluateAll([]*placement.Placement{p}, set, engine.Options{}); err == nil {
		t.Fatal("a placement with no facility matrix was silently accepted")
	}
}
