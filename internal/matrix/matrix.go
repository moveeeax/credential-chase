// Package matrix models the facility requirement matrix: the versioned,
// hand-entered YAML that says which documents a given facility demands
// before it will let a traveller start.
package matrix

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/fileset"
)

// SchemaVersion is the only matrix schema this build understands.
const SchemaVersion = 1

// Category groups requirements for the coordinator board.
type Category string

// Known requirement categories. Anything else is a lint error, because a
// typo'd category silently drops a document out of a board filter.
const (
	CategoryLicense       Category = "license"
	CategoryCertification Category = "certification"
	CategoryHealth        Category = "health"
	CategoryTraining      Category = "training"
	CategoryForm          Category = "form"
	CategoryIdentity      Category = "identity"
)

// Categories lists every known category in board order.
var Categories = []Category{
	CategoryLicense, CategoryCertification, CategoryHealth,
	CategoryTraining, CategoryForm, CategoryIdentity,
}

// Disciplines are the nurse types the pilot customer places.
var Disciplines = []string{"RN", "LPN", "CNA", "RT", "ST"}

// IntakeMethods are the delivery channels a facility will accept.
var IntakeMethods = []string{"fax", "email"}

// Formats are the file types a requirement may accept.
var Formats = []string{"pdf", "jpg", "png"}

// Facility identifies where the packet has to land.
type Facility struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Intake Intake `yaml:"intake"`
}

// Intake is the facility's document intake channel.
type Intake struct {
	Method             string `yaml:"method"`
	Fax                string `yaml:"fax,omitempty"`
	Email              string `yaml:"email,omitempty"`
	Attention          string `yaml:"attention,omitempty"`
	CoverSheetRequired bool   `yaml:"cover_sheet_required"`
}

// AppliesTo narrows a requirement to some placements only.
type AppliesTo struct {
	Disciplines []string `yaml:"disciplines,omitempty"`
	Units       []string `yaml:"units,omitempty"`
}

// Requirement is one line on the facility's checklist.
type Requirement struct {
	Code     string   `yaml:"code"`
	Name     string   `yaml:"name"`
	Category Category `yaml:"category"`
	// Expires marks a document that carries an expiry date.
	Expires bool `yaml:"expires"`
	// LeadDays is how far past the start date the document must stay
	// valid. Zero means "valid on the start date".
	LeadDays int `yaml:"lead_days,omitempty"`
	// Waivable documents can be excused by the facility in writing.
	Waivable  bool      `yaml:"waivable,omitempty"`
	AppliesTo AppliesTo `yaml:"applies_to,omitempty"`
	Accepts   []string  `yaml:"accepts,omitempty"`
	Notes     string    `yaml:"notes,omitempty"`
}

// Matrix is one facility's requirement file.
type Matrix struct {
	SchemaVersion int           `yaml:"schema_version"`
	Facility      Facility      `yaml:"facility"`
	Updated       civil.Date    `yaml:"updated"`
	Source        string        `yaml:"source"`
	Requirements  []Requirement `yaml:"requirements"`

	// Path is where the matrix was read from. Not part of the schema.
	Path string `yaml:"-"`
}

// Applies reports whether a requirement covers a given discipline and unit.
// An empty filter means "everyone".
func (r Requirement) Applies(discipline, unit string) bool {
	if !matchAny(r.AppliesTo.Disciplines, discipline) {
		return false
	}
	return matchAny(r.AppliesTo.Units, unit)
}

func matchAny(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// Set is a loaded collection of matrices, keyed by facility ID.
type Set struct {
	byID map[string]*Matrix
}

// Get returns the matrix for a facility ID.
func (s *Set) Get(id string) (*Matrix, bool) {
	m, ok := s.byID[id]
	return m, ok
}

// IDs returns every loaded facility ID, sorted.
func (s *Set) IDs() []string {
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// All returns every loaded matrix, sorted by facility ID.
func (s *Set) All() []*Matrix {
	out := make([]*Matrix, 0, len(s.byID))
	for _, id := range s.IDs() {
		out = append(out, s.byID[id])
	}
	return out
}

// Len returns the number of loaded matrices.
func (s *Set) Len() int { return len(s.byID) }

// ParseFile decodes a single matrix file. Unknown fields are rejected: a
// misspelled key in a hand-typed requirement file is exactly the failure
// this product exists to prevent.
func ParseFile(path string) (*Matrix, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var m Matrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Path = path
	return &m, nil
}

// Load reads every *.yaml and *.yml file under the given paths. A path may
// be a single file or a directory.
func Load(paths ...string) (*Set, error) {
	set := &Set{byID: map[string]*Matrix{}}
	files, err := collect(paths)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no matrix files found")
	}
	for _, path := range files {
		m, err := ParseFile(path)
		if err != nil {
			return nil, err
		}
		if prev, dup := set.byID[m.Facility.ID]; dup {
			return nil, fmt.Errorf("%s: facility %q already defined in %s",
				path, m.Facility.ID, prev.Path)
		}
		set.byID[m.Facility.ID] = m
	}
	return set, nil
}

func collect(paths []string) ([]string, error) { return fileset.YAML(paths) }
