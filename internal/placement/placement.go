// Package placement models one traveller's assignment: who is going where,
// when they start, and which documents are already on file.
package placement

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

func collect(paths []string) ([]string, error) { return fileset.YAML(paths) }

// SchemaVersion is the only placement schema this build understands.
const SchemaVersion = 1

// Nurse is the traveller. There is no nurse account and no nurse app: a
// phone number and an email address are the whole contact surface.
type Nurse struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Discipline string `yaml:"discipline"`
	Phone      string `yaml:"phone,omitempty"`
	Email      string `yaml:"email,omitempty"`
}

// Coordinator is the agency-side owner who gets the daily digest.
type Coordinator struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// Document is a file the nurse has already sent in.
type Document struct {
	// Requirement is the requirement code from the facility matrix.
	Requirement string     `yaml:"requirement"`
	Received    civil.Date `yaml:"received"`
	Expires     civil.Date `yaml:"expires,omitempty"`
	File        string     `yaml:"file,omitempty"`
	// Waived records a facility waiver, with the note that proves it.
	Waived     bool   `yaml:"waived,omitempty"`
	WaiverNote string `yaml:"waiver_note,omitempty"`
}

// Placement is one assignment record.
type Placement struct {
	SchemaVersion int         `yaml:"schema_version"`
	ID            string      `yaml:"id"`
	Nurse         Nurse       `yaml:"nurse"`
	Facility      string      `yaml:"facility"`
	Unit          string      `yaml:"unit,omitempty"`
	StartDate     civil.Date  `yaml:"start_date"`
	EndDate       civil.Date  `yaml:"end_date,omitempty"`
	Coordinator   Coordinator `yaml:"coordinator"`
	Documents     []Document  `yaml:"documents,omitempty"`

	// Path is where the record was read from. Not part of the schema.
	Path string `yaml:"-"`
}

// Document returns the document filed against a requirement code.
func (p *Placement) Document(code string) (Document, bool) {
	for _, d := range p.Documents {
		if strings.EqualFold(d.Requirement, code) {
			return d, true
		}
	}
	return Document{}, false
}

// Validate checks the parts the engine cannot work without.
func (p *Placement) Validate() error {
	var problems []string
	if p.SchemaVersion != SchemaVersion {
		problems = append(problems,
			fmt.Sprintf("schema_version %d is not supported, want %d", p.SchemaVersion, SchemaVersion))
	}
	if p.ID == "" {
		problems = append(problems, "id is required")
	}
	if p.Facility == "" {
		problems = append(problems, "facility is required")
	}
	if p.StartDate.IsZero() {
		problems = append(problems, "start_date is required")
	}
	if p.Nurse.Name == "" {
		problems = append(problems, "nurse.name is required")
	}
	if p.Coordinator.Email == "" {
		problems = append(problems, "coordinator.email is required for the daily digest")
	}
	for i, d := range p.Documents {
		if d.Requirement == "" {
			problems = append(problems, fmt.Sprintf("documents[%d].requirement is required", i))
		}
		if d.Received.IsZero() && !d.Waived {
			problems = append(problems,
				fmt.Sprintf("documents[%d] (%s) has no received date and is not waived", i, d.Requirement))
		}
		if d.Waived && strings.TrimSpace(d.WaiverNote) == "" {
			problems = append(problems,
				fmt.Sprintf("documents[%d] (%s) is waived without a waiver_note", i, d.Requirement))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", p.Path, strings.Join(problems, "; "))
}

// ParseFile decodes a single placement record, rejecting unknown fields.
func ParseFile(path string) (*Placement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var p Placement
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	return &p, nil
}

// Load reads every placement file under the given paths and validates each
// one. Records come back sorted by start date, which is the order the
// coordinator board wants anyway.
func Load(paths ...string) ([]*Placement, error) {
	files, err := collect(paths)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no placement files found")
	}
	var out []*Placement
	seen := map[string]string{}
	for _, path := range files {
		p, err := ParseFile(path)
		if err != nil {
			return nil, err
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if prev, dup := seen[p.ID]; dup {
			return nil, fmt.Errorf("%s: placement %q already defined in %s", path, p.ID, prev)
		}
		seen[p.ID] = path
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartDate.Equal(out[j].StartDate) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartDate.Before(out[j].StartDate)
	})
	return out, nil
}
