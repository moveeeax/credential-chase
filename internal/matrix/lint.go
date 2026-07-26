package matrix

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/moveeeax/credential-chase/internal/civil"
)

// Severity classifies a lint finding.
type Severity string

// Lint severities. Errors block; warnings are hygiene.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// StaleAfterDays is how old an `updated` date may get before the matrix is
// flagged. Facilities change their packets roughly annually.
const StaleAfterDays = 365

// Finding is one lint result.
type Finding struct {
	Path     string   `json:"path"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Field    string   `json:"field"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: %s [%s] %s", f.Path, f.Severity, f.Field, f.Rule, f.Message)
}

// Report is the outcome of linting one or more matrices.
type Report struct {
	Findings []Finding `json:"findings"`
	Checked  int       `json:"checked"`
}

// Errors returns the number of error-severity findings.
func (r Report) Errors() int { return r.count(Error) }

// Warnings returns the number of warning-severity findings.
func (r Report) Warnings() int { return r.count(Warning) }

func (r Report) count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// OK reports whether the matrices are safe to run the engine against.
func (r Report) OK() bool { return r.Errors() == 0 }

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Lint checks one matrix against the schema rules. `today` anchors the
// staleness rule so the result is deterministic in tests.
//
// Rule codes are stable; they are quoted in review comments and in the
// pilot customer's own matrix pull requests.
func Lint(m *Matrix, today civil.Date) []Finding {
	var fs []Finding
	add := func(rule string, sev Severity, field, format string, args ...any) {
		fs = append(fs, Finding{
			Path:     m.Path,
			Rule:     rule,
			Severity: sev,
			Field:    field,
			Message:  fmt.Sprintf(format, args...),
		})
	}

	if m.SchemaVersion != SchemaVersion {
		add("CM001", Error, "schema_version",
			"schema_version %d is not supported, want %d", m.SchemaVersion, SchemaVersion)
	}

	switch {
	case m.Facility.ID == "":
		add("CM002", Error, "facility.id", "facility id is required")
	case !slugRE.MatchString(m.Facility.ID):
		add("CM003", Error, "facility.id",
			"facility id %q must be a lowercase slug, e.g. st-marys-rochester", m.Facility.ID)
	}
	if strings.TrimSpace(m.Facility.Name) == "" {
		add("CM002", Error, "facility.name", "facility name is required")
	}

	lintIntake(m.Facility.Intake, add)

	if len(m.Requirements) == 0 {
		add("CM004", Error, "requirements", "a matrix with no requirements chases nothing")
	}

	seen := map[string]int{}
	for i, r := range m.Requirements {
		field := fmt.Sprintf("requirements[%d]", i)
		lintRequirement(r, field, add)
		if r.Code != "" {
			if prev, dup := seen[r.Code]; dup {
				add("CM005", Error, field+".code",
					"duplicate requirement code %q, first seen at requirements[%d]", r.Code, prev)
			}
			seen[r.Code] = i
		}
	}

	if m.Updated.IsZero() {
		add("CM014", Warning, "updated",
			"no updated date; a matrix nobody dates is a matrix nobody re-checks")
	} else if age := m.Updated.DaysUntil(today); age > StaleAfterDays {
		add("CM014", Warning, "updated",
			"last updated %s (%d days ago); re-confirm the packet with the facility", m.Updated, age)
	}
	if strings.TrimSpace(m.Source) == "" {
		add("CM015", Warning, "source",
			"no source; record where the requirement list came from and when")
	}

	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Rule < fs[j].Rule })
	return fs
}

type addFunc func(rule string, sev Severity, field, format string, args ...any)

func lintIntake(in Intake, add addFunc) {
	if in.Method == "" {
		add("CM012", Error, "facility.intake.method",
			"intake method is required, one of %s", strings.Join(IntakeMethods, ", "))
		return
	}
	if !contains(IntakeMethods, in.Method) {
		add("CM012", Error, "facility.intake.method",
			"unknown intake method %q, want one of %s", in.Method, strings.Join(IntakeMethods, ", "))
		return
	}
	if in.Method == "fax" && strings.TrimSpace(in.Fax) == "" {
		add("CM013", Error, "facility.intake.fax", "fax intake needs a fax number")
	}
	if in.Method == "email" && strings.TrimSpace(in.Email) == "" {
		add("CM013", Error, "facility.intake.email", "email intake needs an address")
	}
}

func lintRequirement(r Requirement, field string, add addFunc) {
	switch {
	case r.Code == "":
		add("CM006", Error, field+".code", "requirement code is required")
	case !slugRE.MatchString(r.Code):
		add("CM006", Error, field+".code",
			"requirement code %q must be a lowercase slug, e.g. bls-aha", r.Code)
	}
	if strings.TrimSpace(r.Name) == "" {
		add("CM007", Error, field+".name", "requirement name is required")
	}
	if r.Category == "" {
		add("CM008", Error, field+".category",
			"category is required, one of %s", joinCategories())
	} else if !containsCategory(Categories, r.Category) {
		add("CM008", Error, field+".category",
			"unknown category %q, want one of %s", r.Category, joinCategories())
	}
	if !r.Expires && r.LeadDays != 0 {
		add("CM009", Error, field+".lead_days",
			"lead_days is meaningless when expires is false")
	}
	if r.LeadDays < 0 {
		add("CM009", Error, field+".lead_days", "lead_days must not be negative")
	}
	for _, d := range r.AppliesTo.Disciplines {
		if !contains(Disciplines, d) {
			add("CM010", Error, field+".applies_to.disciplines",
				"unknown discipline %q, want one of %s", d, strings.Join(Disciplines, ", "))
		}
	}
	for _, f := range r.Accepts {
		if !contains(Formats, f) {
			add("CM011", Error, field+".accepts",
				"unknown file format %q, want one of %s", f, strings.Join(Formats, ", "))
		}
	}
}

func joinCategories() string {
	out := make([]string, len(Categories))
	for i, c := range Categories {
		out[i] = string(c)
	}
	return strings.Join(out, ", ")
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsCategory(list []Category, v Category) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// LintPaths parses and lints every matrix file under the given paths. Parse
// failures become CM000 findings so one broken file does not hide the rest.
func LintPaths(today civil.Date, paths ...string) (Report, error) {
	files, err := collect(paths)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Findings: []Finding{}}
	for _, path := range files {
		rep.Checked++
		m, err := ParseFile(path)
		if err != nil {
			rep.Findings = append(rep.Findings, Finding{
				Path:     path,
				Rule:     "CM000",
				Severity: Error,
				Field:    "-",
				Message:  cleanYAMLError(err),
			})
			continue
		}
		rep.Findings = append(rep.Findings, Lint(m, today)...)
	}
	return rep, nil
}

func cleanYAMLError(err error) string {
	s := err.Error()
	s = strings.ReplaceAll(s, "\n", "; ")
	return strings.TrimSpace(s)
}
