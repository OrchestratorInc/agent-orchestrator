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

const MaxListLimit = 200

type ListPage struct {
	Sessions   []domain.Session
	NextCursor string
	Archived   *ArchiveSummary
}

type ArchiveSummary struct {
	Total     int
	ByProject map[domain.ProjectID]int
}

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

type listPageKey struct {
	pinned    bool
	updatedAt time.Time
	id        domain.SessionID
}

func pageKeyOf(rec domain.SessionRecord) listPageKey {
	return listPageKey{pinned: rec.IsPinned, updatedAt: rec.UpdatedAt.UTC(), id: rec.ID}
}

func (a listPageKey) before(b listPageKey) bool {
	if a.pinned != b.pinned {
		return a.pinned
	}
	if !a.updatedAt.Equal(b.updatedAt) {
		return a.updatedAt.After(b.updatedAt)
	}
	return a.id > b.id
}

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
