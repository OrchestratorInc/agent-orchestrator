package pr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeEditor struct {
	content    ports.SCMPRContent
	contentErr error
	reads      int
	edited     ports.SCMPRContent
	editErr    error
	edits      []ports.SCMPREditRequest
}

func (f *fakeEditor) PullRequestContent(context.Context, ports.SCMPRRef) (ports.SCMPRContent, error) {
	f.reads++
	return f.content, f.contentErr
}

func (f *fakeEditor) EditPullRequest(_ context.Context, request ports.SCMPREditRequest) (ports.SCMPRContent, error) {
	f.edits = append(f.edits, request)
	return f.edited, f.editErr
}

type editWrite struct {
	url         string
	title, body *string
}

type fakeEditWriter struct {
	writes []editWrite
	err    error
}

func (f *fakeEditWriter) SetPRContent(_ context.Context, prURL string, title, description *string, _ time.Time) error {
	f.writes = append(f.writes, editWrite{url: prURL, title: title, body: description})
	return f.err
}

func ptr(s string) *string { return &s }

func editFixture() (domain.PullRequest, *fakeEditor, *fakeEditWriter, *fakeRefresher, *ActionService) {
	pr := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/42", Number: 42, Provider: "github", Host: "github.com", Repo: "acme/widgets", Title: "Old title"}
	editor := &fakeEditor{
		content: ports.SCMPRContent{Title: "Old title", Body: "Old body\r\nsecond line", CanEdit: true},
		edited:  ports.SCMPRContent{Title: "New title", Body: "New body", UpdatedAt: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)},
	}
	writer, refresher := &fakeEditWriter{}, &fakeRefresher{}
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Editor: editor, EditWriter: writer, Refresher: refresher})
	return pr, editor, writer, refresher, svc
}

func TestEditContent_ReturnsProviderTextAndWhyItCannotBeEdited(t *testing.T) {
	pr, editor, _, _, svc := editFixture()
	got, err := svc.EditContent(context.Background(), "42", pr.URL)
	if err != nil || got != (EditContentResult{Title: "Old title", Body: "Old body\r\nsecond line", CanEdit: true}) {
		t.Fatalf("content = %#v, err %v", got, err)
	}

	editor.content.CanEdit = false
	if got, err := svc.EditContent(context.Background(), "42", pr.URL); err != nil || got.CanEdit || got.Reason != EditReasonNoPermission {
		t.Fatalf("no permission = %#v, err %v", got, err)
	}

	editor.content.CanEdit = true
	for _, closed := range []domain.PullRequest{{Merged: true}, {Closed: true}} {
		closed.URL, closed.Number, closed.Provider, closed.Repo = pr.URL, pr.Number, pr.Provider, pr.Repo
		svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: closed, ok: true}, Editor: editor})
		if got, err := svc.EditContent(context.Background(), "42", pr.URL); err != nil || got.CanEdit || got.Reason != EditReasonNotOpen {
			t.Fatalf("not open = %#v, err %v", got, err)
		}
	}

	editor.contentErr = ports.ErrSCMUnsupported
	if got, err := svc.EditContent(context.Background(), "42", pr.URL); err != nil || got.CanEdit || got.Reason != EditReasonUnsupported {
		t.Fatalf("unsupported = %#v, err %v", got, err)
	}
	none := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}})
	if got, err := none.EditContent(context.Background(), "42", pr.URL); err != nil || got.CanEdit || got.Reason != EditReasonUnsupported {
		t.Fatalf("no editor = %#v, err %v", got, err)
	}
	editor.contentErr = ports.ErrSCMNotFound
	if _, err := svc.EditContent(context.Background(), "42", pr.URL); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
	if _, err := svc.EditContent(context.Background(), "7", pr.URL); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("wrong number error = %v, want not found", err)
	}
	if _, err := svc.EditContent(context.Background(), "x", pr.URL); !errors.Is(err, ErrInvalidPR) {
		t.Fatalf("bad id error = %v, want invalid", err)
	}
}

func TestEdit_SendsOnlyTheChangedFieldAndStoresIt(t *testing.T) {
	pr, editor, writer, refresher, svc := editFixture()
	got, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Title: ptr("  New title "), BaseTitle: ptr("Old title")})
	if err != nil {
		t.Fatal(err)
	}
	if got.PRNumber != 42 || got.Title != "New title" || got.Body != "New body" || got.UpdatedAt.IsZero() {
		t.Fatalf("result = %#v", got)
	}
	if len(editor.edits) != 1 || *editor.edits[0].Title != "New title" || editor.edits[0].Body != nil || editor.edits[0].PR.Repo.Owner != "acme" || editor.edits[0].PR.Number != 42 {
		t.Fatalf("edit = %#v", editor.edits)
	}
	if len(writer.writes) != 1 || writer.writes[0].url != pr.URL || *writer.writes[0].title != "New title" || writer.writes[0].body != nil {
		t.Fatalf("writes = %#v", writer.writes)
	}
	if len(refresher.urls) != 1 || refresher.urls[0] != pr.URL {
		t.Fatalf("refreshes = %q", refresher.urls)
	}
}

func TestEdit_BodyComparesLineEndingsLooselyAndCanClear(t *testing.T) {
	pr, editor, writer, _, svc := editFixture()
	// A text field reads the provider's CRLF back as LF.
	if _, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Body: ptr("New body"), BaseBody: ptr("Old body\nsecond line")}); err != nil {
		t.Fatal(err)
	}
	if len(editor.edits) != 1 || editor.edits[0].Title != nil || *editor.edits[0].Body != "New body" {
		t.Fatalf("edit = %#v", editor.edits)
	}
	if len(writer.writes) != 1 || writer.writes[0].title != nil || *writer.writes[0].body != "New body" {
		t.Fatalf("writes = %#v", writer.writes)
	}
	editor.edited.Body = ""
	if got, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Body: ptr(""), BaseBody: ptr("Old body\nsecond line")}); err != nil || got.Body != "" || *editor.edits[1].Body != "" {
		t.Fatalf("clear = %#v, err %v", got, err)
	}
}

func TestEdit_RefusesWhenTheProviderTextMoved(t *testing.T) {
	for name, request := range map[string]EditRequest{
		"title":                    {Title: ptr("Mine"), BaseTitle: ptr("What I saw")},
		"body":                     {Body: ptr("Mine"), BaseBody: ptr("What I saw")},
		"body moved, both edited":  {Title: ptr("Mine"), BaseTitle: ptr("Old title"), Body: ptr("Mine"), BaseBody: ptr("What I saw")},
		"title moved, both edited": {Title: ptr("Mine"), BaseTitle: ptr("What I saw"), Body: ptr("Mine"), BaseBody: ptr("Old body\nsecond line")},
	} {
		pr, editor, writer, refresher, svc := editFixture()
		request.PRID, request.PRURL = "42", pr.URL
		if _, err := svc.Edit(context.Background(), request); !errors.Is(err, ErrPREditConflict) {
			t.Fatalf("%s: error = %v, want conflict", name, err)
		}
		if len(editor.edits) != 0 {
			t.Fatalf("%s: a conflicting edit reached the provider", name)
		}
		// The view gets what the provider holds now.
		if len(writer.writes) != 1 || *writer.writes[0].title != "Old title" || *writer.writes[0].body != "Old body\r\nsecond line" || len(refresher.urls) != 1 {
			t.Fatalf("%s: writes = %#v refreshes = %q", name, writer.writes, refresher.urls)
		}
	}
}

func TestEdit_ChangeToTheOtherFieldIsNotAConflict(t *testing.T) {
	pr, editor, _, _, svc := editFixture()
	editor.content.Body = "someone rewrote the description"
	if _, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Title: ptr("New title"), BaseTitle: ptr("Old title")}); err != nil {
		t.Fatalf("title edit after a description change: %v", err)
	}
}

func TestEdit_ValidatesBeforeCallingTheProvider(t *testing.T) {
	pr, editor, _, _, svc := editFixture()
	for name, request := range map[string]EditRequest{
		"nothing":          {},
		"blank title":      {Title: ptr("   "), BaseTitle: ptr("Old title")},
		"two-line title":   {Title: ptr("a\nb"), BaseTitle: ptr("Old title")},
		"long title":       {Title: ptr(strings.Repeat("x", maxEditTitleRunes+1)), BaseTitle: ptr("Old title")},
		"title sans base":  {Title: ptr("New")},
		"body sans base":   {Body: ptr("New")},
		"oversized body":   {Body: ptr(strings.Repeat("x", maxEditBodyBytes+1)), BaseBody: ptr("")},
		"title, body base": {Title: ptr("New"), BaseBody: ptr("")},
	} {
		request.PRID, request.PRURL = "42", pr.URL
		if _, err := svc.Edit(context.Background(), request); !errors.Is(err, ErrInvalidEdit) {
			t.Fatalf("%s: error = %v, want invalid edit", name, err)
		}
	}
	if _, err := svc.Edit(context.Background(), EditRequest{PRID: "0", PRURL: pr.URL, Title: ptr("New"), BaseTitle: ptr("Old title")}); !errors.Is(err, ErrInvalidPR) {
		t.Fatalf("bad id error = %v, want invalid pr", err)
	}
	if editor.reads != 0 || len(editor.edits) != 0 {
		t.Fatalf("invalid edits reached the provider: reads=%d edits=%d", editor.reads, len(editor.edits))
	}
	// A title of exactly the limit, counted in characters, is fine.
	if _, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Title: ptr(strings.Repeat("é", maxEditTitleRunes)), BaseTitle: ptr("Old title")}); err != nil {
		t.Fatalf("title at the limit: %v", err)
	}
}

func TestEdit_RefusesClosedForbiddenAndUnsupported(t *testing.T) {
	pr, editor, writer, _, svc := editFixture()
	request := EditRequest{PRID: "42", PRURL: pr.URL, Title: ptr("New title"), BaseTitle: ptr("Old title")}

	for _, closed := range []domain.PullRequest{{Merged: true}, {Closed: true}} {
		closed.URL, closed.Number, closed.Provider, closed.Repo = pr.URL, pr.Number, pr.Provider, pr.Repo
		closedSvc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: closed, ok: true}, Editor: editor})
		if _, err := closedSvc.Edit(context.Background(), request); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("closed error = %v, want invalid transition", err)
		}
	}
	if editor.reads != 0 {
		t.Fatalf("a closed PR's edit read the provider %d times", editor.reads)
	}

	editor.content.CanEdit = false
	if _, err := svc.Edit(context.Background(), request); !errors.Is(err, ErrPREditForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	editor.content.CanEdit = true
	for provider, want := range map[error]error{
		ports.ErrSCMForbidden:   ErrPREditForbidden,
		ports.ErrSCMNotFound:    ErrPRNotFound,
		ports.ErrSCMRejected:    ErrPRRejected,
		ports.ErrSCMUnsupported: ErrUnsupported,
	} {
		editor.editErr = provider
		if _, err := svc.Edit(context.Background(), request); !errors.Is(err, want) {
			t.Fatalf("provider %v: error = %v, want %v", provider, err, want)
		}
	}
	if len(writer.writes) != 0 {
		t.Fatalf("failed edits were stored: %#v", writer.writes)
	}
	none := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}})
	if _, err := none.Edit(context.Background(), request); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no editor error = %v, want unsupported", err)
	}
	missing := NewActionService(ActionDeps{Store: &fakeActionStore{}, Editor: editor})
	if _, err := missing.Edit(context.Background(), request); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("untracked error = %v, want not found", err)
	}
}

func TestEdit_SucceedsWhenTheLocalWriteFails(t *testing.T) {
	pr, _, writer, _, svc := editFixture()
	writer.err = errors.New("disk full")
	if got, err := svc.Edit(context.Background(), EditRequest{PRID: "42", PRURL: pr.URL, Title: ptr("New title"), BaseTitle: ptr("Old title")}); err != nil || got.Title != "New title" {
		t.Fatalf("result = %#v, err %v", got, err)
	}
}
