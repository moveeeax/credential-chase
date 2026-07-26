package matrix_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/matrix"
)

func TestLintBrokenMatrixReportsEveryDefect(t *testing.T) {
	m, err := matrix.ParseFile("testdata/broken.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	findings := matrix.Lint(m, civil.MustParse("2026-08-01"))

	want := map[string]int{
		"CM001": 1, // schema_version 2
		"CM002": 1, // empty facility name
		"CM003": 1, // St_Marys is not a slug
		"CM005": 2, // bls-aha declared three times: two of them are dupes
		"CM006": 1, // BLS_AHA is not a slug
		"CM007": 1, // empty requirement name
		"CM008": 2, // "certifcation" typo and one empty category
		"CM009": 2, // lead_days on a non-expiring req, and a negative one
		"CM010": 1, // discipline "Nurse"
		"CM011": 1, // format "tiff"
		"CM012": 1, // intake method "carrier-pigeon"
		"CM014": 1, // updated 2020-01-01 is stale
		"CM015": 1, // no source
	}

	got := map[string]int{}
	for _, f := range findings {
		got[f.Rule]++
	}
	for rule, n := range want {
		if got[rule] != n {
			t.Errorf("rule %s: got %d findings, want %d", rule, got[rule], n)
		}
	}
	for rule, n := range got {
		if _, expected := want[rule]; !expected {
			t.Errorf("unexpected rule %s fired %d time(s)", rule, n)
		}
	}
}

func TestLintBrokenMatrixSeverities(t *testing.T) {
	rep, err := matrix.LintPaths(civil.MustParse("2026-08-01"), "testdata/broken.yaml")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if rep.OK() {
		t.Fatal("broken matrix reported as OK")
	}
	// CM014 and CM015 are hygiene, everything else blocks.
	if want := 2; rep.Warnings() != want {
		t.Errorf("warnings = %d, want %d", rep.Warnings(), want)
	}
	if rep.Errors() != len(rep.Findings)-rep.Warnings() {
		t.Errorf("errors = %d, does not account for the rest of %d findings",
			rep.Errors(), len(rep.Findings))
	}
}

func TestLintRejectsUnknownFields(t *testing.T) {
	rep, err := matrix.LintPaths(civil.MustParse("2026-08-01"), "testdata/unknown-field.yaml")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(rep.Findings), rep.Findings)
	}
	f := rep.Findings[0]
	if f.Rule != "CM000" || f.Severity != matrix.Error {
		t.Fatalf("got %+v, want a CM000 error", f)
	}
	if !strings.Contains(f.Message, "requirments") {
		t.Errorf("message does not name the offending key: %q", f.Message)
	}
}

func TestLintShippedExamplesAreClean(t *testing.T) {
	// The examples double as the README's runnable demo. If they ever stop
	// linting cleanly, the README is lying.
	rep, err := matrix.LintPaths(civil.MustParse("2026-08-01"), "../../examples/facilities")
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if rep.Checked != 2 {
		t.Errorf("checked %d files, want 2", rep.Checked)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("example matrices are not clean: %v", rep.Findings)
	}
}

func TestLintFlagsStaleMatrixOnlyAfterThreshold(t *testing.T) {
	m, err := matrix.ParseFile("../../examples/facilities/st-marys-rochester.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	updated := civil.MustParse("2026-07-01")

	if fs := matrix.Lint(m, updated.AddDays(matrix.StaleAfterDays)); len(fs) != 0 {
		t.Errorf("flagged at exactly the threshold: %v", fs)
	}
	fs := matrix.Lint(m, updated.AddDays(matrix.StaleAfterDays+1))
	if len(fs) != 1 || fs[0].Rule != "CM014" {
		t.Fatalf("got %v, want a single CM014", fs)
	}
	if fs[0].Severity != matrix.Warning {
		t.Errorf("staleness severity = %s, want warning", fs[0].Severity)
	}
}

func TestFindingsAreSortedByRule(t *testing.T) {
	m, err := matrix.ParseFile("testdata/broken.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fs := matrix.Lint(m, civil.MustParse("2026-08-01"))
	rules := make([]string, len(fs))
	for i, f := range fs {
		rules[i] = f.Rule
	}
	if !sort.StringsAreSorted(rules) {
		t.Errorf("findings are not rule-sorted: %v", rules)
	}
}
