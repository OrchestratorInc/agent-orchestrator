package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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
	s := &Service{store: store, home: canonical(home), roots: map[domain.AgentHarness]string{domain.HarnessClaudeCode: claude, domain.HarnessCodex: codex}}
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
			// GitHub SSH and HTTPS URLs identify the same repository. Other remotes match exactly.
			remote := strings.TrimSuffix(strings.TrimSpace(value), "/")
			if path, ok := strings.CutPrefix(remote, "file://"); ok {
				remote = path
			}
			if colon, slash := strings.Index(remote, ":"), strings.Index(remote, "/"); !strings.Contains(remote, "://") && (colon < 0 || slash >= 0 && slash < colon) {
				if !filepath.IsAbs(remote) {
					remote = filepath.Join(worktree, remote)
				}
				remote = canonical(remote)
			}
			remote = strings.TrimSuffix(remote, ".git")
			for _, prefix := range []string{"git@github.com:", "https://github.com/", "ssh://git@github.com/"} {
				remote = strings.TrimPrefix(remote, prefix)
			}
			return remote
		}
	}
	return ""
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
	if cwd == "" {
		return "", true
	}
	cwd = canonical(cwd)
	if cached, ok := d.byCWD[cwd]; ok {
		return cached.id, cached.eligible
	}
	id, eligible := d.lookup(cwd)
	d.byCWD[cwd] = destination{id, eligible}
	return id, eligible
}

func (d *destinations) lookup(cwd string) (domain.ProjectID, bool) {
	for _, excluded := range d.svc.excluded {
		if pathWithin(excluded, cwd) {
			return "", false // AO-managed worktrees already belong to AO sessions.
		}
	}
	// A deleted folder has no files to continue from.
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return "", false
	}
	best := domain.ProjectID("")
	bestLen := -1
	for _, p := range d.projects {
		if p.path != "" && pathWithin(p.path, cwd) && len(p.path) > bestLen {
			best, bestLen = p.id, len(p.path)
		}
	}
	if best != "" {
		return best, true
	}
	if d.svc.standalone(cwd) {
		return "", true
	}
	common, remote := gitIdentity(cwd)
	for _, p := range d.projects {
		if (common != "" && p.common == common) || (remote != "" && p.remote == remote) {
			if best != "" && best != p.id {
				return "", false
			} // Ambiguous destinations are not auto-adopted.
			best = p.id
		}
	}
	return best, best != ""
}

func candidateID(harness domain.AgentHarness, root, nativeID string) string {
	sum := sha256.Sum256([]byte(string(harness) + "\x00" + root + "\x00" + nativeID))
	return hex.EncodeToString(sum[:])
}

// discover lists transcripts without reading them, newest first.
func (s *Service) discover(ctx context.Context, harness domain.AgentHarness, root string) ([]sourceFiles, error) {
	byKey := map[string]*sourceFiles{}
	dir := filepath.Join(root, "projects")
	if harness == domain.HarnessCodex {
		// Archived Codex threads are not resumable without unarchiving them in Codex.
		dir = filepath.Join(root, "sessions")
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
		case domain.HarnessCodex:
			if key = codexThreadID(path); key == "" {
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

func (f sourceFiles) cacheKey() string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", f.harness, f.root, f.size, f.modTime.UnixNano(), strings.Join(f.paths, "\x00"))
}

// Scan hides missing, malformed and unreadable sources; recent means activity within 30 days.
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
	selected := make(map[string]selection)
	preview := Preview{Candidates: []Candidate{}}
	cache := map[string]parsed{}
	harnesses := []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex}
	for _, harness := range harnesses {
		configured, ok := s.roots[harness]
		if !ok {
			continue
		}
		root := canonical(configured)
		files, err := s.discover(ctx, harness, root)
		if err != nil {
			return Preview{}, err
		}
		// ponytail: every scan reads all histories (4 GiB took 27 s cold, under 1 s once
		// cached by size and mtime). Add a persistent index if that first scan grows.
		recent := time.Now().AddDate(0, 0, -recentDays)
		results := make([]parsed, len(files))
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(max(1, runtime.GOMAXPROCS(0)/2))
		for i, file := range files {
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
			cache[file.cacheKey()] = result
			if result.err != nil {
				continue // Malformed, missing and unreadable histories are ineligible.
			}
			source := result.source
			id := candidateID(harness, root, source.nativeID)
			if prior, ok := selected[id]; ok && !source.last.After(prior.source.last) {
				continue
			}
			project, eligible := dests.resolve(source.cwd)
			if !eligible {
				continue
			}
			title := source.title
			if title == "" {
				title = "Imported conversation"
			}
			c := Candidate{ID: id, Harness: harness, Title: title, ProjectID: project, LastActivity: source.last, Suggested: source.last.After(recent), MessageCount: result.count, Shortened: source.shortened}
			for _, rec := range sessions {
				if rec.Harness != harness {
					continue
				}
				if imp := rec.Metadata.ImportSource; imp != nil && imp.NativeID == source.nativeID && imp.ConfigDir == root {
					c.SessionID = rec.ID
					break
				}
				// Already managed histories are not offered for a second session.
				if rec.Metadata.AgentSessionID == source.nativeID || rec.Metadata.ProviderConversationID == source.nativeID {
					c.SessionID = rec.ID
					break
				}
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
		files, err := s.discover(ctx, rec.Harness, source.ConfigDir)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.key == source.NativeID {
				paths = file.paths
				break
			}
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
	for _, id := range ids {
		result := Result{ID: id, Status: "failed"}
		item, found := s.selections[id]
		if !found {
			result.Error = "Scan again; this selection is no longer available"
			results = append(results, result)
			continue
		}
		if item.candidate.SessionID != "" {
			result.Status = "already_imported"
			result.SessionID = item.candidate.SessionID
			results = append(results, result)
			continue
		}
		// The fresh read is what gets stored, so ordinary growth since the scan is fine.
		source, readErr := readTranscript(ctx, item.source.harness, item.source.root, item.source.paths...)
		project, eligible := dests.resolve(source.cwd)
		if readErr != nil || !eligible || project != item.candidate.ProjectID || source.nativeID != item.source.nativeID || source.cwd != item.source.cwd {
			result.Error = "The source or project changed. Scan again before importing."
			results = append(results, result)
			continue
		}
		now := time.Now().UTC()
		rec := domain.SessionRecord{ProjectID: project, Kind: domain.KindWorker, Harness: source.harness, Mode: domain.SessionModeChat, DisplayName: item.candidate.Title, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: source.last}, CreatedAt: now, UpdatedAt: source.last, ProvisionState: domain.SessionProvisionReady}
		rec.Metadata = domain.SessionMetadata{ProviderConversationID: source.nativeID, AgentSessionID: source.nativeID, NativeTranscriptPath: source.path, ImportSource: &domain.SessionImportSource{NativeID: source.nativeID, ConfigDir: source.root, CWD: source.cwd}}
		created, fresh, createErr := s.store.CreateImportedSession(ctx, rec, source.messages)
		if createErr != nil {
			result.Error = fmt.Sprintf("Import failed: %v", createErr)
		} else {
			result.SessionID = created.ID
			result.Status = "already_imported"
			if fresh {
				result.Status = "created"
			}
			item.candidate.SessionID = created.ID
			s.selections[id] = item
		}
		results = append(results, result)
		if err := ctx.Err(); err != nil {
			return results, err
		}
	}
	return results, nil
}
