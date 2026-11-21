package session

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

func (m moveStore) SetSessionMoveStarted(_ context.Context, id domain.SessionID, moveID string, _ time.Time) (bool, error) {
	rec, ok := m.sessions[id]
	rec.MoveStarted = moveID
	m.sessions[id] = rec
	return ok, nil
}

func (m moveStore) SetSessionMoveConversationPlaced(_ context.Context, id domain.SessionID, _ time.Time) (bool, error) {
	rec, ok := m.sessions[id]
	rec.MoveConversationPlaced = true
	m.sessions[id] = rec
	return ok, nil
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
		{fmt.Errorf("%w: main", sessionmove.ErrDefaultBranch), "The session is on main, the repository's default branch. A move pushes the session's uncommitted work to its branch, so only a session on its own branch can move."},
		{fmt.Errorf("%w: git push: exit status 1: fatal: Authentication failed", sessionmove.ErrRemoteAuth), "Origin refused this computer's Git credentials. Check that you can fetch and push the repository here, then try again."},
	} {
		var got *apierr.Error
		if !errors.As(moveAPIError(tc.err), &got) || got.Message != tc.want {
			t.Errorf("moveAPIError(%v) = %v, want message %q", tc.err, got, tc.want)
		}
	}
	// Git's raw output stays out of a refusal about origin.
	var got *apierr.Error
	if errors.As(moveAPIError(fmt.Errorf("%w: fatal: secret", sessionmove.ErrRemoteUnreachable)), &got) && got.Details != nil {
		t.Errorf("origin refusal details = %v, want none", got.Details)
	}
	// One it cannot explain is a 500 whose message is not git's.
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: newFakeStore()})
	if !errors.As(svc.moveAPIError(context.Background(), errors.New("git push: fatal: raw")), &got) || got.Code != "MOVE_GIT_FAILED" || strings.Contains(got.Message, "raw") {
		t.Errorf("unexplained error = %v, want MOVE_GIT_FAILED without git's text", got)
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

func (c transcriptCommander) CloudSidecarInfo(context.Context, domain.SessionRecord, domain.ProjectRecord) (nativetransfer.ClaudeSidecar, error) {
	return nativetransfer.ClaudeSidecar{}, nil
}

func (c transcriptCommander) WriteCloudSidecar(context.Context, domain.SessionRecord, domain.ProjectRecord, io.Writer, int64) (bool, error) {
	return false, nil
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
	var sidecar bytes.Buffer
	sw := gzip.NewWriter(&sidecar)
	tw := tar.NewWriter(sw)
	sub := []byte(`{"type":"user","cwd":"/workspace/repository","message":{"role":"user","content":"sub"}}` + "\n")
	_ = tw.WriteHeader(&tar.Header{Name: "subagents/agent-1.jsonl", Typeflag: tar.TypeReg, Size: int64(len(sub)), Mode: 0o600})
	_, _ = tw.Write(sub)
	_ = tw.Close()
	_ = sw.Close()
	transcript := MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: gz.Bytes(), SidecarGzip: sidecar.Bytes()}

	archive, warning, _, err := writeImportArchive(context.Background(), t.TempDir(), domain.HarnessClaudeCode, transcript)
	if err != nil || warning != "" {
		t.Fatal(err, warning)
	}
	cwd := t.TempDir()
	res, err := nativetransfer.ImportClaude(context.Background(), mustOpen(t, archive), nativetransfer.ClaudeImportOptions{ConfigDir: t.TempDir(), LaunchCwd: cwd})
	if err != nil || res.NativeID != id {
		t.Fatalf("ImportClaude = %+v, %v", res, err)
	}
	placed, err := os.ReadFile(filepath.Join(filepath.Dir(res.TranscriptPath), id, "subagents", "agent-1.jsonl"))
	if err != nil || !strings.Contains(string(placed), `"cwd":"`) || strings.Contains(string(placed), "/workspace/repository") {
		t.Fatalf("placed subagent transcript = %q, %v", placed, err)
	}

	// A sidecar that cannot be read stays behind; the transcript still moves.
	transcript.SidecarGzip = []byte("plain")
	if _, warning, _, err := writeImportArchive(context.Background(), t.TempDir(), domain.HarnessClaudeCode, transcript); err != nil || !strings.Contains(warning, "stayed in Orchestrator Cloud") {
		t.Fatalf("unreadable sidecar: warning %q, err %v", warning, err)
	}

	for name, bad := range map[string]MoveImportTranscript{
		"not gzip":   {AgentSessionID: id, ProjectDir: transcript.ProjectDir, Gzip: []byte("plain")},
		"id as path": {AgentSessionID: "../" + id, ProjectDir: transcript.ProjectDir, Gzip: gz.Bytes()},
	} {
		if _, _, _, err := writeImportArchive(context.Background(), t.TempDir(), domain.HarnessClaudeCode, bad); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestWriteImportArchivePassesACodexRolloutAsIs(t *testing.T) {
	const id = "01a1f2eb-1c10-7462-9afc-a0787b4e965c"
	rollout := `{"type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-11-19T08:27:36.895Z","cwd":"/workspace/repository"}}` + "\n"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(rollout))
	_ = zw.Close()
	path, warning, _, err := writeImportArchive(context.Background(), t.TempDir(), domain.HarnessCodex, MoveImportTranscript{AgentSessionID: id, Gzip: gz.Bytes()})
	if err != nil || warning != "" {
		t.Fatal(err, warning)
	}
	home, cwd := t.TempDir(), t.TempDir()
	if got, err := nativetransfer.ImportCodex(context.Background(), path, nativetransfer.CodexImportOptions{Home: home, Cwd: cwd}); err != nil || got != id {
		t.Fatalf("ImportCodex = %q, %v", got, err)
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

func TestImportMoveRefusesATipThatIsNotACommitID(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	for _, tip := range []string{"HEAD", "origin/other", "--output=x", strings.Repeat("A", 40), strings.Repeat("a", 39)} {
		fc := &fakeCommander{}
		_, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), MoveImportInput{
			ProjectID: "mer", MoveID: "m-1", Manifest: importManifest(tip),
		})
		var got *apierr.Error
		if !errors.As(err, &got) || got.Code != "MOVE_MANIFEST_INVALID" || fc.spawnCalls != 0 {
			t.Fatalf("tip %q: err = %v, spawns = %d; want MOVE_MANIFEST_INVALID", tip, err, fc.spawnCalls)
		}
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

// Orchestrator Cloud's opencode is OpenCode 2: the session here runs as
// opencode-v2 and resumes the session export document as it came.
func TestImportMoveRunsCloudOpenCodeAsOpenCodeV2(t *testing.T) {
	repo, tip := pushedBranch(t)
	const id = "ses_e34d0cd52ffeMzGW05mHN4RIm7"
	doc := `{"info":{"id":"` + id + `","location":{"directory":"/workspace/repository"}},"messages":[]}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(doc))
	_ = zw.Close()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, RepoOriginURL: "https://github.com/acme/app"}
	var spawned ports.SpawnConfig
	var placed []byte
	fc := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
		spawned = cfg
		if cfg.Transfer != nil {
			placed, _ = os.ReadFile(cfg.Transfer.NativeArchive)
		}
		return domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness}
	}}
	manifest := importManifest(tip)
	manifest.Harness = domain.HarnessOpenCode
	if _, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), MoveImportInput{
		ProjectID: "mer", MoveID: "m-opencode", Manifest: manifest, HandoffNote: "NOTE",
		Transcript: &MoveImportTranscript{AgentSessionID: id, ProjectDir: "/workspace/.ao/home", Gzip: gz.Bytes()},
	}); err != nil {
		t.Fatal(err)
	}
	if spawned.Harness != domain.HarnessOpenCodeV2 || string(placed) != doc {
		t.Fatalf("spawned %s with %q, want opencode-v2 with the document", spawned.Harness, placed)
	}
}

func TestImportMoveStartsFromTheNoteWhenTheConversationCannotBeRead(t *testing.T) {
	repo, tip := pushedBranch(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"type":"user","cwd":"/workspace/repository","message":{"role":"user","content":"hi"}}` + "\n"))
	_ = zw.Close()
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	for _, tc := range []struct {
		name           string
		gzip, sidecar  []byte
		placed         bool
		wantResume     bool
		wantWarning    string
		wantIncomplete bool
	}{
		{name: "placed", gzip: gz.Bytes(), placed: true, wantResume: true},
		{name: "not placed", gzip: gz.Bytes(), wantResume: true, wantWarning: "could not be placed", wantIncomplete: true},
		{name: "sidecar unreadable", gzip: gz.Bytes(), sidecar: []byte("plain"), placed: true, wantResume: true, wantWarning: "stayed in Orchestrator Cloud", wantIncomplete: true},
		{name: "not gzip", gzip: []byte("plain"), wantWarning: "conversation could not be read", wantIncomplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, RepoOriginURL: "https://github.com/acme/app"}
			// The spawn records the conversation id once it placed the conversation.
			fc := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
				rec := domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness}
				if cfg.Transfer != nil && tc.placed {
					rec.Metadata.AgentSessionID = id
				}
				return rec
			}}
			out, err := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}}).ImportMove(context.Background(), MoveImportInput{
				ProjectID: "mer", MoveID: "m-" + strings.ReplaceAll(tc.name, " ", "-"), Manifest: importManifest(tip), HandoffNote: "NOTE",
				Transcript: &MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: tc.gzip, SidecarGzip: tc.sidecar},
			})
			if err != nil {
				t.Fatal(err)
			}
			// The fake store has no spawned session, so copying its settings warns too.
			var conversation []string
			for _, w := range out.Warnings {
				if !strings.HasPrefix(w, "Could not copy a session setting") {
					conversation = append(conversation, w)
				}
			}
			warnings := strings.Join(conversation, "\n")
			if (tc.wantWarning == "") != (warnings == "") || !strings.Contains(warnings, tc.wantWarning) {
				t.Fatalf("warnings %q, want %q", warnings, tc.wantWarning)
			}
			if out.ConversationIncomplete != tc.wantIncomplete {
				t.Fatalf("ConversationIncomplete = %v, want %v", out.ConversationIncomplete, tc.wantIncomplete)
			}
			cfg := fc.spawnedCfg
			if tc.wantResume {
				if cfg.Transfer == nil || cfg.Transfer.FallbackPrompt != "NOTE" || cfg.Prompt != "" {
					t.Fatalf("spawn = %+v; want a transfer with the note as fallback", cfg)
				}
				return
			}
			if cfg.Transfer != nil || cfg.Prompt != "NOTE" {
				t.Fatalf("spawn = %+v; want the note", cfg)
			}
		})
	}
}

// A sidecar folder that could not be placed leaves the conversation id right,
// so a replay has to read the stored result, not re-derive it from the id.
func TestImportMoveReplayKeepsAnIncompleteConversation(t *testing.T) {
	repo, tip := pushedBranch(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"type":"user","cwd":"/workspace/repository","message":{"role":"user","content":"hi"}}` + "\n"))
	_ = zw.Close()
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, RepoOriginURL: "https://github.com/acme/app"}
	// Like the manager, the spawn records the session with its client request and the placed conversation id.
	fc := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{
			ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness,
			ClientRequestID: cfg.ClientRequestID, ClientRequestHash: cfg.ClientRequestHash, ClientRequestCommitted: true,
		}
		rec.Metadata.AgentSessionID = id
		st.sessions[rec.ID] = rec
		return rec
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}})
	in := MoveImportInput{
		ProjectID: "mer", MoveID: "m-1", Manifest: importManifest(tip), HandoffNote: "NOTE",
		Transcript: &MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: gz.Bytes(), SidecarGzip: []byte("plain")},
	}
	first, err := svc.ImportMove(context.Background(), in)
	if err != nil || !first.ConversationIncomplete {
		t.Fatalf("first import: incomplete %v, err %v; want incomplete", first.ConversationIncomplete, err)
	}
	replay, err := svc.ImportMove(context.Background(), in)
	if err != nil || !replay.ConversationIncomplete || fc.spawnCalls != 1 {
		t.Fatalf("replay: incomplete %v, spawns %d, err %v; want incomplete without a second spawn", replay.ConversationIncomplete, fc.spawnCalls, err)
	}
}

// placedWriteFails stands in for a daemon that stopped, or a write that
// failed, between the spawn and recording the placement.
type placedWriteFails struct{ moveStore }

func (placedWriteFails) SetSessionMoveConversationPlaced(context.Context, domain.SessionID, time.Time) (bool, error) {
	return false, errors.New("disk I/O error")
}

// A replay counts the conversation as moved only once its placement was
// recorded after the spawn; until then the source keeps its copy.
func TestImportMoveReplayNeedsTheRecordedPlacement(t *testing.T) {
	repo, tip := pushedBranch(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"type":"user","cwd":"/workspace/repository","message":{"role":"user","content":"hi"}}` + "\n"))
	_ = zw.Close()
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	for _, tc := range []struct {
		name           string
		store          func(moveStore) Store
		wantIncomplete bool
	}{
		{"placement recorded", func(m moveStore) Store { return m }, false},
		{"placement not recorded", func(m moveStore) Store { return placedWriteFails{m} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, RepoOriginURL: "https://github.com/acme/app"}
			fc := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
				rec := domain.SessionRecord{
					ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness,
					ClientRequestID: cfg.ClientRequestID, ClientRequestHash: cfg.ClientRequestHash, ClientRequestCommitted: true,
				}
				rec.Metadata.AgentSessionID = id
				st.sessions[rec.ID] = rec
				return rec
			}}
			svc := NewWithDeps(Deps{Manager: fc, Store: tc.store(moveStore{st})})
			in := MoveImportInput{
				ProjectID: "mer", MoveID: "m-1", Manifest: importManifest(tip), HandoffNote: "NOTE",
				Transcript: &MoveImportTranscript{AgentSessionID: id, ProjectDir: "/home/agent/.claude/projects/-workspace-repository", Gzip: gz.Bytes()},
			}
			first, err := svc.ImportMove(context.Background(), in)
			if err != nil || first.ConversationIncomplete {
				t.Fatalf("first import: incomplete %v, err %v; want the conversation placed", first.ConversationIncomplete, err)
			}
			replay, err := svc.ImportMove(context.Background(), in)
			if err != nil || replay.ConversationIncomplete != tc.wantIncomplete || fc.spawnCalls != 1 {
				t.Fatalf("replay: incomplete %v, spawns %d, err %v; want incomplete %v", replay.ConversationIncomplete, fc.spawnCalls, err, tc.wantIncomplete)
			}
		})
	}
}

type failingMoveNoter struct{}

func (failingMoveNoter) MoveNote(context.Context, domain.SessionRecord, string, bool, int) (string, error) {
	return "", errors.New("note failed")
}

// A failed export leaves origin and the local branch as they were.
func TestExportMoveWritesTheNoteBeforePushing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, wt := root+"/origin.git", root+"/wt"
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "clone", "-q", origin, wt)
	git(wt, "switch", "-q", "-c", "feat/move")
	git(wt, "commit", "-q", "--allow-empty", "-m", "work")
	git(wt, "push", "-q", "origin", "feat/move")
	head := git(wt, "rev-parse", "HEAD")
	if err := os.WriteFile(wt+"/new.txt", []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityExited}, Metadata: domain.SessionMetadata{WorkspacePath: wt},
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: moveStore{st}})
	svc.SetMoveNoter(failingMoveNoter{})
	if _, err := svc.ExportMove(context.Background(), "mer-2", MoveExportInput{MoveID: "m-1", SourceLabel: "Mac"}); err == nil {
		t.Fatal("export succeeded with a failing note")
	}
	if got := git(wt, "ls-remote", "origin", "refs/heads/feat/move"); !strings.HasPrefix(got, head) {
		t.Fatalf("origin feat/move = %q, want %s", got, head)
	}
	if got := git(wt, "rev-parse", "HEAD"); got != head {
		t.Fatalf("local HEAD = %s, want %s", got, head)
	}
}

// A session on the default branch would push its uncommitted work onto it, so
// both preview and export refuse before committing or pushing anything.
func TestExportMoveRefusesTheDefaultBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, seed, wt := root+"/origin.git", root+"/seed", root+"/wt"
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "init", "-q", "-b", "main", seed)
	git(seed, "commit", "-q", "--allow-empty", "-m", "seed")
	git(seed, "push", "-q", origin, "main")
	git(root, "clone", "-q", origin, wt)
	head := git(wt, "rev-parse", "HEAD")
	if err := os.WriteFile(wt+"/dirty.txt", []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityExited}, Metadata: domain.SessionMetadata{WorkspacePath: wt},
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: moveStore{st}})
	var refusal *apierr.Error
	if _, err := svc.PreviewMove(context.Background(), "mer-2"); !errors.As(err, &refusal) || refusal.Code != "MOVE_DEFAULT_BRANCH" {
		t.Fatalf("PreviewMove err = %v, want MOVE_DEFAULT_BRANCH", err)
	}
	if _, err := svc.ExportMove(context.Background(), "mer-2", MoveExportInput{MoveID: "m-1", SourceLabel: "Mac"}); !errors.As(err, &refusal) || refusal.Code != "MOVE_DEFAULT_BRANCH" {
		t.Fatalf("ExportMove err = %v, want MOVE_DEFAULT_BRANCH", err)
	}
	if !strings.Contains(refusal.Message, "main") {
		t.Fatalf("message %q does not name the branch", refusal.Message)
	}
	if got := git(wt, "ls-remote", "origin", "refs/heads/main"); !strings.HasPrefix(got, head) {
		t.Fatalf("origin main = %q, want %s", got, head)
	}
	if got := git(wt, "rev-parse", "HEAD"); got != head {
		t.Fatalf("local HEAD = %s, want %s", got, head)
	}
	if got := git(wt, "status", "--porcelain"); got != "?? dirty.txt" {
		t.Fatalf("worktree status = %q, want the dirty file untouched", got)
	}
	if st.sessions["mer-2"].MoveStarted != "" {
		t.Fatal("a refused export recorded a move in progress")
	}
}

// An export records the move in progress before it pushes, so a restart
// during the move does not relaunch the agent here.
func TestExportMoveRecordsTheMoveBeforePushing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		args = append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin, seed, wt := root+"/origin.git", root+"/seed", root+"/wt"
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	git(root, "init", "-q", "-b", "main", seed)
	git(seed, "commit", "-q", "--allow-empty", "-m", "seed")
	git(seed, "push", "-q", origin, "main")
	git(root, "clone", "-q", origin, wt)
	git(wt, "switch", "-q", "-c", "feat/move")
	if err := os.WriteFile(wt+"/new.txt", []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app"}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityExited}, Metadata: domain.SessionMetadata{WorkspacePath: wt},
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: moveStore{st}})
	if preview, err := svc.PreviewMove(context.Background(), "mer-2"); err != nil || preview.DefaultBranch != "main" {
		t.Fatalf("PreviewMove = %+v, %v; want default branch main", preview, err)
	}
	out, err := svc.ExportMove(context.Background(), "mer-2", MoveExportInput{MoveID: "m-1", SourceLabel: "Mac"})
	if err != nil {
		t.Fatalf("ExportMove: %v", err)
	}
	if got := git(wt, "ls-remote", "origin", "refs/heads/feat/move"); !strings.HasPrefix(got, out.Manifest.Tip) {
		t.Fatalf("origin feat/move = %q, want %s", got, out.Manifest.Tip)
	}
	if got := st.sessions["mer-2"].MoveStarted; got != "m-1" {
		t.Fatalf("MoveStarted = %q, want m-1", got)
	}
}
