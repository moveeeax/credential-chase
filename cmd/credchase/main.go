// Command credchase checks travel-nurse placements against facility
// requirement matrices, and works out who has to be chased today.
//
// It reads YAML, writes text or JSON, and sends nothing. Delivery — SMS,
// email, fax — is a separate credentialed layer.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moveeeax/credential-chase/internal/chase"
	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/engine"
	"github.com/moveeeax/credential-chase/internal/matrix"
	"github.com/moveeeax/credential-chase/internal/placement"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `credchase — credential chase for travel-nurse placements

usage:
  credchase lint   <matrix-path>...                     validate facility requirement matrices
  credchase check  -matrix DIR -placements DIR          per-placement missing/expiring report
  credchase board  -matrix DIR -placements DIR          one-screen red/amber/green board
  credchase chase  -matrix DIR -placements DIR          escalations due today, deduped
  credchase audit  -ledger FILE                         export the chase ledger as CSV
  credchase version

common flags:
  -as-of YYYY-MM-DD   evaluate as if today were this date (default: today, UTC)
  -json               machine-readable output

exit codes:
  0  clean          1  usage or input error          2  findings (lint errors / red placements)
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			os.Exit(int(ec))
		}
		fmt.Fprintln(os.Stderr, "credchase:", err)
		os.Exit(1)
	}
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitCode(1)
	}
	switch args[0] {
	case "lint":
		return cmdLint(args[1:], stdout, stderr)
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "board":
		return cmdBoard(args[1:], stdout, stderr)
	case "chase":
		return cmdChase(args[1:], stdout, stderr)
	case "audit":
		return cmdAudit(args[1:], stdout)
	case "version":
		fmt.Fprintln(stdout, "credchase", version)
		return nil
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprintf(stderr, "credchase: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return exitCode(1)
	}
}

// inputs are the flags every placement-reading command shares.
type inputs struct {
	matrixPaths    string
	placementPaths string
	asOf           string
	criticalDays   int
	jsonOut        bool
}

func (in *inputs) bind(fs *flag.FlagSet, withPlacements bool) {
	fs.StringVar(&in.matrixPaths, "matrix", "examples/facilities", "facility matrix file or directory (comma separated)")
	if withPlacements {
		fs.StringVar(&in.placementPaths, "placements", "examples/placements", "placement file or directory (comma separated)")
	}
	fs.StringVar(&in.asOf, "as-of", "", "evaluate as if today were this date (YYYY-MM-DD)")
	fs.IntVar(&in.criticalDays, "critical-days", engine.DefaultCriticalDays, "runway at or below which an open item is critical")
	fs.BoolVar(&in.jsonOut, "json", false, "emit JSON")
}

func (in *inputs) date() (civil.Date, error) {
	if in.asOf == "" {
		return civil.Today(), nil
	}
	return civil.Parse(in.asOf)
}

func (in *inputs) board() (engine.Board, []*placement.Placement, error) {
	asOf, err := in.date()
	if err != nil {
		return engine.Board{}, nil, err
	}
	set, err := matrix.Load(split(in.matrixPaths)...)
	if err != nil {
		return engine.Board{}, nil, err
	}
	ps, err := placement.Load(split(in.placementPaths)...)
	if err != nil {
		return engine.Board{}, nil, err
	}
	b, err := engine.EvaluateAll(ps, set, engine.Options{AsOf: asOf, CriticalDays: in.criticalDays})
	return b, ps, err
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdLint(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asOf := fs.String("as-of", "", "evaluate staleness as if today were this date (YYYY-MM-DD)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	strict := fs.Bool("strict", false, "treat warnings as errors")
	if err := fs.Parse(args); err != nil {
		return exitCode(1)
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"examples/facilities"}
	}
	today := civil.Today()
	if *asOf != "" {
		d, err := civil.Parse(*asOf)
		if err != nil {
			return err
		}
		today = d
	}

	rep, err := matrix.LintPaths(today, paths...)
	if err != nil {
		return err
	}
	if *jsonOut {
		if err := writeJSON(stdout, rep); err != nil {
			return err
		}
	} else {
		for _, f := range rep.Findings {
			fmt.Fprintln(stdout, f)
		}
		fmt.Fprintf(stdout, "%d matrix file(s) checked: %d error(s), %d warning(s)\n",
			rep.Checked, rep.Errors(), rep.Warnings())
	}
	if rep.Errors() > 0 || (*strict && rep.Warnings() > 0) {
		return exitCode(2)
	}
	return nil
}

func cmdCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in inputs
	in.bind(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitCode(1)
	}
	b, _, err := in.board()
	if err != nil {
		return err
	}
	if in.jsonOut {
		if err := writeJSON(stdout, b); err != nil {
			return err
		}
		return redExit(b)
	}
	for _, res := range b.Results {
		fmt.Fprintf(stdout, "\n%s  %s — %s at %s\n", badge(res.Status), res.PlacementID, res.Nurse, res.Facility)
		fmt.Fprintf(stdout, "  starts %s (T-%d), %d critical / %d warn / %d ok\n",
			res.StartDate, res.DaysToStart, res.Critical, res.Warn, res.OK)
		for _, it := range res.Items {
			fmt.Fprintf(stdout, "  %-8s %-22s %-8s %s\n", it.Severity, it.Code, it.Status, it.Detail)
		}
		for _, o := range res.Orphans {
			fmt.Fprintf(stdout, "  %-8s %-22s %-8s not in the facility matrix\n", "warn", o, "orphan")
		}
	}
	fmt.Fprintln(stdout)
	return redExit(b)
}

func cmdBoard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("board", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in inputs
	in.bind(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitCode(1)
	}
	b, _, err := in.board()
	if err != nil {
		return err
	}
	if in.jsonOut {
		if err := writeJSON(stdout, b); err != nil {
			return err
		}
		return redExit(b)
	}
	fmt.Fprintf(stdout, "coordinator board — as of %s\n\n", b.AsOf)
	fmt.Fprintf(stdout, "%-7s %-10s %-12s %-24s %-6s %-22s %s\n",
		"STATUS", "START", "PLACEMENT", "NURSE", "T-", "FACILITY", "OPEN")
	for _, res := range b.Results {
		open := res.OpenItems()
		codes := make([]string, 0, len(open))
		for _, it := range open {
			codes = append(codes, it.Code)
		}
		fmt.Fprintf(stdout, "%-7s %-10s %-12s %-24s %-6d %-22s %s\n",
			badge(res.Status), res.StartDate, res.PlacementID, trunc(res.Nurse, 24),
			res.DaysToStart, trunc(res.Facility, 22), strings.Join(codes, ","))
	}
	red, amber, green := tally(b)
	fmt.Fprintf(stdout, "\n%d placement(s): %d red, %d amber, %d green\n", len(b.Results), red, amber, green)
	return redExit(b)
}

func cmdChase(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("chase", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in inputs
	in.bind(fs, true)
	ledgerPath := fs.String("ledger", "", "chase ledger for de-duplication (JSON, created if absent)")
	commit := fs.Bool("commit", false, "record the planned actions in the ledger")
	if err := fs.Parse(args); err != nil {
		return exitCode(1)
	}
	b, ps, err := in.board()
	if err != nil {
		return err
	}
	ledger := chase.NewLedger()
	if *ledgerPath != "" {
		ledger, err = chase.LoadLedger(*ledgerPath)
		if err != nil {
			return err
		}
	}
	plan := chase.Schedule(b, ps, ledger)

	if in.jsonOut {
		if err := writeJSON(stdout, plan); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "chase plan — as of %s\n", plan.AsOf)
		if len(plan.Actions) == 0 {
			fmt.Fprintln(stdout, "nothing due")
		}
		for _, a := range plan.Actions {
			fmt.Fprintf(stdout, "\n[%s] %s -> %s (T-%d)\n", a.Channel, a.Key, a.Recipient, a.Window)
			fmt.Fprintf(stdout, "  subject: %s\n", a.Subject)
			for _, line := range strings.Split(strings.TrimRight(a.Body, "\n"), "\n") {
				fmt.Fprintf(stdout, "  | %s\n", line)
			}
		}
		fmt.Fprintf(stdout, "\n%d action(s) due, %d suppressed as already sent\n",
			len(plan.Actions), len(plan.Suppressed))
	}

	if *commit {
		if *ledgerPath == "" {
			return errors.New("-commit needs -ledger")
		}
		for _, a := range plan.Actions {
			ledger.Record(a)
		}
		if err := chase.SaveLedger(*ledgerPath, ledger); err != nil {
			return err
		}
	}
	return nil
}

func cmdAudit(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	ledgerPath := fs.String("ledger", "", "chase ledger to export (JSON)")
	if err := fs.Parse(args); err != nil {
		return exitCode(1)
	}
	if *ledgerPath == "" {
		return errors.New("audit needs -ledger")
	}
	ledger, err := chase.LoadLedger(*ledgerPath)
	if err != nil {
		return err
	}
	return chase.WriteAuditCSV(stdout, ledger)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func redExit(b engine.Board) error {
	for _, res := range b.Results {
		if res.Status == engine.Red {
			return exitCode(2)
		}
	}
	return nil
}

func tally(b engine.Board) (red, amber, green int) {
	for _, res := range b.Results {
		switch res.Status {
		case engine.Red:
			red++
		case engine.Amber:
			amber++
		default:
			green++
		}
	}
	return
}

func badge(s engine.RAG) string { return strings.ToUpper(string(s)) }

// trunc shortens to n runes, not n bytes: facility names carry accents and
// a byte slice through the middle of a rune renders as garbage.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n <= 0 {
		return s
	}
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}
