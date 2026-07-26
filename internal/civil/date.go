// Package civil provides a calendar date without a time or a zone.
//
// Credentialing dates are civil dates: a BLS card expires on a day, an
// assignment starts on a day. Carrying a wall clock and a location around
// with them only creates off-by-one bugs, so this type deliberately has
// neither.
package civil

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Layout is the only date format accepted anywhere in credential-chase.
const Layout = "2006-01-02"

// Date is a year-month-day with no time and no zone.
type Date struct {
	t time.Time
}

// New builds a Date from its parts.
func New(year int, month time.Month, day int) Date {
	return Date{t: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// Parse reads a date in Layout form.
func Parse(s string) (Date, error) {
	t, err := time.Parse(Layout, s)
	if err != nil {
		return Date{}, fmt.Errorf("date %q: want YYYY-MM-DD", s)
	}
	return Date{t: t.UTC()}, nil
}

// MustParse is Parse for fixtures and tests; it panics on bad input.
func MustParse(s string) Date {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Today returns the current date in UTC.
func Today() Date {
	n := time.Now().UTC()
	return New(n.Year(), n.Month(), n.Day())
}

// IsZero reports whether the date was never set.
func (d Date) IsZero() bool { return d.t.IsZero() }

func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.t.Format(Layout)
}

// AddDays returns the date n days later; n may be negative.
func (d Date) AddDays(n int) Date { return Date{t: d.t.AddDate(0, 0, n)} }

// Before reports whether d falls strictly before o.
func (d Date) Before(o Date) bool { return d.t.Before(o.t) }

// After reports whether d falls strictly after o.
func (d Date) After(o Date) bool { return d.t.After(o.t) }

// Equal reports whether the two dates are the same day.
func (d Date) Equal(o Date) bool { return d.t.Equal(o.t) }

// DaysUntil returns the whole days from d to o. Negative when o is earlier.
func (d Date) DaysUntil(o Date) int {
	return int(o.t.Sub(d.t).Hours() / 24)
}

// UnmarshalYAML decodes a scalar node such as 2026-09-01.
func (d *Date) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: date must be a YYYY-MM-DD string", node.Line)
	}
	p, err := Parse(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = p
	return nil
}

// MarshalYAML writes the date back as a plain string.
func (d Date) MarshalYAML() (any, error) { return d.String(), nil }

// MarshalJSON writes the date as a quoted YYYY-MM-DD string, or null.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON reads a quoted YYYY-MM-DD string or null.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s *string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == nil || *s == "" {
		*d = Date{}
		return nil
	}
	p, err := Parse(*s)
	if err != nil {
		return err
	}
	*d = p
	return nil
}
