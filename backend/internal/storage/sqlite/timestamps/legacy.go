package timestamps

import (
	"regexp"
	"strings"
	"time"
)

var legacyDriverTimestamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) ([+-]\d{4}) \S+(?: m=[+-][0-9.]+)?$`)

// These layouts mirror modernc's SQLite date/time formats. Values without an
// explicit offset use UTC, as they do on AO's SQLite connections.
var sqliteTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseLegacy preserves the instant encoded by a stored driver or SQLite time.
func ParseLegacy(value string) (time.Time, bool) {
	if match := legacyDriverTimestamp.FindStringSubmatch(value); match != nil {
		at, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", match[1]+" "+match[2])
		return at.UTC(), err == nil
	}
	if at, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return at.UTC(), true
	}
	value = strings.TrimSuffix(value, "Z")
	for _, layout := range sqliteTimestampLayouts {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}
