package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

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
}

// Preview is a bounded, explicit scan of this host, independent of normal board reads.
type Preview struct {
	Candidates []Candidate `json:"candidates"`
	Truncated  bool        `json:"truncated"`
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

// Service owns the current opaque handles. Re-reading selected sources prevents stale preview writes.
type Service struct {
	store      Store
	roots      map[domain.AgentHarness]string
	mu         sync.Mutex
	selections map[string]selection
}

// New uses the same provider homes as ordinary native AO launches.
func New(store Store) *Service {
	home, _ := os.UserHomeDir()
	claude, codex := os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return &Service{store: store, roots: map[domain.AgentHarness]string{domain.HarnessClaudeCode: claude, domain.HarnessCodex: codex}}
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

func gitValue(ctx context.Context, path string, args ...string) string {
	if path == "" {
		return ""
	}
	argv := append([]string{"-C", path}, args...)
	out, err := exec.CommandContext(ctx, "git", argv...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func repoIdentity(ctx context.Context, path string) string {
	common := gitValue(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return canonical(common)
}

func remoteIdentity(ctx context.Context, path string) string {
	remote := gitValue(ctx, path, "remote", "get-url", "origin")
	// GitHub SSH and HTTPS URLs identify the same repository. Other remotes match exactly.
	remote = strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	remote = strings.TrimPrefix(remote, "git@github.com:")
	remote = strings.TrimPrefix(remote, "https://github.com/")
	remote = strings.TrimPrefix(remote, "ssh://git@github.com/")
	return remote
}

func projectFor(ctx context.Context, cwd string, projects []domain.ProjectRecord) (domain.ProjectID, bool) {
	if cwd == "" {
		return "", true
	} // Truly projectless; an unknown folder is never Standalone.
	cwd = canonical(cwd)
	best := domain.ProjectID("")
	bestLen := -1
	for _, p := range projects {
		root := canonical(p.Path)
		if root != "" && (cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator))) && len(root) > bestLen {
			best, bestLen = domain.ProjectID(p.ID), len(root)
		}
	}
	if best != "" {
		return best, true
	}
	common, remote := repoIdentity(ctx, cwd), remoteIdentity(ctx, cwd)
	for _, p := range projects {
		if (common != "" && repoIdentity(ctx, p.Path) == common) || (remote != "" && remoteIdentity(ctx, p.Path) == remote) {
			if best != "" && best != domain.ProjectID(p.ID) {
				return "", false
			} // Ambiguous destinations are not auto-adopted.
			best = domain.ProjectID(p.ID)
		}
	}
	return best, best != ""
}

func (s *Service) matchingProjects(ctx context.Context) ([]domain.ProjectRecord, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	roots := append([]domain.ProjectRecord(nil), projects...)
	for _, project := range projects {
		if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
			continue
		}
		repos, err := s.store.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			child := project
			child.Path = filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
			roots = append(roots, child)
		}
	}
	return roots, nil
}

func candidateID(t transcript) string {
	sum := sha256.Sum256([]byte(string(t.harness) + "\x00" + t.root + "\x00" + t.nativeID))
	return hex.EncodeToString(sum[:])
}

// Scan hides missing, malformed and unreadable sources; recent means activity within 30 days.
func (s *Service) Scan(ctx context.Context) (Preview, error) {
	// ponytail: serialize explicit scans/imports; add a persistent index only if host scans become too slow.
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, err := s.matchingProjects(ctx)
	if err != nil {
		return Preview{}, err
	}
	sessions, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return Preview{}, err
	}
	selected := make(map[string]selection)
	preview := Preview{Candidates: []Candidate{}}
	files := 0
	var totalBytes int64
	type destination struct {
		id       domain.ProjectID
		eligible bool
	}
	destinations := map[string]destination{}
	for harness, configured := range s.roots {
		root := canonical(configured)
		dirs := []string{filepath.Join(root, "projects")}
		if harness == domain.HarnessCodex {
			dirs = []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")}
		}
		for _, dir := range dirs {
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
				if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
					return nil
				}
				files++
				if files > 10000 {
					preview.Truncated = true
					return fs.SkipAll
				}
				info, statErr := entry.Info()
				if statErr != nil {
					return nil //nolint:nilerr // Unreadable files are ineligible.
				}
				totalBytes += info.Size()
				if totalBytes > 512<<20 {
					preview.Truncated = true
					return fs.SkipAll
				}
				source, readErr := readTranscript(ctx, harness, root, path)
				if readErr != nil {
					return nil //nolint:nilerr // Malformed, missing and unreadable histories are ineligible.
				}
				id := candidateID(source)
				if prior, ok := selected[id]; ok && !source.last.After(prior.source.last) {
					return nil
				}
				dest, cached := destinations[source.cwd]
				if !cached {
					dest.id, dest.eligible = projectFor(ctx, source.cwd, projects)
					destinations[source.cwd] = dest
				}
				project, eligible := dest.id, dest.eligible
				if !eligible {
					return nil
				}
				title := "Imported conversation"
				for _, m := range source.messages {
					if m.Role == domain.MessageRoleUser {
						title = strings.Join(strings.Fields(m.Text), " ")
						break
					}
				}
				runes := []rune(title)
				if len(runes) > 100 {
					title = string(runes[:100]) + "…"
				}
				c := Candidate{ID: id, Harness: harness, Title: title, ProjectID: project, LastActivity: source.last, Suggested: source.last.After(time.Now().AddDate(0, 0, -30)), MessageCount: len(source.messages)}
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
				source.messages = nil // Retain only metadata between preview and import.
				selected[id] = selection{candidate: c, source: source}
				return nil
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return Preview{}, err
			}
		}
	}
	s.selections = selected
	for _, item := range selected {
		preview.Candidates = append(preview.Candidates, item.candidate)
	}
	sort.Slice(preview.Candidates, func(i, j int) bool {
		return preview.Candidates[i].LastActivity.After(preview.Candidates[j].LastActivity)
	})
	return preview, nil
}

// Import revalidates source contents and destination before each atomic registration.
func (s *Service) Import(ctx context.Context, ids []string) ([]Result, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return nil, apierr.Invalid("INVALID_IMPORT_SELECTION", "Select between 1 and 500 conversations", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, err := s.matchingProjects(ctx)
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
		source, readErr := readTranscript(ctx, item.source.harness, item.source.root, item.source.path)
		project, eligible := projectFor(ctx, source.cwd, projects)
		if readErr != nil || !eligible || project != item.candidate.ProjectID || source.nativeID != item.source.nativeID || source.cwd != item.source.cwd || source.fingerprint != item.source.fingerprint {
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
