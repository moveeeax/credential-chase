package chase_test

import (
	"bytes"
	"encoding/csv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/credential-chase/internal/chase"
	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/engine"
	"github.com/moveeeax/credential-chase/internal/matrix"
	"github.com/moveeeax/credential-chase/internal/placement"
)

func TestCurrentWindowPicksTheTightestCrossedThreshold(t *testing.T) {
	tests := []struct {
		daysToStart int
		want        int
		active      bool
	}{
		{60, 0, false}, // nothing crossed yet
		{22, 0, false}, // one day before the first window
		{21, 21, true}, // the threshold itself is inside the window
		{18, 21, true},
		{14, 14, true},
		{8, 14, true}, // T-7 has not been reached yet
		{7, 7, true},
		{4, 7, true},
		{3, 3, true},
		{2, 3, true},
		{1, 1, true},
		{0, 1, true},  // starts today
		{-3, 1, true}, // already started, still chasing
	}
	for _, tc := range tests {
		got, active := chase.CurrentWindow(tc.daysToStart)
		if active != tc.active || (active && got != tc.want) {
			t.Errorf("CurrentWindow(%d) = (%d, %v), want (%d, %v)",
				tc.daysToStart, got, active, tc.want, tc.active)
		}
	}
}

func demoPlan(t *testing.T, asOf string, ledger *chase.Ledger) (chase.Plan, engine.Board, []*placement.Placement) {
	t.Helper()
	set, err := matrix.Load("../../examples/facilities")
	if err != nil {
		t.Fatalf("load matrices: %v", err)
	}
	ps, err := placement.Load("../../examples/placements")
	if err != nil {
		t.Fatalf("load placements: %v", err)
	}
	b, err := engine.EvaluateAll(ps, set, engine.Options{AsOf: civil.MustParse(asOf)})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return chase.Schedule(b, ps, ledger), b, ps
}

func TestScheduleContactsEveryOpenPlacementOnceAndDigestsTheCoordinator(t *testing.T) {
	plan, _, _ := demoPlan(t, "2026-08-01", chase.NewLedger())

	var sms, email, digest int
	seen := map[string]bool{}
	for _, a := range plan.Actions {
		if seen[a.Key] {
			t.Errorf("duplicate action key %s in a single plan", a.Key)
		}
		seen[a.Key] = true
		switch a.Channel {
		case chase.ChannelSMS:
			sms++
		case chase.ChannelEmail:
			email++
		case chase.ChannelDigest:
			digest++
		}
	}
	// PL-1042, PL-1043 and PL-1045 have open items and are inside a window;
	// PL-1044 is green and 44 days out.
	if sms != 3 || email != 3 {
		t.Errorf("sms/email = %d/%d, want 3/3", sms, email)
	}
	if digest != 1 {
		t.Errorf("digest actions = %d, want exactly one per coordinator per day", digest)
	}
	for _, a := range plan.Actions {
		if a.PlacementID == "PL-1044" {
			t.Errorf("green placement PL-1044 was chased: %s", a.Key)
		}
	}
}

func TestScheduleUsesTheWindowTheStartDateHasReached(t *testing.T) {
	plan, _, _ := demoPlan(t, "2026-08-01", chase.NewLedger())

	want := map[string]int{"PL-1042": 7, "PL-1043": 21, "PL-1045": 7}
	for _, a := range plan.Actions {
		if a.Channel == chase.ChannelDigest {
			continue
		}
		if w, ok := want[a.PlacementID]; ok && a.Window != w {
			t.Errorf("%s: window T-%d, want T-%d", a.PlacementID, a.Window, w)
		}
		if !strings.Contains(a.Key, "T-") {
			t.Errorf("key %q does not carry the window, so a later window would be deduped away", a.Key)
		}
	}
}

func TestLedgerSuppressesARepeatRunOnTheSameDay(t *testing.T) {
	ledger := chase.NewLedger()
	first, _, _ := demoPlan(t, "2026-08-01", ledger)
	if len(first.Actions) == 0 {
		t.Fatal("nothing due on the demo date")
	}
	for _, a := range first.Actions {
		if !ledger.Record(a) {
			t.Errorf("Record(%s) reported a duplicate on first write", a.Key)
		}
	}

	second, _, _ := demoPlan(t, "2026-08-01", ledger)
	if len(second.Actions) != 0 {
		t.Errorf("%d action(s) survived the ledger: %v", len(second.Actions), second.Actions)
	}
	if len(second.Suppressed) != len(first.Actions) {
		t.Errorf("suppressed %d, want %d", len(second.Suppressed), len(first.Actions))
	}
}

func TestCrossingTheNextWindowChasesAgain(t *testing.T) {
	ledger := chase.NewLedger()
	first, _, _ := demoPlan(t, "2026-08-01", ledger) // PL-1043 is at T-21
	for _, a := range first.Actions {
		ledger.Record(a)
	}

	// Five days later PL-1043 has reached T-14 and must be chased again,
	// while the T-21 messages stay suppressed.
	later, _, _ := demoPlan(t, "2026-08-06", ledger)
	var windows []int
	for _, a := range later.Actions {
		if a.PlacementID == "PL-1043" {
			windows = append(windows, a.Window)
		}
	}
	if len(windows) != 2 {
		t.Fatalf("PL-1043 got %d new actions, want 2 (sms + email at T-14)", len(windows))
	}
	for _, w := range windows {
		if w != 14 {
			t.Errorf("window T-%d, want T-14", w)
		}
	}
}

func TestRecordIsIdempotent(t *testing.T) {
	ledger := chase.NewLedger()
	a := chase.Action{Key: "PL-1|T-7|sms", Channel: chase.ChannelSMS, DueOn: civil.MustParse("2026-08-01")}

	if !ledger.Record(a) {
		t.Fatal("first Record returned false")
	}
	if ledger.Record(a) {
		t.Error("second Record returned true; the ledger is not idempotent")
	}
	if len(ledger.Entries) != 1 {
		t.Errorf("%d entries, want 1", len(ledger.Entries))
	}
}

func TestLedgerRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "ledger.json")

	empty, err := chase.LoadLedger(path)
	if err != nil {
		t.Fatalf("loading a missing ledger must not fail: %v", err)
	}
	if len(empty.Entries) != 0 {
		t.Fatalf("missing ledger produced %d entries", len(empty.Entries))
	}

	plan, _, _ := demoPlan(t, "2026-08-01", empty)
	for _, a := range plan.Actions {
		empty.Record(a)
	}
	if err := chase.SaveLedger(path, empty); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := chase.LoadLedger(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Entries) != len(empty.Entries) {
		t.Fatalf("reloaded %d entries, want %d", len(reloaded.Entries), len(empty.Entries))
	}
	for _, e := range empty.Entries {
		if !reloaded.Has(e.Key) {
			t.Errorf("key %s did not survive the round trip", e.Key)
		}
	}
}

func TestAuditCSVHasOneRowPerLedgerEntry(t *testing.T) {
	ledger := chase.NewLedger()
	plan, _, _ := demoPlan(t, "2026-08-01", ledger)
	for _, a := range plan.Actions {
		ledger.Record(a)
	}

	var buf bytes.Buffer
	if err := chase.WriteAuditCSV(&buf, ledger); err != nil {
		t.Fatalf("export: %v", err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(rows) != len(ledger.Entries)+1 {
		t.Fatalf("%d rows, want %d entries plus a header", len(rows), len(ledger.Entries))
	}
	if len(rows[0]) != len(chase.AuditHeader) {
		t.Fatalf("header has %d columns, want %d", len(rows[0]), len(chase.AuditHeader))
	}
	for i, col := range chase.AuditHeader {
		if rows[0][i] != col {
			t.Errorf("column %d = %q, want %q", i, rows[0][i], col)
		}
	}
	// Every row must carry the day it was sent; an audit line without a
	// date is not an audit line.
	for i, row := range rows[1:] {
		if _, err := civil.Parse(row[0]); err != nil {
			t.Errorf("row %d sent_on = %q: %v", i, row[0], err)
		}
	}
}

func TestNurseMessageNamesTheOutstandingDocuments(t *testing.T) {
	plan, _, _ := demoPlan(t, "2026-08-01", chase.NewLedger())

	for _, a := range plan.Actions {
		if a.PlacementID != "PL-1042" || a.Channel != chase.ChannelSMS {
			continue
		}
		for _, want := range []string{"ACLS provider card", "BLS provider card", "Facility orientation packet"} {
			if !strings.Contains(a.Body, want) {
				t.Errorf("message does not mention %q:\n%s", want, a.Body)
			}
		}
		if !strings.Contains(a.Body, "Dana") {
			t.Errorf("message does not address the nurse:\n%s", a.Body)
		}
		return
	}
	t.Fatal("no SMS action for PL-1042")
}
