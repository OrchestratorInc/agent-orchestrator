package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// recordingStore notes which sessions a list read loaded PR facts for.
type recordingStore struct {
	*fakeStore
	prFactIDs []domain.SessionID
}

func (r *recordingStore) ListPRFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	r.prFactIDs = append(r.prFactIDs, ids...)
	return r.fakeStore.ListPRFactsForSessions(ctx, ids)
}

func archivedRecord(id string, project domain.ProjectID, updated time.Time) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), ProjectID: project, Kind: domain.KindWorker, IsTerminated: true, CreatedAt: updated, UpdatedAt: updated}
}

func pageIDs(page ListPage) []string {
	out := make([]string, 0, len(page.Sessions))
	for _, sess := range page.Sessions {
		out = append(out, string(sess.ID))
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

// The archive pages pinned first, then most recently updated (desktop's
// archive order), with the id breaking ties, whatever order the store returns.
func TestListPageOrdersTheArchiveAndResumesAfterTheCursor(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 7; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%d", i), "ao", base.Add(time.Duration(i)*time.Hour))
		st.sessions[rec.ID] = rec
	}
	tie := archivedRecord("a-9", "ao", base.Add(4*time.Hour)) // same updatedAt as a-4
	st.sessions[tie.ID] = tie
	pinned := archivedRecord("a-0", "ao", base)
	pinned.IsPinned = true
	st.sessions[pinned.ID] = pinned
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "ao", Kind: domain.KindWorker}

	svc := &Service{store: st}
	filter := ListFilter{Active: boolPtr(false), Limit: 4}
	var got []string
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("cursor never ran out")
		}
		page, err := svc.ListPage(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Sessions) > filter.Limit {
			t.Fatalf("page of %d, limit %d", len(page.Sessions), filter.Limit)
		}
		got = append(got, pageIDs(page)...)
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
	}
	want := []string{"a-0", "a-7", "a-6", "a-5", "a-9", "a-4", "a-3", "a-2", "a-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("archive order = %v, want %v", got, want)
	}
}

// A cursor is a position, not an offset: a session archived between two page
// reads lands at the top and neither repeats nor pushes a row out of page two.
func TestListPageCursorSurvivesNewArchiveRowsBetweenPages(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 4; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%d", i), "ao", base.Add(time.Duration(i)*time.Hour))
		st.sessions[rec.ID] = rec
	}
	svc := &Service{store: st}
	first, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	newest := archivedRecord("a-new", "ao", base.Add(10*time.Hour))
	st.sessions[newest.ID] = newest
	second, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pageIDs(first), []string{"a-4", "a-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if got, want := pageIDs(second), []string{"a-2", "a-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if second.NextCursor != "" {
		t.Fatalf("last page cursor = %q, want none", second.NextCursor)
	}
}

// Paging happens before the read model is built, so a page of a big archive
// loads PR facts for that page only.
func TestListPageLoadsFactsForThePageOnly(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := &recordingStore{fakeStore: newFakeStore()}
	for i := 1; i <= 60; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%02d", i), "ao", base.Add(time.Duration(i)*time.Minute))
		st.sessions[rec.ID] = rec
	}
	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 5 || len(st.prFactIDs) != 5 {
		t.Fatalf("page %d sessions, PR facts loaded for %d; want 5 and 5", len(page.Sessions), len(st.prFactIDs))
	}
}

// The live list says how big the archive it left out is, per project and
// under the same kind filter, so a phone can show Archive's count without
// downloading it.
func TestListPageSummarisesTheArchiveBehindAnActiveList(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "ao", Kind: domain.KindWorker}
	st.sessions["legacy-live"] = domain.SessionRecord{ID: "legacy-live", ProjectID: "ao"} // written before kinds
	st.sessions["ao-1"] = archivedRecord("ao-1", "ao", now)
	st.sessions["ao-2"] = archivedRecord("ao-2", "ao", now)
	st.sessions["web-1"] = archivedRecord("web-1", "web", now)
	st.sessions["solo"] = archivedRecord("solo", "", now)
	orch := archivedRecord("ao-orch", "ao", now)
	orch.Kind = domain.KindOrchestrator
	st.sessions[orch.ID] = orch
	st.sessions["prep"] = domain.SessionRecord{ID: "prep", ProjectID: "ao", Kind: domain.KindWorker, IsTerminated: true, IsTaskPreparation: true}
	svc := &Service{store: st}

	page, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(true), Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	ids := pageIDs(page)
	if len(ids) != 2 {
		t.Fatalf("live workers = %v, want live and legacy-live", ids)
	}
	want := &ArchiveSummary{Total: 4, ByProject: map[domain.ProjectID]int{"ao": 2, "web": 1, "": 1}}
	if !reflect.DeepEqual(page.Archived, want) {
		t.Fatalf("archive summary = %+v, want %+v", page.Archived, want)
	}

	scoped, err := svc.ListPage(context.Background(), ListFilter{ProjectID: "web", Active: boolPtr(true), Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Archived == nil || scoped.Archived.Total != 1 {
		t.Fatalf("project-scoped summary = %+v, want total 1", scoped.Archived)
	}

	for _, filter := range []ListFilter{{}, {Active: boolPtr(false)}} {
		page, err := svc.ListPage(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if page.Archived != nil {
			t.Fatalf("filter %+v reported an archive summary %+v", filter, page.Archived)
		}
	}
}

func TestListPageRejectsBadPagingInput(t *testing.T) {
	svc := &Service{store: newFakeStore()}
	cursor := encodeSessionCursor(listPageKey{id: "a-1", updatedAt: time.Now().UTC()})
	for name, filter := range map[string]ListFilter{
		"garbage cursor":       {Limit: 10, Cursor: "%%%"},
		"cursor without id":    {Limit: 10, Cursor: "MAoyMDI2LTEwLTAxVDAwOjAwOjAwWgo"},
		"cursor without limit": {Cursor: cursor},
		"limit too large":      {Limit: MaxListLimit + 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.ListPage(context.Background(), filter)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
				t.Fatalf("err = %v, want an invalid-input error", err)
			}
		})
	}
}

// An unpaged list is unchanged: every match, no cursor.
func TestListWithoutLimitReturnsEveryMatch(t *testing.T) {
	st := newFakeStore()
	for i := 0; i < MaxListLimit+5; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%03d", i), "ao", time.Now())
		st.sessions[rec.ID] = rec
	}
	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != MaxListLimit+5 || page.NextCursor != "" {
		t.Fatalf("unpaged list = %d sessions, cursor %q", len(page.Sessions), page.NextCursor)
	}
}

func mergedPR(number int, mergedAt time.Time) domain.PRFacts {
	return domain.PRFacts{URL: fmt.Sprintf("https://github.com/acme/w/pull/%d", number), Number: number, Merged: true, StateChangedAt: mergedAt, UpdatedAt: mergedAt.Add(time.Hour)}
}

// A merged-PR list keeps the archived sessions owning a merged PR, newest
// merge first however recently the session itself changed, and pages all of
// them while reporting the total.
func TestListPageFiltersByPRStateOrderedByWhenItWasReached(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 5; i++ {
		// Session updatedAt runs opposite to merge time.
		rec := archivedRecord(fmt.Sprintf("m-%d", i), "ao", base.Add(time.Duration(10-i)*time.Hour))
		st.sessions[rec.ID] = rec
		st.prFacts[rec.ID] = []domain.PRFacts{mergedPR(i, base.Add(time.Duration(i)*time.Minute))}
	}
	// Pinning does not lift a session above newer merges.
	pinned := st.sessions["m-1"]
	pinned.IsPinned = true
	st.sessions["m-1"] = pinned
	// A session whose only PR is closed, one with no PR, and one with an open
	// PR and an older merged one.
	st.sessions["closed"] = archivedRecord("closed", "ao", base)
	st.prFacts["closed"] = []domain.PRFacts{{Number: 90, Closed: true, UpdatedAt: base.Add(time.Hour)}}
	st.sessions["none"] = archivedRecord("none", "ao", base)
	st.sessions["both"] = archivedRecord("both", "ao", base)
	legacy := mergedPR(91, time.Time{})
	legacy.UpdatedAt = base.Add(90 * time.Second) // no state change recorded: between m-1 and m-2
	st.prFacts["both"] = []domain.PRFacts{{Number: 92, UpdatedAt: base.Add(time.Hour)}, legacy}

	svc := &Service{store: st}
	filter := ListFilter{Active: boolPtr(false), Kind: domain.KindWorker, PRState: domain.PRStateMerged, Limit: 2}
	var got []string
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("cursor never ran out")
		}
		page, err := svc.ListPage(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total == nil || *page.Total != 6 {
			t.Fatalf("total = %v, want 6", page.Total)
		}
		got = append(got, pageIDs(page)...)
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
	}
	want := []string{"m-5", "m-4", "m-3", "m-2", "both", "m-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged order = %v, want %v", got, want)
	}

	all, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), PRState: domain.PRStateMerged})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pageIDs(all), want) {
		t.Fatalf("unpaged merged order = %v, want %v", pageIDs(all), want)
	}
}

// The PR-state filter replaces the archive summary with its own total and
// keeps the other filters: an active list with a merged filter holds only live
// sessions whose PR merged.
func TestListPagePRStateKeepsTheOtherFilters(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["live"] = domain.SessionRecord{ID: "live", ProjectID: "ao", Kind: domain.KindWorker, UpdatedAt: base}
	st.prFacts["live"] = []domain.PRFacts{mergedPR(1, base)}
	st.sessions["gone"] = archivedRecord("gone", "ao", base)
	st.prFacts["gone"] = []domain.PRFacts{mergedPR(2, base)}
	st.sessions["other"] = archivedRecord("other", "web", base)
	st.prFacts["other"] = []domain.PRFacts{mergedPR(3, base)}

	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{Active: boolPtr(true), PRState: domain.PRStateMerged})
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(page); !reflect.DeepEqual(got, []string{"live"}) {
		t.Fatalf("active merged = %v, want [live]", got)
	}
	if page.Archived != nil || page.Total == nil || *page.Total != 1 {
		t.Fatalf("archived = %+v, total = %v; want no summary and total 1", page.Archived, page.Total)
	}
	page, err = (&Service{store: st}).ListPage(context.Background(), ListFilter{ProjectID: "web", PRState: domain.PRStateMerged})
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(page); !reflect.DeepEqual(got, []string{"other"}) {
		t.Fatalf("project web merged = %v, want [other]", got)
	}
}

// A PR-state page loads card facts for its own sessions only: which sessions
// match comes from the narrow PR-state read, not from every candidate's facts.
func TestListPagePRStateLoadsFactsForThePageOnly(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := &recordingStore{fakeStore: newFakeStore()}
	for i := 1; i <= 60; i++ {
		rec := archivedRecord(fmt.Sprintf("a-%02d", i), "ao", base.Add(time.Duration(i)*time.Minute))
		st.sessions[rec.ID] = rec
		if i%3 == 0 {
			st.prFacts[rec.ID] = []domain.PRFacts{mergedPR(i, base.Add(time.Duration(i)*time.Hour))}
		}
	}
	page, err := (&Service{store: st}).ListPage(context.Background(), ListFilter{Active: boolPtr(false), PRState: domain.PRStateMerged, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pageIDs(page), []string{"a-60", "a-57"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page = %v, want %v", got, want)
	}
	if page.Total == nil || *page.Total != 20 {
		t.Fatalf("total = %v, want 20", page.Total)
	}
	loaded := make([]string, 0, len(st.prFactIDs))
	for _, id := range st.prFactIDs {
		loaded = append(loaded, string(id))
	}
	sort.Strings(loaded)
	if want := []string{"a-57", "a-60"}; !reflect.DeepEqual(loaded, want) {
		t.Fatalf("PR facts loaded for %v, want exactly the page's %v", loaded, want)
	}
}

// The version names the whole PR-state list: every page of one list carries
// it, a re-read of the same list repeats it, and a change on a later page
// changes it even when the total and the first page stay the same (one
// archived session restored and another ended).
func TestListPagePRStateVersionNamesTheWholeList(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 1; i <= 4; i++ {
		rec := archivedRecord(fmt.Sprintf("m-%d", i), "ao", base)
		st.sessions[rec.ID] = rec
		st.prFacts[rec.ID] = []domain.PRFacts{mergedPR(i, base.Add(time.Duration(i)*time.Hour))}
	}
	svc := &Service{store: st}
	filter := ListFilter{Active: boolPtr(false), PRState: domain.PRStateMerged, Limit: 2}
	first, err := svc.ListPage(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	filter.Cursor = first.NextCursor
	second, err := svc.ListPage(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version == "" || first.Version != second.Version {
		t.Fatalf("versions = %q and %q, want one non-empty version for both pages", first.Version, second.Version)
	}
	again, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), PRState: domain.PRStateMerged, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != first.Version {
		t.Fatalf("re-read version = %q, want %q", again.Version, first.Version)
	}

	// m-1 is restored and m-0, merged earlier still, ends: same total, same
	// first page (m-4, m-3), different second page.
	restored := st.sessions["m-1"]
	restored.IsTerminated = false
	st.sessions["m-1"] = restored
	ended := archivedRecord("m-0", "ao", base)
	st.sessions[ended.ID] = ended
	st.prFacts[ended.ID] = []domain.PRFacts{mergedPR(100, base)}
	moved, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), PRState: domain.PRStateMerged, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(moved); !reflect.DeepEqual(got, pageIDs(first)) || *moved.Total != *first.Total {
		t.Fatalf("first page %v total %d, want the same as before (%v, %d)", got, *moved.Total, pageIDs(first), *first.Total)
	}
	if moved.Version == first.Version {
		t.Fatalf("version %q unchanged after a later page changed", moved.Version)
	}

	plain, err := svc.ListPage(context.Background(), ListFilter{Active: boolPtr(false), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Version != "" {
		t.Fatalf("list without prState has version %q", plain.Version)
	}
}

func TestMatchesSessionFilterKind(t *testing.T) {
	worker := domain.SessionRecord{ID: "w1", Kind: domain.KindWorker}
	legacy := domain.SessionRecord{ID: "w0", Kind: ""}
	orch := domain.SessionRecord{ID: "o1", Kind: domain.KindOrchestrator}
	reviewer := domain.SessionRecord{ID: "r1", Kind: domain.KindReviewer}

	workerFilter := ListFilter{Kind: domain.KindWorker}
	orchFilter := ListFilter{Kind: domain.KindOrchestrator}
	reviewerFilter := ListFilter{Kind: domain.KindReviewer}

	if !matchesSessionFilter(worker, workerFilter) {
		t.Fatal("worker must match worker filter")
	}
	if !matchesSessionFilter(legacy, workerFilter) {
		t.Fatal("legacy session must match worker filter")
	}
	if matchesSessionFilter(orch, workerFilter) {
		t.Fatal("orchestrator must not match worker filter")
	}
	if matchesSessionFilter(reviewer, workerFilter) {
		t.Fatal("reviewer must not match worker filter")
	}

	if matchesSessionFilter(worker, orchFilter) || matchesSessionFilter(legacy, orchFilter) {
		t.Fatal("worker must not match orchestrator filter")
	}
	if !matchesSessionFilter(orch, orchFilter) {
		t.Fatal("orchestrator must match orchestrator filter")
	}
	if matchesSessionFilter(reviewer, orchFilter) {
		t.Fatal("reviewer must not match orchestrator filter")
	}

	if matchesSessionFilter(worker, reviewerFilter) || matchesSessionFilter(legacy, reviewerFilter) {
		t.Fatal("worker must not match reviewer filter")
	}
	if matchesSessionFilter(orch, reviewerFilter) {
		t.Fatal("orchestrator must not match reviewer filter")
	}
	if !matchesSessionFilter(reviewer, reviewerFilter) {
		t.Fatal("reviewer must match reviewer filter")
	}
}
