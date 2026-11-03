package timestamps

import (
	"regexp"
	"time"
)

var legacyDriverTimestamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) ([+-]\d{4}) \S+(?: m=[+-][0-9.]+)?$`)

// ParseLegacy preserves the instant encoded by a stored driver or RFC3339 time.
func ParseLegacy(value string) (time.Time, bool) {
	if match := legacyDriverTimestamp.FindStringSubmatch(value); match != nil {
		at, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", match[1]+" "+match[2])
		return at.UTC(), err == nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	return at.UTC(), err == nil
}
