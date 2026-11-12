// Package engine evaluates placements against facility requirement
// matrices and produces the missing/expiring picture the coordinator board
// and the chase scheduler both read.
//
// The warn/critical model is deliberately the same shape as certcheck: a
// gap is a warning while there is still runway to fix it, and critical once
// there is not. The only difference is the date source — a facility packet
// instead of an x509 NotAfter.
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/matrix"
	"github.com/moveeeax/credential-chase/internal/placement"
)

// Status is the state of one requirement on one placement.
type Status string

// Requirement states, worst to best.
const (
	// StatusMissing means nothing has been filed at all.
	StatusMissing Status = "missing"
	// StatusExpired means the document on file is already out of date.
	StatusExpired Status = "expired"
	// StatusUnusable means a document is recorded but the file behind it
	// would be rejected: it is missing, empty, or in a format the facility
	// does not accept. Always critical — the record looks complete, so
	// nobody would otherwise notice until the facility bounces the packet.
	StatusUnusable Status = "unusable"
	// StatusShort means the document is valid today but lapses before it
	// has covered the assignment for lead_days past the start date.
	StatusShort Status = "short"
	// StatusWaived means the facility excused the requirement in writing.
	StatusWaived Status = "waived"
	// StatusOK means the document is on file and covers the assignment.
	StatusOK Status = "ok"
)

// Severity is the escalation level of a single item.
type Severity string

// Item severities.
const (
	SeverityCritical Severity = "critical"
	SeverityWarn     Severity = "warn"
	SeverityOK       Severity = "ok"
)

// RAG is the single colour the coordinator board shows per placement.
type RAG string

// Board colours.
const (
	Red   RAG = "red"
	Amber RAG = "amber"
	Green RAG = "green"
)

// DefaultCriticalDays is the runway at or below which an open gap stops
// being a warning. Three weeks of chasing has already failed by then, and
// the coordinator has to pick up the phone.
const DefaultCriticalDays = 7

// Options tunes the evaluation.
type Options struct {
	// AsOf is the date the evaluation is run for.
	AsOf civil.Date
	// CriticalDays is the runway at or below which gaps become critical.
	CriticalDays int
	// DocsDir is where the files named by `file:` live. When set, each
	// referenced file must exist and be non-empty. When empty, only the
	// file extension is checked against the requirement's `accepts`.
	DocsDir string
}

func (o Options) criticalDays() int {
	if o.CriticalDays <= 0 {
		return DefaultCriticalDays
	}
	return o.CriticalDays
}

// Item is one requirement evaluated against one placement.
type Item struct {
	Code     string          `json:"code"`
	Name     string          `json:"name"`
	Category matrix.Category `json:"category"`
	Status   Status          `json:"status"`
	Severity Severity        `json:"severity"`
	Received civil.Date      `json:"received"`
	Expires  civil.Date      `json:"expires"`
	// CoverageDays is how many days past the start date the document
	// stays valid. Nil when nothing is on file or nothing expires.
	CoverageDays *int   `json:"coverage_days"`
	RequiredDays int    `json:"required_days"`
	Detail       string `json:"detail"`
	// FileProblem says why the referenced file would be rejected. Empty
	// when the file is fine or was not checked.
	FileProblem string `json:"file_problem"`
}

// Open reports whether the item still needs work.
func (i Item) Open() bool { return i.Severity != SeverityOK }

// Result is one placement's full picture.
type Result struct {
	PlacementID string     `json:"placement_id"`
	Nurse       string     `json:"nurse"`
	Discipline  string     `json:"discipline"`
	FacilityID  string     `json:"facility_id"`
	Facility    string     `json:"facility"`
	Unit        string     `json:"unit,omitempty"`
	Coordinator string     `json:"coordinator"`
	StartDate   civil.Date `json:"start_date"`
	AsOf        civil.Date `json:"as_of"`
	DaysToStart int        `json:"days_to_start"`
	Status      RAG        `json:"status"`
	Critical    int        `json:"critical"`
	Warn        int        `json:"warn"`
	OK          int        `json:"ok"`
	Items       []Item     `json:"items"`
	// Orphans are documents filed against requirement codes the facility
	// matrix does not define. Usually a typo, occasionally a matrix that
	// has drifted behind the facility's actual packet.
	Orphans []string `json:"orphans,omitempty"`
}

// OpenItems returns every item that still needs work, worst first.
func (r Result) OpenItems() []Item {
	var out []Item
	for _, it := range r.Items {
		if it.Open() {
			out = append(out, it)
		}
	}
	return out
}

// Evaluate scores one placement against its facility matrix.
func Evaluate(p *placement.Placement, m *matrix.Matrix, opts Options) Result {
	asOf := opts.AsOf
	if asOf.IsZero() {
		asOf = civil.Today()
	}
	res := Result{
		PlacementID: p.ID,
		Nurse:       p.Nurse.Name,
		Discipline:  p.Nurse.Discipline,
		FacilityID:  p.Facility,
		Facility:    m.Facility.Name,
		Unit:        p.Unit,
		Coordinator: p.Coordinator.Email,
		StartDate:   p.StartDate,
		AsOf:        asOf,
		DaysToStart: asOf.DaysUntil(p.StartDate),
		Items:       []Item{},
	}

	known := map[string]bool{}
	for _, req := range m.Requirements {
		known[req.Code] = true
		if !req.Applies(p.Nurse.Discipline, p.Unit) {
			continue
		}
		res.Items = append(res.Items, evaluateOne(req, p, res.DaysToStart, asOf, opts.criticalDays(), opts.DocsDir))
	}

	for _, d := range p.Documents {
		if !known[d.Requirement] {
			res.Orphans = append(res.Orphans, d.Requirement)
		}
	}
	sort.Strings(res.Orphans)

	for _, it := range res.Items {
		switch it.Severity {
		case SeverityCritical:
			res.Critical++
		case SeverityWarn:
			res.Warn++
		default:
			res.OK++
		}
	}
	res.Status = rag(res.Critical, res.Warn)

	sort.SliceStable(res.Items, func(i, j int) bool {
		a, b := res.Items[i], res.Items[j]
		if severityRank(a.Severity) != severityRank(b.Severity) {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		return a.Code < b.Code
	})
	return res
}

func evaluateOne(req matrix.Requirement, p *placement.Placement, daysToStart int, asOf civil.Date, criticalDays int, docsDir string) Item {
	it := evaluateDates(req, p, daysToStart, asOf, criticalDays)
	if it.Status == StatusWaived {
		return it
	}
	doc, ok := p.Document(req.Code)
	if !ok {
		return it
	}
	it.FileProblem = fileProblem(doc.File, req.Accepts, docsDir)
	if it.FileProblem == "" {
		return it
	}
	// An expired document stays expired: that is the bigger problem and
	// the one the nurse has to fix first. Anything else on file is
	// overridden, because a record that looks fine but cannot be sent is
	// exactly the gap this check exists to catch.
	if it.Status != StatusExpired {
		it.Status = StatusUnusable
		it.Severity = SeverityCritical
		it.Detail = it.FileProblem
	}
	return it
}

// fileProblem checks the file a document record points at. An empty name
// means nothing was recorded, which is not checked: `file:` is optional.
func fileProblem(name string, accepts []string, docsDir string) string {
	if name == "" {
		return ""
	}
	if len(accepts) > 0 {
		ext := normalizeExt(filepath.Ext(name))
		if !acceptsExt(accepts, ext) {
			got := ext
			if got == "" {
				got = "no extension"
			}
			return fmt.Sprintf("file %s is %s, facility accepts %s",
				name, got, strings.Join(accepts, ", "))
		}
	}
	if docsDir == "" {
		return ""
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(docsDir, name)
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("file %s not found in %s", name, docsDir)
	case err != nil:
		return fmt.Sprintf("file %s cannot be read: %v", name, err)
	case !info.Mode().IsRegular():
		return fmt.Sprintf("file %s is not a regular file", name)
	case info.Size() == 0:
		return fmt.Sprintf("file %s is empty", name)
	}
	return ""
}

// normalizeExt lowercases an extension, drops the dot and folds the
// common `.jpeg` spelling into the `jpg` format the matrix uses.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext == "jpeg" {
		return "jpg"
	}
	return ext
}

func acceptsExt(accepts []string, ext string) bool {
	for _, a := range accepts {
		if strings.EqualFold(a, ext) {
			return true
		}
	}
	return false
}

func evaluateDates(req matrix.Requirement, p *placement.Placement, daysToStart int, asOf civil.Date, criticalDays int) Item {
	it := Item{
		Code:         req.Code,
		Name:         req.Name,
		Category:     req.Category,
		RequiredDays: req.LeadDays,
	}

	doc, ok := p.Document(req.Code)
	if !ok {
		it.Status = StatusMissing
		it.Severity = gapSeverity(daysToStart, criticalDays)
		it.Detail = "nothing on file"
		return it
	}

	it.Received = doc.Received
	it.Expires = doc.Expires

	if doc.Waived {
		it.Status = StatusWaived
		it.Severity = SeverityOK
		it.Detail = "waived by facility: " + doc.WaiverNote
		return it
	}

	if !req.Expires || doc.Expires.IsZero() {
		it.Status = StatusOK
		it.Detail = "on file since " + doc.Received.String()
		it.Severity = SeverityOK
		if req.Expires && doc.Expires.IsZero() {
			// The matrix says this document expires but the record
			// carries no date. That is a gap, not a pass.
			it.Status = StatusMissing
			it.Severity = gapSeverity(daysToStart, criticalDays)
			it.Detail = "on file but no expiry date recorded"
		}
		return it
	}

	// Coverage is measured from the start date, not from today: a card
	// that is valid now but lapses in week two of a 13-week assignment is
	// still a problem the facility will bounce.
	coverage := p.StartDate.DaysUntil(doc.Expires)
	it.CoverageDays = &coverage

	switch {
	case !doc.Expires.After(asOf):
		it.Status = StatusExpired
		it.Severity = SeverityCritical
		it.Detail = fmt.Sprintf("expired %s", doc.Expires)
	case coverage < req.LeadDays:
		it.Status = StatusShort
		it.Severity = gapSeverity(daysToStart, criticalDays)
		it.Detail = fmt.Sprintf("expires %s, %s past start, facility wants %d",
			doc.Expires, days(coverage), req.LeadDays)
	default:
		it.Status = StatusOK
		it.Severity = SeverityOK
		it.Detail = fmt.Sprintf("valid to %s, %s past start", doc.Expires, days(coverage))
	}
	return it
}

func days(n int) string {
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d day", n)
	}
	return fmt.Sprintf("%d days", n)
}

// gapSeverity turns runway into a severity. Once the start date is inside
// the critical window — or already past — an open item is critical.
func gapSeverity(daysToStart, criticalDays int) Severity {
	if daysToStart <= criticalDays {
		return SeverityCritical
	}
	return SeverityWarn
}

func rag(critical, warn int) RAG {
	switch {
	case critical > 0:
		return Red
	case warn > 0:
		return Amber
	default:
		return Green
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarn:
		return 1
	default:
		return 2
	}
}

// Board is the whole coordinator screen: every placement, worst first
// within start-date order.
type Board struct {
	AsOf    civil.Date `json:"as_of"`
	Results []Result   `json:"placements"`
}

// EvaluateAll scores every placement. A placement whose facility has no
// matrix is an error the coordinator must see, not a silent skip.
func EvaluateAll(ps []*placement.Placement, set *matrix.Set, opts Options) (Board, error) {
	asOf := opts.AsOf
	if asOf.IsZero() {
		asOf = civil.Today()
	}
	opts.AsOf = asOf

	b := Board{AsOf: asOf, Results: []Result{}}
	for _, p := range ps {
		m, ok := set.Get(p.Facility)
		if !ok {
			return Board{}, fmt.Errorf("%s: no requirement matrix for facility %q", p.Path, p.Facility)
		}
		b.Results = append(b.Results, Evaluate(p, m, opts))
	}
	sort.SliceStable(b.Results, func(i, j int) bool {
		a, c := b.Results[i], b.Results[j]
		if a.StartDate.Equal(c.StartDate) {
			return a.PlacementID < c.PlacementID
		}
		return a.StartDate.Before(c.StartDate)
	})
	return b, nil
}
