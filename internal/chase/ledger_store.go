package chase

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadLedger reads a ledger from disk. A missing file is an empty ledger,
// not an error: the first run of a new pilot has nothing to dedupe against.
func LoadLedger(path string) (*Ledger, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewLedger(), nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return NewLedger(), nil
	}
	l := NewLedger()
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	l.index = nil
	l.reindex()
	return l, nil
}

// SaveLedger writes the ledger atomically, so a crash mid-write cannot lose
// the record of what was already sent.
func SaveLedger(path string, l *Ledger) error {
	if l.Entries == nil {
		l.Entries = []Entry{}
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ledger-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AuditHeader is the column order of the audit export. It is stable: a
// reviewer who has seen one export should recognise the next one.
var AuditHeader = []string{
	"sent_on", "placement_id", "nurse", "facility",
	"channel", "recipient", "window_days", "items", "subject", "key",
}

// WriteAuditCSV exports the ledger as the audit trail a Joint Commission
// HCSS reviewer asks for: who was chased, for what, on which day, through
// which channel.
func WriteAuditCSV(w io.Writer, l *Ledger) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(AuditHeader); err != nil {
		return err
	}
	for _, e := range l.Entries {
		row := []string{
			e.SentOn.String(),
			e.PlacementID,
			e.Nurse,
			e.Facility,
			string(e.Channel),
			e.Recipient,
			strconv.Itoa(e.Window),
			strings.Join(e.Items, " "),
			e.Subject,
			e.Key,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
