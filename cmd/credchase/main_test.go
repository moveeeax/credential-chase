package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exec runs the CLI in-process and returns stdout, stderr and the exit code.
func exec(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, &out, &errOut)
	code := 0
	if err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			code = int(ec)
		} else {
			code = 1
			errOut.WriteString(err.Error())
		}
	}
	return out.String(), errOut.String(), code
}

const (
	facilities = "../../examples/facilities"
	placements = "../../examples/placements"
	demoDate   = "2026-08-01"
)

func TestLintOnShippedExamplesExitsZero(t *testing.T) {
	out, _, code := exec(t, "lint", "-as-of", demoDate, facilities)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "0 error(s), 0 warning(s)") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}

func TestLintOnABrokenMatrixExitsTwo(t *testing.T) {
	out, _, code := exec(t, "lint", "-as-of", demoDate, "../../internal/matrix/testdata/broken.yaml")
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "CM001") {
		t.Errorf("output does not quote rule codes:\n%s", out)
	}
}

func TestLintStrictPromotesWarnings(t *testing.T) {
	// The example matrices are clean today but stale a long way in the
	// future; -strict must turn that hygiene warning into a failure.
	stale := "2030-01-01"
	if _, _, code := exec(t, "lint", "-as-of", stale, facilities); code != 0 {
		t.Fatalf("exit %d, want 0 without -strict", code)
	}
	out, _, code := exec(t, "lint", "-as-of", stale, "-strict", facilities)
	if code != 2 {
		t.Fatalf("exit %d, want 2 with -strict:\n%s", code, out)
	}
}

func TestBoardShowsEveryPlacementSortedByStart(t *testing.T) {
	out, _, code := exec(t, "board", "-matrix", facilities, "-placements", placements, "-as-of", demoDate)
	if code != 2 {
		t.Fatalf("exit %d, want 2 while red placements exist:\n%s", code, out)
	}
	if !strings.Contains(out, "4 placement(s): 2 red, 1 amber, 1 green") {
		t.Errorf("unexpected tally:\n%s", out)
	}
	order := []string{"PL-1042", "PL-1045", "PL-1043", "PL-1044"}
	at := -1
	for _, id := range order {
		i := strings.Index(out, id)
		if i < 0 {
			t.Fatalf("%s missing from the board:\n%s", id, out)
		}
		if i < at {
			t.Errorf("%s is out of start-date order:\n%s", id, out)
		}
		at = i
	}
}

func TestCheckJSONIsMachineReadable(t *testing.T) {
	out, _, code := exec(t, "check", "-matrix", facilities, "-placements", placements,
		"-as-of", demoDate, "-json")
	if code != 2 {
		t.Fatalf("exit %d, want 2:\n%s", code, out)
	}
	var board struct {
		AsOf       string `json:"as_of"`
		Placements []struct {
			PlacementID string `json:"placement_id"`
			Status      string `json:"status"`
			DaysToStart int    `json:"days_to_start"`
			Items       []struct {
				Code     string `json:"code"`
				Status   string `json:"status"`
				Severity string `json:"severity"`
			} `json:"items"`
		} `json:"placements"`
	}
	if err := json.Unmarshal([]byte(out), &board); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if board.AsOf != demoDate {
		t.Errorf("as_of = %q, want %q", board.AsOf, demoDate)
	}
	if len(board.Placements) != 4 {
		t.Fatalf("%d placements in JSON, want 4", len(board.Placements))
	}
	first := board.Placements[0]
	if first.PlacementID != "PL-1042" || first.Status != "red" || first.DaysToStart != 4 {
		t.Errorf("first placement = %+v", first)
	}
	var short bool
	for _, it := range first.Items {
		if it.Code == "bls-aha" && it.Status == "short" && it.Severity == "critical" {
			short = true
		}
	}
	if !short {
		t.Errorf("bls-aha is not reported as a critical short-coverage item: %+v", first.Items)
	}
}

func TestChaseCommitThenRerunIsQuiet(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	args := []string{"chase", "-matrix", facilities, "-placements", placements,
		"-as-of", demoDate, "-ledger", ledger}

	out, _, code := exec(t, append(args, "-commit")...)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "7 action(s) due, 0 suppressed") {
		t.Fatalf("unexpected first run:\n%s", out)
	}

	out, _, code = exec(t, args...)
	if code != 0 {
		t.Fatalf("exit %d on rerun:\n%s", code, out)
	}
	if !strings.Contains(out, "0 action(s) due, 7 suppressed as already sent") {
		t.Errorf("the ledger did not suppress the rerun:\n%s", out)
	}
}

func TestAuditExportsTheLedgerAsCSV(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	if _, _, code := exec(t, "chase", "-matrix", facilities, "-placements", placements,
		"-as-of", demoDate, "-ledger", ledger, "-commit"); code != 0 {
		t.Fatalf("chase -commit exit %d", code)
	}

	out, _, code := exec(t, "audit", "-ledger", ledger)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 8 { // header plus seven actions
		t.Fatalf("%d CSV lines, want 8:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "sent_on,placement_id,nurse,facility,channel") {
		t.Errorf("unexpected header: %q", lines[0])
	}
}

func TestChaseCommitWithoutALedgerIsRefused(t *testing.T) {
	_, _, code := exec(t, "chase", "-matrix", facilities, "-placements", placements,
		"-as-of", demoDate, "-commit")
	if code != 1 {
		t.Errorf("exit %d, want 1: committing with nowhere to record is a mistake, not a no-op", code)
	}
}

func TestBadAsOfDateIsRejected(t *testing.T) {
	if _, _, code := exec(t, "board", "-matrix", facilities, "-placements", placements,
		"-as-of", "01/08/2026"); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	_, errOut, code := exec(t, "chas")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "usage:") {
		t.Errorf("no usage on stderr:\n%s", errOut)
	}
}

func TestDocsDirTurnsAMissingFileIntoARedPlacement(t *testing.T) {
	raman := filepath.Join(placements, "pl-1044-raman.yaml")
	dir := t.TempDir()
	for _, f := range []string{
		"raman-rn-license.pdf", "raman-bls.pdf", "raman-acls.pdf", "raman-fit-test.pdf",
		"raman-tb.pdf", "raman-flu.pdf", "raman-hipaa.pdf", "raman-orientation.pdf",
	} { // raman-id.pdf deliberately left out
		if err := os.WriteFile(filepath.Join(dir, f), []byte("%PDF-1.7"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Without -docs-dir the green placement stays green.
	if out, _, code := exec(t, "board", "-matrix", facilities, "-placements", raman, "-as-of", demoDate); code != 0 {
		t.Fatalf("exit %d without -docs-dir, want 0:\n%s", code, out)
	}

	out, _, code := exec(t, "check", "-matrix", facilities, "-placements", raman, "-as-of", demoDate, "-docs-dir", dir, "-json")
	if code != 2 {
		t.Fatalf("exit %d with a missing file, want 2:\n%s", code, out)
	}
	var board struct {
		Placements []struct {
			Status string `json:"status"`
			Items  []struct {
				Code        string `json:"code"`
				Status      string `json:"status"`
				FileProblem string `json:"file_problem"`
			} `json:"items"`
		} `json:"placements"`
	}
	if err := json.Unmarshal([]byte(out), &board); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	pl := board.Placements[0]
	if pl.Status != "red" {
		t.Errorf("status = %s, want red", pl.Status)
	}
	for _, it := range pl.Items {
		switch {
		case it.Code == "photo-id":
			if it.Status != "unusable" || !strings.Contains(it.FileProblem, "raman-id.pdf") {
				t.Errorf("photo-id = %+v, want unusable naming raman-id.pdf", it)
			}
		case it.FileProblem != "":
			t.Errorf("%s has a file problem but its file exists: %q", it.Code, it.FileProblem)
		}
	}

	out, _, _ = exec(t, "board", "-matrix", facilities, "-placements", raman, "-as-of", demoDate, "-docs-dir", dir)
	if !strings.Contains(out, "RED") || !strings.Contains(out, "photo-id") {
		t.Errorf("board does not show the unusable photo-id:\n%s", out)
	}
}

func TestDocsDirThatDoesNotExistIsAnInputError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	_, errOut, code := exec(t, "board", "-matrix", facilities, "-placements", placements,
		"-as-of", demoDate, "-docs-dir", missing)
	if code != 1 {
		t.Fatalf("exit %d, want 1 for a -docs-dir that does not exist", code)
	}
	if !strings.Contains(errOut, "-docs-dir") {
		t.Errorf("error does not name the flag:\n%s", errOut)
	}
}
