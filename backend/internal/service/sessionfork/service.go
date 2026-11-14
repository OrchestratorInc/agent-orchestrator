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

var ErrDisabled = errors.New("session fork is not enabled")

var ErrNotRepository = errors.New("workspace is not a git repository")

var ErrSnapshotMissing = errors.New("file snapshot missing")

var ErrSessionNotFound = errors.New("session not found")

type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

func refuse(code string) *Refusal { return &Refusal{Code: code, Message: refusalMessages[code]} }

type Source struct {
	LatestTurnID     string
	Refusal          string
	ContextMode      domain.SessionForkContextMode
	AnyTurn          bool
	FilesSnapshotRef string
	FilesAsOf        time.Time
	FilesTurnID      string
	LimitAttachments bool
	AttachmentNames  []string
	PrepareChat      func(ctx context.Context, target ports.ForkTarget) (string, error)
	PrepareTerminal  func(ctx context.Context, target ports.ForkTarget) (ports.ForkTerminalLaunch, error)
	Handoff          func(ctx context.Context, target ports.ForkTarget) (string, error)
	Freeze           func(ctx context.Context)
}

type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

type Lineage interface {
	RecordSessionFork(ctx context.Context, fork domain.SessionFork) error
	GetSessionFork(ctx context.Context, forkID domain.SessionID) (domain.SessionFork, bool, error)
	NextSessionForkNumber(ctx context.Context, rootID domain.SessionID) (int, error)
	ListSessionForks(ctx context.Context, sourceID domain.SessionID) ([]domain.SessionFork, error)
}

type PRs interface {
	ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error)
}

type Requests interface {
	GetSessionByClientRequestID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
}

type Spawner interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error)
}

type Sources interface {
	ForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (Source, error)
}

type Seeds interface {
	Capture(ctx context.Context, workspace, ref string) (headSHA string, err error)
	Delete(ctx context.Context, workspace, ref string) error
	SnapshotHead(ctx context.Context, workspace, ref string) (string, error)
}

type Accounts interface {
	SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error)
}

type Deps struct {
	Accounts Accounts
	PRs      PRs
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

type Service struct {
	deps      Deps
	numbersMu sync.Mutex
	numbers   map[domain.SessionID]map[int]bool
}

func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, numbers: map[domain.SessionID]map[int]bool{}}
}

func (s *Service) Enabled() bool { return s != nil && s.deps.Enabled }

type Options struct {
	Allowed       bool          `json:"allowed"`
	Refusal       *Refusal      `json:"refusal,omitempty"`
	SuggestedName string        `json:"suggestedName,omitempty"`
	AnyTurn       bool          `json:"anyTurn,omitempty"`
	ForkedFrom    *Origin       `json:"forkedFrom,omitempty"`
	Forks         []ForkSummary `json:"forks,omitempty"`
	SourcePRs     []SourcePR    `json:"sourcePRs,omitempty"`
}

type ForkSummary struct {
	SessionID domain.SessionID `json:"sessionId"`
	Title     string           `json:"title"`
	Exists    bool             `json:"exists"`
}

type SourcePR struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Merged bool   `json:"merged"`
	Closed bool   `json:"closed"`
}

type Origin struct {
	SessionID    domain.SessionID `json:"sessionId"`
	Title        string           `json:"title"`
	TurnID       string           `json:"turnId,omitempty"`
	SourceExists bool             `json:"sourceExists"`
	TextHandoff  bool             `json:"textHandoff,omitempty"`
}

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
	title := fork.SourceTitle
	if exists {
		title = Title(source)
	}
	return &Origin{
		SessionID: fork.SourceSessionID, Title: title, TurnID: fork.SourceTurnID, SourceExists: exists,
		TextHandoff: fork.ContextMode == domain.ForkContextTextHandoff,
	}, nil
}

func (s *Service) Fork(ctx context.Context, sourceID domain.SessionID, clientRequestID string) (domain.SessionRecord, error) {
	return s.ForkTurn(ctx, sourceID, clientRequestID, "")
}

func (s *Service) ForkTurn(ctx context.Context, sourceID domain.SessionID, clientRequestID, turnID string) (domain.SessionRecord, error) {
	if !s.Enabled() {
		return domain.SessionRecord{}, ErrDisabled
	}
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
		ProjectID:         plan.source.ProjectID,
		ClientRequestID:   clientRequestID,
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
		plan.dropSeed(ctx, s.deps.Seeds)
	} else {
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
	source   domain.SessionRecord
	agent    Source
	title    string
	root     domain.SessionID
	number   int
	name     string
	head     string
	seedRef  string
	keepSeed bool
}

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
		if err := s.requireUnchanged(ctx, rec); err != nil {
			plan.dropSeed(ctx, s.deps.Seeds)
			return forkPlan{}, err
		}
	}
	return plan, nil
}

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

func RootTitle(title string) string {
	if m := forkPrefix.FindStringSubmatch(strings.TrimSpace(title)); m != nil {
		return m[1]
	}
	return title
}

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

func nextFreeNumber(n int, held map[int]bool) int {
	for held[n] {
		n++
	}
	return n
}
