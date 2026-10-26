package timestamp

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// ObservedAt tolerates legacy text in the one nullable native identity column.
// Normal writes still bind a time.Time, preserving SQLite's existing format.
type ObservedAt struct{ sql.NullTime }

// Scan accepts SQLite timestamps and legacy blank values.
func (v *ObservedAt) Scan(src any) error {
	if text, ok := src.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			v.NullTime = sql.NullTime{}
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999", time.DateTime} {
			if parsed, err := time.Parse(layout, text); err == nil {
				v.NullTime = sql.NullTime{Time: parsed, Valid: true}
				return nil
			}
		}
		return fmt.Errorf("invalid native identity observation timestamp %q", text)
	}
	return v.NullTime.Scan(src)
}

// Value writes the normal nullable time representation.
func (v ObservedAt) Value() (driver.Value, error) { return v.NullTime.Value() }
