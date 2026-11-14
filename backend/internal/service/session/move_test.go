package session

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionmove"
)

type moveStore struct{ *fakeStore }

func (m moveStore) SetSessionMovedTo(_ context.Context, id domain.SessionID, move domain.SessionMove, _ time.Time) (bool, error) {
	rec := m.sessions[id]
	rec.MovedTo = &move
	m.sessions[id] = rec
	return true, nil
}

// fakeStore counts these calls without a lock, and the orchestrator notice
// reads sessions on its own goroutine. No session here has pull requests.
func (moveStore) ListPRFactsForSession(context.Context, domain.SessionID) ([]domain.PRFacts, error) {
	return nil, nil
}

func (moveStore) ListPRFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	return nil, nil
}

func TestCompleteMoveDoesNotWaitForTheOrchestrator(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker}
	release := make(chan struct{})
	told := make(chan string, 1)
	fc := &fakeCommander{sendFunc: func(id domain.SessionID, message string) error {
		<-release
		told <- string(id) + " " + message
		return nil
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}})

	dest := domain.SessionMove{MoveID: "move-1", OrgID: "org-1", SessionID: "cloud-1"}
	if _, err := svc.CompleteMove(context.Background(), "mer-2", MoveCompleteInput{Destination: dest}); err != nil {
		t.Fatalf("CompleteMove: %v", err)
	}
	if st.sessions["mer-2"].MovedTo == nil || len(fc.killed) != 1 {
		t.Fatalf("CompleteMove returned before marking and archiving: moved=%v killed=%v", st.sessions["mer-2"].MovedTo, fc.killed)
	}
	close(release)
	select {
	case got := <-told:
		if !strings.HasPrefix(got, "mer-1 ") || !strings.Contains(got, "mer-2") || !strings.Contains(got, "cloud-1") {
			t.Fatalf("orchestrator message = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the orchestrator was not told")
	}
}

func TestMoveRefusalsNameTheFiles(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: model.bin, data/weights.bin", sessionmove.ErrLFSObjects), "Git LFS files cannot move yet: model.bin, data/weights.bin."},
		{fmt.Errorf("%w: vendor/sub", sessionmove.ErrDirtySubmodule), "Commit or discard the changes inside vendor/sub first."},
		{&sessionmove.FileTooLargeError{Limit: 1, Files: []sessionmove.Blob{{Path: "big.bin"}, {Path: "big.bin"}, {Path: "data.csv"}}}, "Over GitHub's 100 MiB file limit: big.bin, data.csv."},
		{fmt.Errorf("%w: MERGE_HEAD exists", sessionmove.ErrOperationInProgress), "Finish or abort the merge, rebase or conflict in the worktree first."},
	} {
		var got *apierr.Error
		if !errors.As(moveAPIError(tc.err), &got) || got.Message != tc.want {
			t.Errorf("moveAPIError(%v) = %v, want message %q", tc.err, got, tc.want)
		}
	}
}

// transcriptCommander is a manager that reads a session's agent conversation.
type transcriptCommander struct {
	*fakeCommander
	size     int64
	sizeErr  error
	id, data string
	writeErr error
}

func (c transcriptCommander) CloudTranscriptSize(context.Context, domain.SessionRecord, domain.ProjectRecord) (int64, error) {
	return c.size, c.sizeErr
}

func (c transcriptCommander) WriteCloudTranscript(_ context.Context, _ domain.SessionRecord, _ domain.ProjectRecord, w io.Writer, _ int64) (string, error) {
	if c.writeErr != nil {
		return "", c.writeErr
	}
	_, err := io.WriteString(w, c.data)
	return c.id, err
}

func transcriptService(c transcriptCommander, state domain.ActivityState) *Service {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: state}, Metadata: domain.SessionMetadata{WorkspacePath: "/wt/mer-2"},
	}
	c.fakeCommander = &fakeCommander{}
	return NewWithDeps(Deps{Manager: c, Store: st})
}

func TestMoveTranscriptGzipsTheConversation(t *testing.T) {
	svc := transcriptService(transcriptCommander{id: "aid-1", data: "{\"cwd\":\"/ao-moved/cwd\"}\n"}, domain.ActivityExited)
	out, err := svc.MoveTranscript(context.Background(), "mer-2")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(out.Gzip))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	if out.AgentSessionID != "aid-1" || string(raw) != "{\"cwd\":\"/ao-moved/cwd\"}\n" {
		t.Fatalf("MoveTranscript = %q, %q", out.AgentSessionID, raw)
	}
}

func TestMoveTranscriptRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		c     transcriptCommander
		state domain.ActivityState
		code  string
	}{
		{"running agent", transcriptCommander{}, domain.ActivityIdle, "MOVE_AGENT_RUNNING"},
		{"another agent", transcriptCommander{writeErr: sessionmanager.ErrCloudTranscriptUnsupported}, domain.ActivityExited, "MOVE_TRANSCRIPT_UNSUPPORTED"},
		{"too large", transcriptCommander{writeErr: fmt.Errorf("%w: 70 MiB", nativetransfer.ErrTooLarge)}, domain.ActivityExited, "MOVE_TRANSCRIPT_TOO_LARGE"},
		{"no transcript", transcriptCommander{writeErr: errors.New("no Claude Code transcript")}, domain.ActivityExited, "MOVE_TRANSCRIPT_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transcriptService(tc.c, tc.state).MoveTranscript(context.Background(), "mer-2")
			var got *apierr.Error
			if !errors.As(err, &got) || got.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestMoveTranscriptInfoSaysWhyTheConversationStays(t *testing.T) {
	for _, tc := range []struct {
		c    transcriptCommander
		want MoveTranscriptInfo
	}{
		{transcriptCommander{size: 2048}, MoveTranscriptInfo{Bytes: 2048}},
		{transcriptCommander{sizeErr: sessionmanager.ErrCloudTranscriptUnsupported}, MoveTranscriptInfo{Unavailable: "unsupported"}},
		{transcriptCommander{sizeErr: errors.New("missing")}, MoveTranscriptInfo{Unavailable: "not_found"}},
		{transcriptCommander{size: CloudTranscriptMaxBytes + 1}, MoveTranscriptInfo{Bytes: CloudTranscriptMaxBytes + 1, Unavailable: "too_large"}},
	} {
		svc := transcriptService(tc.c, domain.ActivityIdle)
		if got := svc.moveTranscriptInfo(context.Background(), domain.SessionRecord{}, domain.ProjectRecord{}); got != tc.want {
			t.Errorf("moveTranscriptInfo = %+v, want %+v", got, tc.want)
		}
	}
}

func TestWriteImportArchiveReadsTheCloudTranscript(t *testing.T) {
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"type":"user","cwd":"/workspace/repository","sessionId":"` + id + `","version":"2.1.287","message":{"role":"user","content":"hi"}}` + "\n"))
	_ = zw.Close()
	transcript := MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: gz.Bytes()}

	archive, err := writeImportArchive(context.Background(), t.TempDir(), transcript)
	if err != nil {
		t.Fatal(err)
	}
	res, err := nativetransfer.ImportClaude(context.Background(), mustOpen(t, archive), nativetransfer.ClaudeImportOptions{ConfigDir: t.TempDir(), LaunchCwd: t.TempDir()})
	if err != nil || res.NativeID != id {
		t.Fatalf("ImportClaude = %+v, %v", res, err)
	}

	for name, bad := range map[string]MoveImportTranscript{
		"not gzip":   {AgentSessionID: id, ProjectDir: transcript.ProjectDir, Gzip: []byte("plain")},
		"id as path": {AgentSessionID: "../" + id, ProjectDir: transcript.ProjectDir, Gzip: gz.Bytes()},
	} {
		if _, err := writeImportArchive(context.Background(), t.TempDir(), bad); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func mustOpen(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func importManifest(tip string) MoveManifest {
	return MoveManifest{RepoKey: "github.com/acme/app", Branch: "feat/move", Tip: tip, Harness: domain.HarnessClaudeCode, SourceLabel: "Orchestrator Cloud"}
}

func TestImportMoveRefusesAnotherRepository(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/other"}
	fc := &fakeCommander{}
	_, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), MoveImportInput{
		ProjectID: "mer", MoveID: "m-1", Manifest: importManifest(strings.Repeat("a", 40)),
	})
	var got *apierr.Error
	if !errors.As(err, &got) || got.Code != "MOVE_REPO_MISMATCH" || fc.spawnCalls != 0 {
		t.Fatalf("err = %v, spawns = %d; want MOVE_REPO_MISMATCH and no spawn", err, fc.spawnCalls)
	}
}

func TestImportMoveReplaysTheSameMove(t *testing.T) {
	in := MoveImportInput{ProjectID: "mer", MoveID: "m-1", Manifest: importManifest(strings.Repeat("a", 40))}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		ClientRequestID: "m-1", ClientRequestHash: moveRequestHash(in), ClientRequestCommitted: true,
	}
	fc := &fakeCommander{}
	out, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), in)
	if err != nil || out.Session.ID != "mer-1" || fc.spawnCalls != 0 {
		t.Fatalf("ImportMove = %q, %v, spawns = %d; want mer-1 without a spawn", out.Session.ID, err, fc.spawnCalls)
	}
}

// pushedBranch makes an origin with feat/move one commit ahead of main and a
// clone that has only main, and returns the clone and feat/move's tip.
func pushedBranch(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		args = append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, src := root+"/origin.git", root+"/src"
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "init", "-q", "-b", "main", src)
	git(src, "commit", "-q", "--allow-empty", "-m", "seed")
	git(src, "push", "-q", origin, "main")
	git(src, "switch", "-q", "-c", "feat/move")
	git(src, "commit", "-q", "--allow-empty", "-m", "work")
	git(src, "push", "-q", origin, "feat/move")
	git(root, "clone", "-q", origin, root+"/repo")
	return root + "/repo", git(src, "rev-parse", "HEAD")
}

func TestImportMoveStartsFromTheNoteWhenTheConversationCannotBeRead(t *testing.T) {
	repo, tip := pushedBranch(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"type":"user","cwd":"/workspace/repository","message":{"role":"user","content":"hi"}}` + "\n"))
	_ = zw.Close()
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	for _, tc := range []struct {
		name       string
		gzip       []byte
		wantResume bool
	}{
		{"readable", gz.Bytes(), true},
		{"not gzip", []byte("plain"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, RepoOriginURL: "https://github.com/acme/app"}
			fc := &fakeCommander{}
			out, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), MoveImportInput{
				ProjectID: "mer", MoveID: "m-" + tc.name, Manifest: importManifest(tip), HandoffNote: "NOTE",
				Transcript: &MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: tc.gzip},
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg := fc.spawnedCfg
			warned := strings.Contains(strings.Join(out.Warnings, "\n"), "conversation could not be read")
			if tc.wantResume {
				if cfg.Transfer == nil || cfg.Transfer.FallbackPrompt != "NOTE" || cfg.Prompt != "" || warned {
					t.Fatalf("spawn = %+v, warnings %v; want a transfer with the note as fallback", cfg, out.Warnings)
				}
				return
			}
			if cfg.Transfer != nil || cfg.Prompt != "NOTE" || !warned {
				t.Fatalf("spawn = %+v, warnings %v; want the note and a warning", cfg, out.Warnings)
			}
		})
	}
}
