// Package sessionfork copies a session into a new session with its own
// worktree, branch, files, and the agent's native memory of the conversation.
//
// A fork copies the session as one of its turns left it: the conversation up
// to that turn, the files as they were then, and the agent's own memory.
package sessionfork

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Refusal codes. The desktop shows Message as the disabled button's tooltip.
const (
	CodeOrchestrator     = "FORK_ORCHESTRATOR"
	CodeWorkspaceProject = "FORK_WORKSPACE_PROJECT"
	CodeNotGit           = "FORK_NOT_GIT"
	CodeSourceTerminated = "FORK_SOURCE_TERMINATED"
	CodeSourceBusy       = "FORK_SOURCE_BUSY"
	CodeSourcePreparing  = "FORK_SOURCE_PREPARING"
	CodeNoFinishedTurn   = "FORK_TURN_NOT_FINISHED"
	CodeAgentUnsupported = "FORK_AGENT_UNSUPPORTED"
	CodeNativeMissing    = "FORK_NATIVE_SESSION_MISSING"
	CodeTurnFilesMissing = "FORK_TURN_FILES_MISSING"
	CodeOlderTurn        = "FORK_OLDER_TURN_UNSUPPORTED"
)

var refusalMessages = map[string]string{
	CodeOrchestrator:     "Orchestrators can't be forked.",
	CodeWorkspaceProject: "Forking isn't available for multi-repo projects.",
	CodeNotGit:           "Forking needs a git repository.",
	CodeSourceTerminated: "Restore this session to fork it.",
	CodeSourceBusy:       "Wait for the agent to go idle to fork this session.",
	CodeSourcePreparing:  "Wait for this session to finish setting up.",
	CodeNoFinishedTurn:   "This session has no finished turn to fork yet.",
	CodeAgentUnsupported: "This agent can't be forked.",
	CodeNativeMissing:    "This agent no longer has this conversation, so it can't be forked.",
	CodeTurnFilesMissing: "The files from this turn weren't saved, so it can't be forked.",
	CodeOlderTurn:        "This agent can fork only from its latest turn.",
}

// ErrDisabled means session forks are turned off for this service.
var ErrDisabled = errors.New("session fork is not enabled")

// ErrNotRepository is returned by Seeds when the workspace isn't in git.
var ErrNotRepository = errors.New("workspace is not a git repository")

// ErrSnapshotMissing means no file snapshot was saved for a turn.
var ErrSnapshotMissing = errors.New("file snapshot missing")

// ErrSessionNotFound means the source session doesn't exist.
var ErrSessionNotFound = errors.New("session not found")

// Refusal is a reason a session can't be forked right now.
type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

func refuse(code string) *Refusal { return &Refusal{Code: code, Message: refusalMessages[code]} }

// Source is what the agent layer knows about forking one session.
type Source struct {
	// LatestTurnID is the latest finished turn. Empty for terminal sessions.
	LatestTurnID string
	// Refusal is set when this agent can't fork this session.
	Refusal     string
	ContextMode domain.SessionForkContextMode
	// AnyTurn is true when this agent can fork from any finished turn.
	AnyTurn bool
	// FilesSnapshotRef holds an older turn's files, saved for rollback.
	FilesSnapshotRef string
	// FilesAsOf is when that snapshot was taken; artifacts are copied as
	// they were then.
	FilesAsOf time.Time
	// FilesTurnID is the turn that snapshot was taken for; its artifacts
	// were kept under it as it started.
	FilesTurnID string
	// LimitAttachments, for an older turn, copies only AttachmentNames: the
	// attachments the conversation had by that turn.
	LimitAttachments bool
	AttachmentNames  []string
	PrepareChat      func(ctx context.Context, target ports.ForkTarget) (string, error)
	PrepareTerminal  func(ctx context.Context, target ports.ForkTarget) (ports.ForkTerminalLaunch, error)
	// Handoff builds the text copy of the conversation a text-handoff fork's
	// fresh agent starts with.
	Handoff func(ctx context.Context, target ports.ForkTarget) (string, error)
	// Freeze, when set, is called just before a fork captures the source's
	// files, so what Handoff copies later matches them. Checking whether a
	// session can be forked doesn't call it.
	Freeze func(ctx context.Context)
}

// Sessions reads session records.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Projects reads project records.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Lineage stores where forks came from.
type Lineage interface {
	RecordSessionFork(ctx context.Context, fork domain.SessionFork) error
	GetSessionFork(ctx context.Context, forkID domain.SessionID) (domain.SessionFork, bool, error)
	NextSessionForkNumber(ctx context.Context, rootID domain.SessionID) (int, error)
	ListSessionForks(ctx context.Context, sourceID domain.SessionID) ([]domain.SessionFork, error)
}

// PRs lists the pull requests a session owns.
type PRs interface {
	ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error)
}

// Requests finds the session an earlier request made.
type Requests interface {
	GetSessionByClientRequestID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
}

// Spawner creates sessions.
type Spawner interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error)
}

// Sources asks the agent layer how a session can be forked.
type Sources interface {
	ForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (Source, error)
}

// Seeds captures the source's uncommitted and untracked files.
type Seeds interface {
	// Capture records the workspace's files under ref and returns HEAD.
	Capture(ctx context.Context, workspace, ref string) (headSHA string, err error)
	Delete(ctx context.Context, workspace, ref string) error
	// SnapshotHead is the commit a saved file snapshot was taken on, or
	// ErrSnapshotMissing.
	SnapshotHead(ctx context.Context, workspace, ref string) (string, error)
}

// Accounts reads which provider account a session runs under.
type Accounts interface {
	SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error)
}

// Deps wires the service.
type Deps struct {
	// Accounts is optional. The fork runs under the source's account, whose
	// store holds the agent's own copy of the conversation.
	Accounts Accounts
	// PRs is optional; it lists a fork's original's PRs, read-only.
	PRs PRs
	// Requests is optional; with it, a retried request returns the fork it
	// made before the original is checked again.
	Requests Requests
	Enabled  bool
	Sessions Sessions
	Projects Projects
	Lineage  Lineage
	Spawner  Spawner
	Sources  Sources
	Seeds    Seeds
	Now      func() time.Time
}

// Service forks sessions.
type Service struct {
	deps Deps
	// numbers holds the fork numbers taken by forks still being made, per
	// root: a fork is recorded only once its spawn returns, so two forks
	// started together would otherwise read the same next number.
	numbersMu sync.Mutex
	numbers   map[domain.SessionID]map[int]bool
}

// New builds the service.
func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, numbers: map[domain.SessionID]map[int]bool{}}
}

// Enabled reports whether session forks are on.
func (s *Service) Enabled() bool { return s != nil && s.deps.Enabled }

// Options says whether a session can be forked right now.
type Options struct {
	Allowed bool     `json:"allowed"`
	Refusal *Refusal `json:"refusal,omitempty"`
	// SuggestedName is what the fork will be called.
	SuggestedName string `json:"suggestedName,omitempty"`
	// AnyTurn is true when every finished turn can be forked, not just the
	// latest.
	AnyTurn bool `json:"anyTurn,omitempty"`
	// ForkedFrom is set when this session is itself a fork.
	ForkedFrom *Origin `json:"forkedFrom,omitempty"`
	// Forks are the sessions forked from this one.
	Forks []ForkSummary `json:"forks,omitempty"`
	// SourcePRs are the original's pull requests, shown read-only in a fork.
	SourcePRs []SourcePR `json:"sourcePRs,omitempty"`
}

// ForkSummary is one session forked from this one.
type ForkSummary struct {
	SessionID domain.SessionID `json:"sessionId"`
	Title     string           `json:"title"`
	// Exists is false once the fork has been deleted.
	Exists bool `json:"exists"`
}

// SourcePR is one of a fork's original's pull requests.
type SourcePR struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Merged bool   `json:"merged"`
	Closed bool   `json:"closed"`
}

// Origin is the session a fork was copied from.
type Origin struct {
	SessionID domain.SessionID `json:"sessionId"`
	Title     string           `json:"title"`
	TurnID    string           `json:"turnId,omitempty"`
	// SourceExists is false once the original has been deleted.
	SourceExists bool `json:"sourceExists"`
	// TextHandoff is true when the fork's agent couldn't copy the original's
	// conversation and was given it as text instead.
	TextHandoff bool `json:"textHandoff,omitempty"`
}

// Options reports whether the session can be forked, and why not.
func (s *Service) Options(ctx context.Context, sourceID domain.SessionID) (Options, error) {
	if !s.Enabled() {
		return Options{}, ErrDisabled
	}
	links, err := s.links(ctx, sourceID)
	if err != nil {
		return Options{}, err
	}
	plan, err := s.plan(ctx, sourceID, "", false)
	var refusal *Refusal
	if errors.As(err, &refusal) {
		links.Refusal = refusal
		return links, nil
	}
	if err != nil {
		return Options{}, err
	}
	links.Allowed, links.SuggestedName, links.AnyTurn = true, plan.name, plan.agent.AnyTurn
	return links, nil
}

// links fills in where a session came from and what was forked from it.
func (s *Service) links(ctx context.Context, id domain.SessionID) (Options, error) {
	var out Options
	origin, err := s.origin(ctx, id)
	if err != nil {
		return Options{}, err
	}
	out.ForkedFrom = origin
	if origin != nil && origin.SourceExists && s.deps.PRs != nil {
		prs, err := s.deps.PRs.ListPRFactsForSession(ctx, origin.SessionID)
		if err != nil {
			return Options{}, err
		}
		for _, pr := range prs {
			out.SourcePRs = append(out.SourcePRs, SourcePR{URL: pr.URL, Number: pr.Number, Merged: pr.Merged, Closed: pr.Closed})
		}
	}
	forks, err := s.deps.Lineage.ListSessionForks(ctx, id)
	if err != nil {
		return Options{}, err
	}
	for _, fork := range forks {
		summary := ForkSummary{SessionID: fork.ForkSessionID, Title: ForkDisplayName(fork.ForkNumber, RootTitle(fork.SourceTitle))}
		if rec, found, err := s.deps.Sessions.GetSession(ctx, fork.ForkSessionID); err != nil {
			return Options{}, err
		} else if found {
			summary.Title, summary.Exists = Title(rec), true
		}
		out.Forks = append(out.Forks, summary)
	}
	return out, nil
}

func (s *Service) origin(ctx context.Context, id domain.SessionID) (*Origin, error) {
	fork, found, err := s.deps.Lineage.GetSessionFork(ctx, id)
	if err != nil || !found {
		return nil, err
	}
	source, exists, err := s.deps.Sessions.GetSession(ctx, fork.SourceSessionID)
	if err != nil {
		return nil, err
	}
	// The original's current name; the one stored at fork time is kept for
	// when it has been deleted.
	title := fork.SourceTitle
	if exists {
		title = Title(source)
	}
	return &Origin{
		SessionID: fork.SourceSessionID, Title: title, TurnID: fork.SourceTurnID, SourceExists: exists,
		TextHandoff: fork.ContextMode == domain.ForkContextTextHandoff,
	}, nil
}

// Fork forks the session at its latest finished turn. The same
// clientRequestID returns the same session.
func (s *Service) Fork(ctx context.Context, sourceID domain.SessionID, clientRequestID string) (domain.SessionRecord, error) {
	return s.ForkTurn(ctx, sourceID, clientRequestID, "")
}

// ForkTurn forks the session as it was at turnID, or at its latest finished
// turn when turnID is empty.
func (s *Service) ForkTurn(ctx context.Context, sourceID domain.SessionID, clientRequestID, turnID string) (domain.SessionRecord, error) {
	if !s.Enabled() {
		return domain.SessionRecord{}, ErrDisabled
	}
	// A retry returns the fork the first request made, before the original
	// is checked: by now it may be busy, archived, or gone.
	if rec, found, err := s.replay(ctx, sourceID, clientRequestID); err != nil || found {
		return rec, err
	}
	plan, err := s.plan(ctx, sourceID, turnID, true)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	release := s.takeNumber(&plan)
	defer release()
	cfg := ports.SpawnConfig{
		ProjectID:       plan.source.ProjectID,
		ClientRequestID: clientRequestID,
		// Spawn replays a request id only with the same hash; a retry forks
		// the same source.
		ClientRequestHash: forkRequestHash(plan.source.ID),
		Kind:              domain.KindWorker,
		Harness:           plan.source.Harness,
		AgentConfig: ports.AgentConfig{
			Model: plan.source.Metadata.Model, Effort: plan.source.Metadata.Effort, Permissions: plan.source.Metadata.Permissions,
		},
		EffortOverride:  true,
		RequestedMode:   domain.NormalizeSessionMode(plan.source.Mode),
		DisplayName:     plan.name,
		KeepDisplayName: true,
		Async:           domain.NormalizeSessionMode(plan.source.Mode) == domain.SessionModeChat,
		Fork:            plan.spawnFork(),
	}
	if s.deps.Accounts != nil {
		account, err := s.deps.Accounts.SessionAccountID(ctx, plan.source.ID, plan.source.Harness)
		if err != nil {
			plan.dropSeed(ctx, s.deps.Seeds)
			return domain.SessionRecord{}, fmt.Errorf("read source account: %w", err)
		}
		cfg.AgentAccountID = account
	}
	rec, _, _, err := s.deps.Spawner.Spawn(ctx, cfg)
	if err != nil {
		plan.dropSeed(ctx, s.deps.Seeds)
		return domain.SessionRecord{}, err
	}
	if _, found, err := s.deps.Lineage.GetSessionFork(ctx, rec.ID); err != nil {
		return domain.SessionRecord{}, err
	} else if found {
		// A retried request returned the first fork; this snapshot is unused.
		plan.dropSeed(ctx, s.deps.Seeds)
	} else {
		// The session exists now, so its lineage is written even if the
		// request is cancelled. Should this write fail, retrying the same
		// request returns the same session and records it then.
		if err := s.deps.Lineage.RecordSessionFork(context.WithoutCancel(ctx), domain.SessionFork{
			ForkSessionID: rec.ID, SourceSessionID: plan.source.ID, RootSessionID: plan.root,
			SourceTurnID: plan.agent.LatestTurnID, SourceTitle: plan.title, ContextMode: plan.agent.ContextMode,
			ForkNumber: plan.number, CreatedAt: s.deps.Now().UTC(),
		}); err != nil {
			return domain.SessionRecord{}, err
		}
	}
	return rec, nil
}

// replay finds the fork an earlier request with the same id made from the
// same source. A request that is unfinished, made for something else, or
// whose fork wasn't recorded yet goes through Spawn, which handles each.
func (s *Service) replay(ctx context.Context, sourceID domain.SessionID, clientRequestID string) (domain.SessionRecord, bool, error) {
	if clientRequestID == "" || s.deps.Requests == nil {
		return domain.SessionRecord{}, false, nil
	}
	rec, found, err := s.deps.Requests.GetSessionByClientRequestID(ctx, clientRequestID)
	if err != nil || !found || !rec.ClientRequestCommitted || rec.ClientRequestHash != forkRequestHash(sourceID) {
		return domain.SessionRecord{}, false, err
	}
	if _, recorded, err := s.deps.Lineage.GetSessionFork(ctx, rec.ID); err != nil || !recorded {
		return domain.SessionRecord{}, false, err
	}
	return rec, true, nil
}

// RetrySpawnFork rebuilds the fork step for a fork whose start failed: it
// copies the original's files and native conversation again.
func (s *Service) RetrySpawnFork(ctx context.Context, fork domain.SessionRecord) (*ports.SpawnFork, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	lineage, found, err := s.deps.Lineage.GetSessionFork(ctx, fork.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("session %s is not a fork", fork.ID)
	}
	plan, err := s.plan(ctx, lineage.SourceSessionID, lineage.SourceTurnID, true)
	if errors.Is(err, ErrSessionNotFound) {
		return nil, errors.New("the original session was deleted, so this fork can't be made again")
	}
	if err != nil {
		return nil, err
	}
	return plan.spawnFork(), nil
}

type forkPlan struct {
	source  domain.SessionRecord
	agent   Source
	title   string
	root    domain.SessionID
	number  int
	name    string
	head    string
	seedRef string
	// keepSeed marks a seed the fork borrowed and must not delete.
	keepSeed bool
}

// dropSeed deletes a seed the fork captured itself; a borrowed one is kept.
func (p forkPlan) dropSeed(ctx context.Context, seeds Seeds) {
	if p.keepSeed || p.seedRef == "" {
		return
	}
	_ = seeds.Delete(context.WithoutCancel(ctx), p.source.Metadata.WorkspacePath, p.seedRef)
}

func (p forkPlan) spawnFork() *ports.SpawnFork {
	return &ports.SpawnFork{
		SourceSessionID:  p.source.ID,
		StartPoint:       p.head,
		SeedRef:          p.seedRef,
		KeepSeedRef:      p.keepSeed,
		SourceTitle:      p.title,
		SourceWorkspace:  p.source.Metadata.WorkspacePath,
		ArtifactsAsOf:    p.agent.FilesAsOf,
		ArtifactsTurnID:  p.agent.FilesTurnID,
		LimitAttachments: p.agent.LimitAttachments,
		AttachmentNames:  p.agent.AttachmentNames,
		PrepareChat:      p.agent.PrepareChat,
		PrepareTerminal:  p.agent.PrepareTerminal,
		Handoff:          p.agent.Handoff,
	}
}

// plan checks every rule. With capture set it also records the source's files.
func (s *Service) plan(ctx context.Context, sourceID domain.SessionID, turnID string, capture bool) (forkPlan, error) {
	rec, found, err := s.deps.Sessions.GetSession(ctx, sourceID)
	if err != nil {
		return forkPlan{}, err
	}
	if !found {
		return forkPlan{}, ErrSessionNotFound
	}
	if rec.Kind == domain.KindOrchestrator {
		return forkPlan{}, refuse(CodeOrchestrator)
	}
	if rec.IsTerminated {
		return forkPlan{}, refuse(CodeSourceTerminated)
	}
	if rec.ProjectID != "" {
		project, found, err := s.deps.Projects.GetProject(ctx, string(rec.ProjectID))
		if err != nil {
			return forkPlan{}, err
		}
		if found && project.Kind.WithDefault() == domain.ProjectKindWorkspace {
			return forkPlan{}, refuse(CodeWorkspaceProject)
		}
	}
	if rec.ProvisionState != "" && rec.ProvisionState != domain.SessionProvisionReady {
		return forkPlan{}, refuse(CodeSourcePreparing)
	}
	if strings.TrimSpace(rec.Metadata.WorkspacePath) == "" {
		return forkPlan{}, refuse(CodeNotGit)
	}
	if rec.Activity.State == domain.ActivityActive {
		return forkPlan{}, refuse(CodeSourceBusy)
	}
	agent, err := s.deps.Sources.ForkSource(ctx, rec, turnID)
	if err != nil {
		return forkPlan{}, err
	}
	if agent.Refusal != "" {
		return forkPlan{}, refuse(agent.Refusal)
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat && agent.LatestTurnID == "" {
		return forkPlan{}, refuse(CodeNoFinishedTurn)
	}
	plan := forkPlan{source: rec, agent: agent, title: Title(rec), root: rec.ID}
	if lineage, found, err := s.deps.Lineage.GetSessionFork(ctx, rec.ID); err != nil {
		return forkPlan{}, err
	} else if found {
		plan.root = lineage.RootSessionID
	}
	if plan.number, err = s.deps.Lineage.NextSessionForkNumber(ctx, plan.root); err != nil {
		return forkPlan{}, err
	}
	s.numbersMu.Lock()
	plan.number = nextFreeNumber(plan.number, s.numbers[plan.root])
	s.numbersMu.Unlock()
	plan.name = ForkDisplayName(plan.number, RootTitle(plan.title))
	if !capture {
		if _, err := s.deps.Seeds.Capture(ctx, rec.Metadata.WorkspacePath, ""); errors.Is(err, ErrNotRepository) {
			return forkPlan{}, refuse(CodeNotGit)
		}
		return plan, nil
	}
	if agent.Freeze != nil {
		agent.Freeze(ctx)
	}
	if agent.FilesSnapshotRef != "" {
		// An older turn: its files are the snapshot taken before the turn
		// after it, saved for rollback. The fork reads it and never deletes it.
		plan.head, err = s.deps.Seeds.SnapshotHead(ctx, rec.Metadata.WorkspacePath, agent.FilesSnapshotRef)
		if errors.Is(err, ErrSnapshotMissing) {
			return forkPlan{}, refuse(CodeTurnFilesMissing)
		}
		if err != nil {
			return forkPlan{}, fmt.Errorf("read turn files: %w", err)
		}
		plan.seedRef, plan.keepSeed = agent.FilesSnapshotRef, true
		return plan, nil
	}
	plan.seedRef = "refs/ao/fork-seeds/" + randomID()
	plan.head, err = s.deps.Seeds.Capture(ctx, rec.Metadata.WorkspacePath, plan.seedRef)
	if errors.Is(err, ErrNotRepository) {
		return forkPlan{}, refuse(CodeNotGit)
	}
	if err != nil {
		return forkPlan{}, fmt.Errorf("capture source files: %w", err)
	}
	if agent.Freeze != nil {
		// What Freeze kept must match the files: a source that acted since
		// could have changed them in a way its copied conversation misses.
		if err := s.requireUnchanged(ctx, rec); err != nil {
			plan.dropSeed(ctx, s.deps.Seeds)
			return forkPlan{}, err
		}
	}
	return plan, nil
}

// requireUnchanged refuses the fork if the source has become busy or done
// anything since rec was read.
func (s *Service) requireUnchanged(ctx context.Context, rec domain.SessionRecord) error {
	now, found, err := s.deps.Sessions.GetSession(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !found {
		return ErrSessionNotFound
	}
	if now.Activity.State == domain.ActivityActive || !now.Activity.LastActivityAt.Equal(rec.Activity.LastActivityAt) {
		return refuse(CodeSourceBusy)
	}
	return nil
}

// Title is the name a session shows in the sidebar.
func Title(rec domain.SessionRecord) string {
	if name := strings.TrimSpace(rec.DisplayName); name != "" {
		return name
	}
	if prompt := strings.TrimSpace(rec.Metadata.Prompt); prompt != "" {
		line, _, _ := strings.Cut(prompt, "\n")
		return strings.TrimSpace(line)
	}
	return string(rec.ID)
}

var forkPrefix = regexp.MustCompile(`^Fork (?:\d+ )?of (.+)$`)

// RootTitle strips a "Fork of" or "Fork N of" prefix, so forks of forks never
// read "Fork of Fork of".
func RootTitle(title string) string {
	if m := forkPrefix.FindStringSubmatch(strings.TrimSpace(title)); m != nil {
		return m[1]
	}
	return title
}

// ForkDisplayName names the nth fork of a session.
func ForkDisplayName(number int, rootTitle string) string {
	if number <= 1 {
		return "Fork of " + rootTitle
	}
	return fmt.Sprintf("Fork %d of %s", number, rootTitle)
}

func forkRequestHash(source domain.SessionID) string {
	sum := sha256.Sum256([]byte("session-fork\x00" + string(source)))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// takeNumber holds plan's fork number, moving it past any held by forks of the
// same root still being made, until the fork is recorded or fails.
func (s *Service) takeNumber(plan *forkPlan) (release func()) {
	s.numbersMu.Lock()
	defer s.numbersMu.Unlock()
	held := s.numbers[plan.root]
	if held == nil {
		held = map[int]bool{}
		s.numbers[plan.root] = held
	}
	plan.number = nextFreeNumber(plan.number, held)
	plan.name = ForkDisplayName(plan.number, RootTitle(plan.title))
	held[plan.number] = true
	root, number := plan.root, plan.number
	return func() {
		s.numbersMu.Lock()
		defer s.numbersMu.Unlock()
		delete(s.numbers[root], number)
		if len(s.numbers[root]) == 0 {
			delete(s.numbers, root)
		}
	}
}

// nextFreeNumber is the first number from n that no fork in progress holds.
func nextFreeNumber(n int, held map[int]bool) int {
	for held[n] {
		n++
	}
	return n
}
