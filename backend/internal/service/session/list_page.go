package session

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// MaxListLimit bounds one page of a session list.
const MaxListLimit = 200

// ListPage is one page of a session list.
type ListPage struct {
	Sessions []domain.Session
	// NextCursor resumes the list after the page's last session. Empty on the
	// last page, and for an unpaged list.
	NextCursor string
	// Archived counts the terminated sessions an active-only list (Active is
	// true) left out, so a client that does not fetch the archive can still say
	// how big it is. Nil for any other filter.
	Archived *ArchiveSummary
}

// ArchiveSummary is the size of the archive behind an active-only list, under
// the same project and kind filters.
type ArchiveSummary struct {
	Total     int
	ByProject map[domain.ProjectID]int
}

// selectSessionRecords keeps the rows matching filter and, for an active-only
// filter, counts the terminated rows that the same filter without Active would
// have kept.
func selectSessionRecords(recs []domain.SessionRecord, filter ListFilter) ([]domain.SessionRecord, *ArchiveSummary) {
	matched := make([]domain.SessionRecord, 0, len(recs))
	var archive *ArchiveSummary
	var anyState ListFilter
	if filter.Active != nil && *filter.Active {
		archive = &ArchiveSummary{ByProject: map[domain.ProjectID]int{}}
		anyState = filter
		anyState.Active = nil
	}
	for _, rec := range recs {
		if matchesSessionFilter(rec, filter) {
			matched = append(matched, rec)
			continue
		}
		if archive != nil && rec.IsTerminated && matchesSessionFilter(rec, anyState) {
			archive.Total++
			archive.ByProject[rec.ProjectID]++
		}
	}
	return matched, archive
}

// listPageKey is a row's position in a paged list.
type listPageKey struct {
	pinned    bool
	updatedAt time.Time
	id        domain.SessionID
}

func pageKeyOf(rec domain.SessionRecord) listPageKey {
	return listPageKey{pinned: rec.IsPinned, updatedAt: rec.UpdatedAt.UTC(), id: rec.ID}
}

// before reports whether a sorts ahead of b in a paged list: pinned rows
// first, then the most recently updated, then by id so the order is total and
// a cursor always resumes at one place. Desktop's archive orders by updatedAt
// too, so both apps list the archive the same way.
func (a listPageKey) before(b listPageKey) bool {
	if a.pinned != b.pinned {
		return a.pinned
	}
	if !a.updatedAt.Equal(b.updatedAt) {
		return a.updatedAt.After(b.updatedAt)
	}
	return a.id > b.id
}

// pageSessionRecords orders the rows, skips everything up to and including the
// cursor's row, and returns at most limit of them with the cursor for the rest.
// The cursor is a position rather than an offset, so rows added or removed
// between pages neither repeat nor go missing.
func pageSessionRecords(recs []domain.SessionRecord, after *listPageKey, limit int) ([]domain.SessionRecord, string) {
	sort.SliceStable(recs, func(i, j int) bool { return pageKeyOf(recs[i]).before(pageKeyOf(recs[j])) })
	if after != nil {
		start := sort.Search(len(recs), func(i int) bool { return after.before(pageKeyOf(recs[i])) })
		recs = recs[start:]
	}
	if len(recs) <= limit {
		return recs, ""
	}
	page := recs[:limit]
	return page, encodeSessionCursor(pageKeyOf(page[len(page)-1]))
}

func encodeSessionCursor(key listPageKey) string {
	pinned := "0"
	if key.pinned {
		pinned = "1"
	}
	value := pinned + "\n" + key.updatedAt.Format(time.RFC3339Nano) + "\n" + string(key.id)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSessionCursor(raw string) (listPageKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return listPageKey{}, invalidSessionCursor()
	}
	parts := strings.SplitN(string(decoded), "\n", 3)
	if len(parts) != 3 || parts[2] == "" {
		return listPageKey{}, invalidSessionCursor()
	}
	pinned, err := strconv.ParseBool(parts[0])
	if err != nil {
		return listPageKey{}, invalidSessionCursor()
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return listPageKey{}, invalidSessionCursor()
	}
	return listPageKey{pinned: pinned, updatedAt: updatedAt.UTC(), id: domain.SessionID(parts[2])}, nil
}

func invalidSessionCursor() error {
	return apierr.Invalid("INVALID_SESSION_CURSOR", "Session cursor is invalid", nil)
}
