package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// recentDays is the window the dialog preselects.
const recentDays = 30

// Store keeps registration and the readable copy atomic. It never launches a controller.
type Store interface {
	ListProjects(context.Context) ([]domain.ProjectRecord, error)
	ListWorkspaceRepos(context.Context, string) ([]domain.WorkspaceRepoRecord, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	CreateImportedSession(context.Context, domain.SessionRecord, []Message) (domain.SessionRecord, bool, error)
}

// Candidate exposes an opaque selection handle, never a provider file path.
type Candidate struct {
	ID           string              `json:"id"`
	Harness      domain.AgentHarness `json:"harness"`
	Title        string              `json:"title"`
	ProjectID    domain.ProjectID    `json:"projectId,omitempty"`
	LastActivity time.Time           `json:"lastActivity"`
	Suggested    bool                `json:"suggested"`
	SessionID    domain.SessionID    `json:"sessionId,omitempty"`
	MessageCount int                 `json:"messageCount"`
	// Shortened reports text too large to import; the rest of the history is kept.
	Shortened bool `json:"shortened,omitempty"`
	// Folder is the repository a chat came from when no AO project holds it.
	// The chat can be imported once that folder is added as a project.
	Folder string `json:"folder,omitempty"`
}

// Preview is a bounded, explicit scan of this host, independent of normal board reads.
type Preview struct {
	Candidates []Candidate `json:"candidates"`
}

// Result is one selected conversation's outcome; successful neighbors survive a failed item.
type Result struct {
	ID        string           `json:"id"`
	SessionID domain.SessionID `json:"sessionId,omitempty"`
	Status    string           `json:"status" enum:"created,already_imported,failed"`
	Error     string           `json:"error,omitempty"`
}

type selection struct {
	candidate Candidate
	source    transcript
}

// sourceFiles is one native conversation on disk; Codex may split it into segments.
type sourceFiles struct {
	harness domain.AgentHarness
	root    string
	paths   []string
	key     string
	modTime time.Time
	size    int64
}

type parsed struct {
	source transcript
	count  int
	err    error
}

// Service owns the current opaque handles. Re-reading selected sources prevents stale preview writes.
type Service struct {
	store      Store
	roots      map[domain.AgentHarness]string
	home       string
	excluded   []string
	mu         sync.Mutex
	selections map[string]selection
	cache      map[string]parsed
}

// New uses the same provider homes as ordinary native AO launches. AO's own
// data directory holds its managed worktrees, which are never import sources.
func New(store Store, dataDir string) *Service {
	home, _ := os.UserHomeDir()
	claude, codex := os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	pi := os.Getenv("PI_CODING_AGENT_DIR")
	if pi == "" {
		pi = filepath.Join(home, ".pi", "agent")
	}
	// Kimi Code keeps its sessions under KIMI_CODE_HOME, defaulting to ~/.kimi-code.
	kimi := os.Getenv("KIMI_CODE_HOME")
	if kimi == "" {
		kimi = filepath.Join(home, ".kimi-code")
	}
	qwen := os.Getenv(qwenHomeEnv)
	if qwen == "" {
		qwen = filepath.Join(home, ".qwen")
	}
	roots := map[domain.AgentHarness]string{domain.HarnessClaudeCode: claude, domain.HarnessCodex: codex, domain.HarnessDroid: droidRoot(home), domain.HarnessPi: pi, domain.HarnessKimi: kimi, domain.HarnessQwen: qwen}
	if opencode := openCodeRoot(home); opencode != "" {
		roots[domain.HarnessOpenCode] = opencode
	}
	s := &Service{store: store, home: canonical(home), roots: roots}
	if dataDir != "" {
		s.excluded = []string{canonical(dataDir)}
	}
	return s
}

func canonical(path string) string {
	if path == "" {
		return ""
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return physical
}

// gitIdentity reads Git metadata directly: spawning git for every transcript
// folder made scans take minutes. It returns the shared Git directory, which
// linked worktrees have in common, and the normalized origin URL.
func gitIdentity(path string) (common, remote string) {
	for dir := canonical(path); dir != ""; {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			if !info.IsDir() {
				pointer, err := os.ReadFile(dotGit)
				line := strings.TrimSpace(string(pointer))
				if err != nil || !strings.HasPrefix(line, "gitdir:") {
					return "", ""
				}
				if gitDir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:")); !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
			}
			common = gitDir
			if shared, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				if common = strings.TrimSpace(string(shared)); !filepath.IsAbs(common) {
					common = filepath.Join(gitDir, common)
				}
			}
			common = canonical(common)
			return common, originURL(filepath.Join(common, "config"), dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// originURL normalizes the origin so clones of one repository compare equal. Git
// resolves a relative path remote from the worktree folder, so this does too.
func originURL(config, worktree string) string {
	body, err := os.ReadFile(config)
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.ReplaceAll(line, " ", "") == `[remote"origin"]`
			continue
		}
		if key, value, ok := strings.Cut(line, "="); inOrigin && ok && strings.TrimSpace(key) == "url" {
			remote := strings.TrimSuffix(strings.TrimSpace(value), "/")
			if path, ok := strings.CutPrefix(remote, "file://"); ok {
				remote = path
			}
			if identity, ok := remoteIdentity(remote); ok {
				return identity
			}
			// A local path, which keeps its case: the filesystem may be case-sensitive.
			if !filepath.IsAbs(remote) {
				remote = filepath.Join(worktree, remote)
			}
			return strings.TrimSuffix(canonical(remote), ".git")
		}
	}
	return ""
}

// remoteIdentity reduces a network remote to host/path, so the SSH, scp-style
// and HTTPS forms of one repository, with or without a user, a default port or
// .git, compare equal. Hosts such as GitHub ignore case in owner and repository
// names, so the identity is lowercase. ok is false for a local path.
func remoteIdentity(remote string) (string, bool) {
	if filepath.IsAbs(remote) || filepath.VolumeName(remote) != "" {
		return "", false
	}
	var authority, path string
	if scheme, rest, found := strings.Cut(remote, "://"); found {
		switch strings.ToLower(scheme) {
		case "ssh", "git+ssh", "https", "http", "git":
		default:
			return "", false
		}
		authority, path, _ = strings.Cut(rest, "/")
		if host, port, hasPort := strings.Cut(authority[strings.LastIndex(authority, "@")+1:], ":"); hasPort {
			switch port {
			case "", "22", "80", "443":
				authority = host
			}
		}
	} else if colon, slash := strings.Index(remote, ":"), strings.Index(remote, "/"); colon > 1 && (slash < 0 || colon < slash) {
		// scp-like syntax: [user@]host:path. A one-letter "host" is a drive.
		authority, path = remote[:colon], remote[colon+1:]
	} else {
		return "", false
	}
	host := authority[strings.LastIndex(authority, "@")+1:]
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return "", false
	}
	return strings.ToLower(host + "/" + path), true
}

// SameRepository recognizes linked worktrees and separate clones of the same origin.
func SameRepository(_ context.Context, source, destination string) bool {
	sourceCommon, sourceRemote := gitIdentity(source)
	destinationCommon, destinationRemote := gitIdentity(destination)
	return sourceCommon != "" && sourceCommon == destinationCommon || sourceRemote != "" && sourceRemote == destinationRemote
}

type projectRoot struct {
	id                   domain.ProjectID
	path, common, remote string
}

// destinations resolves each project's Git identity once per scan or import.
type destinations struct {
	svc      *Service
	projects []projectRoot
	byCWD    map[string]destination
}

type destination struct {
	id       domain.ProjectID
	eligible bool
	// folder is set for a repository that no project holds yet.
	folder string
}

func (s *Service) destinations(ctx context.Context) (*destinations, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	d := &destinations{svc: s, byCWD: map[string]destination{}}
	addRoot := func(id string, path string) {
		common, remote := gitIdentity(path)
		d.projects = append(d.projects, projectRoot{id: domain.ProjectID(id), path: canonical(path), common: common, remote: remote})
	}
	for _, project := range projects {
		addRoot(project.ID, project.Path)
		if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
			continue
		}
		repos, err := s.store.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			addRoot(project.ID, filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)))
		}
	}
	return d, nil
}

// standalone recognizes folders providers use for chats that have no project:
// the home folder and Codex Desktop's per-chat folders.
func (s *Service) standalone(cwd string) bool {
	return s.home != "" && (cwd == s.home || pathWithin(filepath.Join(s.home, "Documents", "Codex"), cwd))
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *destinations) resolve(cwd string) (domain.ProjectID, bool) {
	dest := d.destination(cwd)
	return dest.id, dest.eligible
}

func (d *destinations) destination(cwd string) destination {
	if cwd == "" {
		return destination{eligible: true}
	}
	cwd = canonical(cwd)
	if cached, ok := d.byCWD[cwd]; ok {
		return cached
	}
	dest := d.lookup(cwd)
	d.byCWD[cwd] = dest
	return dest
}

func (d *destinations) lookup(cwd string) destination {
	for _, excluded := range d.svc.excluded {
		if pathWithin(excluded, cwd) {
			return destination{} // AO-managed worktrees already belong to AO sessions.
		}
	}
	// A deleted folder has no files to continue from.
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return destination{}
	}
	var within *projectRoot
	for i, p := range d.projects {
		if p.path != "" && pathWithin(p.path, cwd) && (within == nil || len(p.path) > len(within.path)) {
			within = &d.projects[i]
		}
	}
	common, remote := gitIdentity(cwd)
	same := func(p projectRoot) bool {
		return (common != "" && p.common == common) || (remote != "" && p.remote == remote)
	}
	// A folder inside a project can hold an unrelated repository, such as an
	// ignored clone; only the project's own repository belongs to the project.
	if within != nil && (within.common == "" && within.remote == "" || same(*within)) {
		return destination{id: within.id, eligible: true}
	}
	if d.svc.standalone(cwd) {
		return destination{eligible: true}
	}
	best := domain.ProjectID("")
	for _, p := range d.projects {
		if same(p) {
			if best != "" && best != p.id {
				return destination{}
			} // Ambiguous destinations are not auto-adopted.
			best = p.id
		}
	}
	if best == "" {
		return destination{folder: repositoryRoot(common)}
	}
	return destination{id: best, eligible: true}
}

// repositoryRoot is the main checkout that owns a Git directory, so chats from
// its linked worktrees offer one folder. Bare repositories have none.
func repositoryRoot(common string) string {
	if filepath.Base(common) != ".git" {
		return ""
	}
	root := filepath.Dir(common)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return ""
	}
	return root
}

var errManagedByAO = errors.New("this conversation already belongs to an AO session")

// historyOnlyImportHarnesses lists harnesses whose imports AO can only display,
// never resume: their native resume is keyed to the original folder, so a chat
// cannot be reopened from AO's worktree. Each such harness registers itself here.
//
// Qwen used to be listed here, but it now does a full copy-and-resume import (the
// transcript is relocated into the worktree's native project on first open, see
// adapters/agent/qwen/continuation.go). The map stays as the shared opt-in for any
// harness that is still read-only while its full-import support is in progress.
var historyOnlyImportHarnesses = map[domain.AgentHarness]bool{}

// historyOnlyImport reports whether a harness's imports are history-only.
func historyOnlyImport(harness domain.AgentHarness) bool {
	return historyOnlyImportHarnesses[harness]
}

func candidateID(harness domain.AgentHarness, root, nativeID string) string {
	sum := sha256.Sum256([]byte(string(harness) + "\x00" + root + "\x00" + nativeID))
	return hex.EncodeToString(sum[:])
}

// discover lists transcripts without reading them, newest first.
func (s *Service) discover(ctx context.Context, harness domain.AgentHarness, root string) ([]sourceFiles, error) {
	// Per-harness layouts that do not fit the projects/sessions directory model
	// discover their own conversations.
	switch harness {
	case domain.HarnessKimi:
		return discoverKimi(ctx, root)
	case domain.HarnessOpenCode:
		// OpenCode stores conversations in a SQLite database, not a projects tree.
		return discoverOpenCode(ctx, root)
	}
	byKey := map[string]*sourceFiles{}
	dir := filepath.Join(root, "projects")
	switch harness {
	case domain.HarnessCodex:
		// Archived Codex threads are not resumable without unarchiving them in Codex.
		dir = filepath.Join(root, "sessions")
	case domain.HarnessPi:
		// Pi stores one JSONL per conversation under sessions/<cwd-dashed>/.
		dir = filepath.Join(root, "sessions")
	case domain.HarnessDroid:
		dir = filepath.Join(root, droidSessions)
	}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		if entry.IsDir() {
			if entry.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		key := path
		switch harness {
		case domain.HarnessClaudeCode:
			if !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
		case domain.HarnessDroid:
			// One conversation per <id>.jsonl; the <id>.settings.json sidecar
			// and its .bak are not transcripts.
			if !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
		case domain.HarnessCodex:
			if key = codexThreadID(path); key == "" {
				return nil
			}
		case domain.HarnessQwen:
			if !qwenIsTranscript(path, entry.Name()) {
				return nil
			}
		case domain.HarnessPi:
			// Each Pi JSONL is one conversation; the default key (its path) groups it alone.
			if !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // Unreadable files are ineligible.
		}
		group := byKey[key]
		if group == nil {
			group = &sourceFiles{harness: harness, root: root, key: key}
			byKey[key] = group
		}
		group.paths = append(group.paths, path)
		group.size += info.Size()
		if info.ModTime().After(group.modTime) {
			group.modTime = info.ModTime()
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	out := make([]sourceFiles, 0, len(byKey))
	for _, group := range byKey {
		group.paths = codexSegments(group.paths)
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

// codexThreadSegments finds one thread's rollout files. It runs before each
// first-open attempt, so unlike discover it matches file names only: no other
// rollout is stat'ed or grouped.
func codexThreadSegments(ctx context.Context, root, threadID string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		if entry.Type().IsRegular() && codexThreadID(path) == threadID {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return codexSegments(paths), nil
}

// codexSegments orders a thread's files by their timestamped names. While
// Codex compresses a segment both copies can exist; the plain one is current.
func codexSegments(paths []string) []string {
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if n := len(out); n > 0 && path == out[n-1]+".zst" {
			continue
		}
		out = append(out, path)
	}
	return out
}

// nativeID names the conversation as readTranscript will: Claude's file name,
// Codex's thread id.
func (f sourceFiles) nativeID() string {
	// Claude, Droid and Qwen each name a conversation by its own <id>.jsonl file;
	// Codex's id is the thread recorded inside the rollout.
	if len(f.paths) > 0 && (f.harness == domain.HarnessClaudeCode || f.harness == domain.HarnessDroid || f.harness == domain.HarnessQwen) {
		return strings.TrimSuffix(filepath.Base(f.paths[0]), ".jsonl")
	}
	if f.harness == domain.HarnessPi && len(f.paths) > 0 {
		return piNativeID(f.paths[0])
	}
	return f.key
}

func (f sourceFiles) cacheKey() string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", f.harness, f.root, f.size, f.modTime.UnixNano(), strings.Join(f.paths, "\x00"))
}

// Scan hides missing, malformed and unreadable sources; recent means activity within 30 days.
// A chat from a repository that no project holds names that repository's folder.
func (s *Service) Scan(ctx context.Context) (Preview, error) {
	// ponytail: serialize explicit scans/imports; add a persistent index only if host scans become too slow.
	s.mu.Lock()
	defer s.mu.Unlock()
	dests, err := s.destinations(ctx)
	if err != nil {
		return Preview{}, err
	}
	sessions, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return Preview{}, err
	}
	// Config folder and native ID name one conversation; an agent switch changes
	// the session's harness, never the conversation it came from. Histories AO
	// already manages are not offered for a second session either.
	imported := map[[2]string]domain.SessionID{}
	managed := map[string]domain.SessionID{}
	for _, rec := range sessions {
		imp := rec.Metadata.ImportSource
		if imp != nil && rec.IsTerminated {
			// A terminated import no longer holds its conversation, which may
			// be imported again.
			continue
		}
		if imp != nil {
			imported[[2]string{imp.ConfigDir, imp.NativeID}] = rec.ID
		}
		for _, native := range []string{rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID} {
			if _, seen := managed[native]; native != "" && !seen {
				managed[native] = rec.ID
			}
		}
	}
	selected := make(map[string]selection)
	preview := Preview{Candidates: []Candidate{}}
	cache := map[string]parsed{}
	harnesses := []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessDroid, domain.HarnessOpenCode, domain.HarnessPi, domain.HarnessKimi, domain.HarnessQwen}
	for _, harness := range harnesses {
		configured, ok := s.roots[harness]
		if !ok {
			continue
		}
		root := canonical(configured)
		files, err := s.discover(ctx, harness, root)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Preview{}, ctxErr
			}
			// An unreadable provider store, such as a locked, corrupt or
			// older-schema opencode.db, hides only its own chats.
			slog.Warn("session import: skipping a provider whose history could not be read", "harness", harness, "err", err)
			continue
		}
		// ponytail: every scan reads all histories (4 GiB took 27 s cold, under 1 s once
		// cached by size and mtime). Add a persistent index if that first scan grows.
		recent := time.Now().AddDate(0, 0, -recentDays)
		results := make([]parsed, len(files))
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(max(1, runtime.GOMAXPROCS(0)/2))
		for i, file := range files {
			// AO's own sessions are never offered; skip reading their histories.
			if _, own := managed[file.nativeID()]; own {
				if _, wasImported := imported[[2]string{root, file.nativeID()}]; !wasImported {
					results[i] = parsed{err: errManagedByAO}
					continue
				}
			}
			if cached, ok := s.cache[file.cacheKey()]; ok {
				results[i] = cached
				continue
			}
			group.Go(func() error {
				source, err := readTranscript(groupCtx, harness, root, file.paths...)
				if ctxErr := groupCtx.Err(); ctxErr != nil {
					return ctxErr
				}
				count := len(source.messages)
				source.messages = nil // Retain only metadata between preview and import.
				results[i] = parsed{source: source, count: count, err: err}
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return Preview{}, err
		}
		for i, file := range files {
			result := results[i]
			if result.err != nil {
				continue // Malformed, missing and unreadable histories are ineligible; read again next scan.
			}
			cache[file.cacheKey()] = result
			source := result.source
			id := candidateID(harness, root, source.nativeID)
			if prior, ok := selected[id]; ok && !source.last.After(prior.source.last) {
				continue
			}
			dest := dests.destination(source.cwd)
			if !dest.eligible && dest.folder == "" {
				continue
			}
			title := source.title
			if title == "" {
				title = "Imported conversation"
			}
			c := Candidate{ID: id, Harness: harness, Title: title, ProjectID: dest.id, Folder: dest.folder, LastActivity: source.last, Suggested: source.last.After(recent), MessageCount: result.count, Shortened: source.shortened}
			if sessionID, ok := imported[[2]string{root, source.nativeID}]; ok {
				c.SessionID = sessionID
			} else if sessionID, ok := managed[source.nativeID]; ok {
				c.SessionID = sessionID
			}
			selected[id] = selection{candidate: c, source: source}
		}
	}
	s.selections, s.cache = selected, cache
	for _, item := range selected {
		preview.Candidates = append(preview.Candidates, item.candidate)
	}
	sort.Slice(preview.Candidates, func(i, j int) bool {
		return preview.Candidates[i].LastActivity.After(preview.Candidates[j].LastActivity)
	})
	return preview, nil
}

// RefreshArchive rereads an imported conversation just before its first open,
// so turns added in another app since the import stay in the visible history.
// The store ignores it once the session's conversation exists.
func (s *Service) RefreshArchive(ctx context.Context, rec domain.SessionRecord) error {
	source := rec.Metadata.ImportSource
	replacer, ok := s.store.(interface {
		ReplaceImportedMessages(context.Context, domain.SessionID, []Message) error
	})
	if source == nil || source.Adopted || !ok {
		return nil
	}
	paths := []string{rec.Metadata.NativeTranscriptPath}
	if rec.Harness == domain.HarnessCodex {
		// Long Codex threads continue in later segment files.
		segments, err := codexThreadSegments(ctx, source.ConfigDir, source.NativeID)
		if err != nil {
			return err
		}
		if len(segments) > 0 {
			paths = segments
		}
	}
	fresh, err := readTranscript(ctx, rec.Harness, source.ConfigDir, paths...)
	if err != nil {
		return err
	}
	if fresh.nativeID != source.NativeID || len(fresh.messages) == 0 {
		return errors.New("the source no longer holds this conversation")
	}
	return replacer.ReplaceImportedMessages(ctx, rec.ID, fresh.messages)
}

// importOne registers one selected conversation from its fresh read. s.mu is held.
func (s *Service) importOne(ctx context.Context, dests *destinations, id string, source transcript, readErr error) Result {
	result := Result{ID: id, Status: "failed"}
	// Out of time: report every row, so the dialog shows what was created.
	if ctx.Err() != nil {
		result.Error = "The import stopped before this conversation. Import it again."
		return result
	}
	item, found := s.selections[id]
	if !found {
		result.Error = "Scan again; this selection is no longer available"
		return result
	}
	if item.candidate.SessionID != "" {
		result.Status = "already_imported"
		result.SessionID = item.candidate.SessionID
		return result
	}
	project, eligible := dests.resolve(source.cwd)
	if readErr != nil || !eligible || project != item.candidate.ProjectID || source.nativeID != item.source.nativeID || source.cwd != item.source.cwd {
		result.Error = "The source or project changed. Scan again before importing."
		return result
	}
	now := time.Now().UTC()
	rec := domain.SessionRecord{ProjectID: project, Kind: domain.KindWorker, Harness: source.harness, Mode: domain.SessionModeChat, DisplayName: item.candidate.Title, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: source.last}, CreatedAt: now, UpdatedAt: source.last, ProvisionState: domain.SessionProvisionReady}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: source.nativeID, AgentSessionID: source.nativeID, NativeTranscriptPath: source.path, ImportSource: &domain.SessionImportSource{NativeID: source.nativeID, ConfigDir: source.root, CWD: source.cwd, HistoryOnly: historyOnlyImport(source.harness)}}
	// Claude and Pi resume on AO's or the user's default model unless told
	// otherwise, which may be a provider the chat never used; keep the chat's own.
	switch source.harness {
	case domain.HarnessClaudeCode:
		rec.Metadata.Model = source.model
	case domain.HarnessPi:
		rec.Metadata.Model, rec.Metadata.Effort = source.model, source.effort
	}
	created, fresh, createErr := s.store.CreateImportedSession(ctx, rec, source.messages)
	if createErr != nil {
		result.Error = fmt.Sprintf("Import failed: %v", createErr)
		return result
	}
	result.SessionID = created.ID
	result.Status = "already_imported"
	if fresh {
		result.Status = "created"
	}
	item.candidate.SessionID = created.ID
	s.selections[id] = item
	return result
}

// Import revalidates source identity and destination before each atomic registration.
func (s *Service) Import(ctx context.Context, ids []string) ([]Result, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return nil, apierr.Invalid("INVALID_IMPORT_SELECTION", "Select between 1 and 500 conversations", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dests, err := s.destinations(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(ids))
	// Histories are read in parallel, as Scan reads them, a batch at a time so
	// only one batch of full histories is held; each batch then registers in
	// order.
	batch := max(1, runtime.GOMAXPROCS(0)/2)
	for start := 0; start < len(ids); start += batch {
		chunk := ids[start:min(start+batch, len(ids))]
		type fresh struct {
			source transcript
			err    error
		}
		reads := make([]fresh, len(chunk))
		var group errgroup.Group
		for i, id := range chunk {
			item, found := s.selections[id]
			if !found || item.candidate.SessionID != "" || ctx.Err() != nil {
				continue
			}
			group.Go(func() error {
				// The fresh read is what gets stored, so ordinary growth since the scan is fine.
				reads[i].source, reads[i].err = readTranscript(ctx, item.source.harness, item.source.root, item.source.paths...)
				return nil
			})
		}
		_ = group.Wait()
		for i, id := range chunk {
			results = append(results, s.importOne(ctx, dests, id, reads[i].source, reads[i].err))
		}
	}
	return results, nil
}
