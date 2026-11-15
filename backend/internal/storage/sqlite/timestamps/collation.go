package timestamps

import (
	"strings"
	"time"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterCollationUtf8("ao_timestamp", compareStored)
}

// Query-time comparison keeps ordering and fences correct before repair reaches
// a row. No schema or index uses this collation, so older binaries can reopen it.
func compareStored(left, right string) int {
	l, lok := parseStored(left)
	r, rok := parseStored(right)
	if lok && rok {
		return l.Compare(r)
	}
	// Invalid values keep a deterministic order without making mixed valid and
	// invalid triples violate the ordering required by SQLite.
	if lok != rok {
		if lok {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func parseStored(value string) (time.Time, bool) {
	if at, ok := ParseLegacy(value); ok {
		return at, true
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}
