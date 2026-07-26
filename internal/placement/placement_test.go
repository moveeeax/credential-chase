package placement_test

import (
	"os"
	"strings"
	"testing"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/placement"
)

func TestLoadExamplesSortsByStartDate(t *testing.T) {
	ps, err := placement.Load("../../examples/placements")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ps) != 4 {
		t.Fatalf("loaded %d placements, want 4", len(ps))
	}
	want := []string{"PL-1042", "PL-1045", "PL-1043", "PL-1044"}
	for i, id := range want {
		if ps[i].ID != id {
			t.Errorf("position %d = %s, want %s", i, ps[i].ID, id)
		}
	}
}

func TestValidateReportsEveryMissingField(t *testing.T) {
	p, err := placement.ParseFile("testdata/incomplete.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = p.Validate()
	if err == nil {
		t.Fatal("an unusable placement validated cleanly")
	}
	msg := err.Error()
	for _, want := range []string{
		"id is required",
		"facility is required",
		"start_date is required",
		"nurse.name is required",
		"coordinator.email is required",
		"documents[0]", // bls-aha has no received date and is not waived
		"documents[1]", // empty requirement code
		"waiver_note",  // waived without a note
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("validation message does not mention %q:\n%s", want, msg)
		}
	}
}

func TestLoadRejectsDuplicatePlacementIDs(t *testing.T) {
	dir := t.TempDir()
	body := `schema_version: 1
id: PL-1
nurse: {name: A B, discipline: RN}
facility: f
start_date: 2026-09-01
coordinator: {name: C, email: c@example.com}
`
	writeFile(t, dir+"/a.yaml", body)
	writeFile(t, dir+"/b.yaml", body)

	if _, err := placement.Load(dir); err == nil {
		t.Fatal("two placements with the same id were accepted")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/typo.yaml", `schema_version: 1
id: PL-1
nurse: {name: A B, discipline: RN}
facility: f
start_dat: 2026-09-01
coordinator: {name: C, email: c@example.com}
`)
	_, err := placement.ParseFile(dir + "/typo.yaml")
	if err == nil {
		t.Fatal("start_dat typo was accepted")
	}
	if !strings.Contains(err.Error(), "start_dat") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestDocumentLookupIsCaseInsensitive(t *testing.T) {
	p := &placement.Placement{
		Documents: []placement.Document{
			{Requirement: "BLS-AHA", Received: civil.MustParse("2026-07-01")},
		},
	}
	if _, ok := p.Document("bls-aha"); !ok {
		t.Error("a document filed under a differently-cased code was not found")
	}
	if _, ok := p.Document("acls-aha"); ok {
		t.Error("found a document that was never filed")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
