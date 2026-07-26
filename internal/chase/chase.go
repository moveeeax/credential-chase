// Package chase turns the engine's open items into the fixed escalation
// schedule: SMS and email to the nurse at T-21, T-14, T-7, T-3 and T-1, and
// one daily digest to the coordinator.
//
// Nothing here talks to Twilio or to a fax API. It decides what should be
// sent and refuses to decide it twice; sending is a separate, credentialed
// layer.
package chase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/moveeeax/credential-chase/internal/civil"
	"github.com/moveeeax/credential-chase/internal/engine"
	"github.com/moveeeax/credential-chase/internal/placement"
)

// Windows are the escalation thresholds in days before the start date.
// They are fixed on purpose: a per-customer schedule is a support burden
// nobody is paying for yet.
var Windows = []int{21, 14, 7, 3, 1}

// Channel is how an action reaches its recipient.
type Channel string

// Delivery channels.
const (
	ChannelSMS    Channel = "sms"
	ChannelEmail  Channel = "email"
	ChannelDigest Channel = "digest"
)

// Action is one message that is due and has not been sent before.
type Action struct {
	Key         string     `json:"key"`
	Channel     Channel    `json:"channel"`
	Recipient   string     `json:"recipient"`
	PlacementID string     `json:"placement_id,omitempty"`
	Nurse       string     `json:"nurse,omitempty"`
	Facility    string     `json:"facility,omitempty"`
	Window      int        `json:"window"`
	DueOn       civil.Date `json:"due_on"`
	StartDate   civil.Date `json:"start_date,omitempty"`
	Items       []string   `json:"items,omitempty"`
	Subject     string     `json:"subject"`
	Body        string     `json:"body"`
}

// Plan is everything due on one day.
type Plan struct {
	AsOf       civil.Date `json:"as_of"`
	Actions    []Action   `json:"actions"`
	Suppressed []string   `json:"suppressed,omitempty"`
}

// CurrentWindow returns the escalation window a placement sits in, given
// the days remaining until its start date. It returns false before the
// first threshold is crossed.
//
// Once the last threshold is passed the placement stays in the T-1 window:
// a start date that has arrived with documents missing is the loudest state
// the system has, not a state it falls out of.
func CurrentWindow(daysToStart int) (int, bool) {
	current, found := 0, false
	for _, w := range Windows {
		if daysToStart <= w && (!found || w < current) {
			current, found = w, true
		}
	}
	return current, found
}

// Ledger records what has already been sent, so a rerun of the same day
// does not re-text a nurse who is already annoyed.
type Ledger struct {
	Entries []Entry `json:"entries"`

	index map[string]bool
}

// Entry is one immutable line of the audit log.
type Entry struct {
	Key         string     `json:"key"`
	SentOn      civil.Date `json:"sent_on"`
	Channel     Channel    `json:"channel"`
	Recipient   string     `json:"recipient"`
	PlacementID string     `json:"placement_id,omitempty"`
	Nurse       string     `json:"nurse,omitempty"`
	Facility    string     `json:"facility,omitempty"`
	Window      int        `json:"window"`
	Items       []string   `json:"items,omitempty"`
	Subject     string     `json:"subject"`
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger { return &Ledger{Entries: []Entry{}, index: map[string]bool{}} }

// Has reports whether a key has already been recorded.
func (l *Ledger) Has(key string) bool {
	if l == nil {
		return false
	}
	l.reindex()
	return l.index[key]
}

// Record appends an action to the ledger. Recording the same key twice is a
// no-op, which is what makes the ledger safe to drive from cron.
func (l *Ledger) Record(a Action) bool {
	l.reindex()
	if l.index[a.Key] {
		return false
	}
	l.Entries = append(l.Entries, Entry{
		Key:         a.Key,
		SentOn:      a.DueOn,
		Channel:     a.Channel,
		Recipient:   a.Recipient,
		PlacementID: a.PlacementID,
		Nurse:       a.Nurse,
		Facility:    a.Facility,
		Window:      a.Window,
		Items:       a.Items,
		Subject:     a.Subject,
	})
	l.index[a.Key] = true
	return true
}

func (l *Ledger) reindex() {
	if l.index != nil {
		return
	}
	l.index = make(map[string]bool, len(l.Entries))
	for _, e := range l.Entries {
		l.index[e.Key] = true
	}
}

// Schedule works out what is due on the board's as-of date and has not
// already been sent according to the ledger. It does not mutate the ledger;
// the caller commits once delivery has actually happened.
func Schedule(b engine.Board, ps []*placement.Placement, ledger *Ledger) Plan {
	byID := map[string]*placement.Placement{}
	for _, p := range ps {
		byID[p.ID] = p
	}

	plan := Plan{AsOf: b.AsOf, Actions: []Action{}}
	digests := map[string][]engine.Result{}

	for _, res := range b.Results {
		open := res.OpenItems()
		if len(open) == 0 {
			continue
		}
		window, active := CurrentWindow(res.DaysToStart)
		if !active {
			continue
		}
		p := byID[res.PlacementID]
		digests[res.Coordinator] = append(digests[res.Coordinator], res)

		codes := itemCodes(open)
		for _, ch := range []Channel{ChannelSMS, ChannelEmail} {
			recipient := ""
			if p != nil {
				if ch == ChannelSMS {
					recipient = p.Nurse.Phone
				} else {
					recipient = p.Nurse.Email
				}
			}
			if recipient == "" {
				continue
			}
			a := Action{
				Key:         fmt.Sprintf("%s|T-%d|%s", res.PlacementID, window, ch),
				Channel:     ch,
				Recipient:   recipient,
				PlacementID: res.PlacementID,
				Nurse:       res.Nurse,
				Facility:    res.Facility,
				Window:      window,
				DueOn:       b.AsOf,
				StartDate:   res.StartDate,
				Items:       codes,
				Subject:     nurseSubject(res, window),
				Body:        nurseBody(res, open),
			}
			if ledger.Has(a.Key) {
				plan.Suppressed = append(plan.Suppressed, a.Key)
				continue
			}
			plan.Actions = append(plan.Actions, a)
		}
	}

	for _, coordinator := range sortedKeys(digests) {
		results := digests[coordinator]
		a := Action{
			Key:       fmt.Sprintf("digest|%s|%s", coordinator, b.AsOf),
			Channel:   ChannelDigest,
			Recipient: coordinator,
			DueOn:     b.AsOf,
			Subject:   fmt.Sprintf("credential-chase: %d placement(s) with open items", len(results)),
			Body:      digestBody(results),
		}
		if ledger.Has(a.Key) {
			plan.Suppressed = append(plan.Suppressed, a.Key)
			continue
		}
		plan.Actions = append(plan.Actions, a)
	}

	sort.SliceStable(plan.Actions, func(i, j int) bool { return plan.Actions[i].Key < plan.Actions[j].Key })
	sort.Strings(plan.Suppressed)
	return plan
}

func itemCodes(items []engine.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Code)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string][]engine.Result) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nurseSubject(res engine.Result, window int) string {
	return fmt.Sprintf("[T-%d] %s starts %s — %d document(s) still needed",
		window, res.Facility, res.StartDate, len(res.OpenItems()))
}

func nurseBody(res engine.Result, open []engine.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s — your assignment at %s starts %s (%d days).\n",
		firstName(res.Nurse), res.Facility, res.StartDate, res.DaysToStart)
	b.WriteString("Still outstanding:\n")
	for _, it := range open {
		fmt.Fprintf(&b, "  - %s: %s\n", it.Name, it.Detail)
	}
	b.WriteString("Upload from your phone with the link in your last message. No app, no password.\n")
	return b.String()
}

func digestBody(results []engine.Result) string {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].StartDate.Before(results[j].StartDate)
	})
	var b strings.Builder
	b.WriteString("Placements with open credential items, soonest start first:\n")
	for _, res := range results {
		fmt.Fprintf(&b, "  [%s] %s — %s at %s, starts %s (T-%d): %d critical, %d warn\n",
			strings.ToUpper(string(res.Status)), res.PlacementID, res.Nurse,
			res.Facility, res.StartDate, res.DaysToStart, res.Critical, res.Warn)
		for _, it := range res.OpenItems() {
			fmt.Fprintf(&b, "      %s (%s) — %s\n", it.Name, it.Status, it.Detail)
		}
	}
	return b.String()
}

func firstName(full string) string {
	if i := strings.IndexByte(full, ' '); i > 0 {
		return full[:i]
	}
	return full
}
