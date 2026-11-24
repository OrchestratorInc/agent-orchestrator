package legacyimport

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

// Store is the narrow slice of the rewrite's native storage layer the importer
// writes through. *sqlite.Store satisfies it. Idempotency lives here: a project
// whose id already exists is skipped, never overwritten, so a re-run is safe
// and legacy files stay the sole source of truth.
type Store interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	UpsertProject(ctx context.Context, r domain.ProjectRecord) error
}

// Options configure one import run.
type Options struct {
	// Root is the legacy state root to read (default ~/.agent-orchestrator).
	Root string
	// DryRun parses + plans every row but writes nothing.
	DryRun bool
	// Now is the fallback registered_at timestamp. Zero -> time.Now().UTC().
	Now time.Time
	// RepoOriginURL resolves a repo's git origin. Nil -> the real git resolver.
	RepoOriginURL func(path string) string
}

// Report is the structured outcome of an import run.
type Report struct {
	DryRun           bool     `json:"dryRun"`
	ProjectsImported int      `json:"projectsImported"`
	ProjectsSkipped  int      `json:"projectsSkipped"` // already present
	Notes            []string `json:"notes,omitempty"`
}

// HasLegacyData reports whether root holds an importable legacy store: a
// config.yaml with at least one project. Used for the first-boot opt-in check.
func HasLegacyData(root string) bool {
	if root == "" {
		return false
	}
	cfg, err := loadLegacyConfig(root)
	if err != nil {
		return false
	}
	return len(cfg.Projects) > 0
}

// LegacyConfigError returns the parse error for root's legacy config.yaml, or
// nil if the store is absent or parsed cleanly. The CLI import path uses it to
// surface a parse failure instead of swallowing it as "no data" (issue #2186);
// HasLegacyData stays a bool for the migration-probe service layer, which must
// not error on a missing or broken store.
func LegacyConfigError(ctx context.Context, root string) error {
	if root == "" {
		return nil
	}
	_, err := loadLegacyConfig(root)
	return err
}

// rewriteProjectID gates the rewrite project-id charset (validateProjectID,
// service.go). Legacy ids are a strict subset, so this all but always passes;
// it guards against a hand-edited legacy config carrying an illegal id.
var rewriteProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isValidRewriteProjectID(id string) bool {
	return id != "" && id != "." && !strings.Contains(id, "..") &&
		!strings.ContainsAny(id, `/\`) && rewriteProjectID.MatchString(id)
}

// Run reads the legacy store and writes projects into store. It never modifies
// legacy files. It is idempotent: existing rows are skipped. A per-project
// parse or write failure is recorded as a note and does not abort the whole
// run, except a store write error, which is returned.
func Run(ctx context.Context, store Store, opts Options) (Report, error) {
	root := opts.Root
	if root == "" {
		root = DefaultLegacyRootDir()
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	resolveOrigin := opts.RepoOriginURL
	if resolveOrigin == nil {
		resolveOrigin = defaultRepoOriginURL
	}

	rep := Report{DryRun: opts.DryRun}

	cfg, err := loadLegacyConfig(root)
	if err != nil {
		return rep, err
	}
	if len(cfg.Projects) == 0 {
		rep.Notes = append(rep.Notes, "no legacy projects found at "+root)
		return rep, nil
	}

	configMtime := ""
	if info, err := os.Stat(globalConfigPath(root)); err == nil {
		configMtime = info.ModTime().UTC().Format(time.RFC3339)
	}
	prefs := loadPreferences(root)
	reg := loadRegistered(root)

	// Deterministic order: ids sorted.
	ids := make([]string, 0, len(cfg.Projects))
	for id := range cfg.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	deps := projectRowDeps{repoOriginURL: resolveOrigin, configMtime: configMtime, now: now}

	for _, id := range ids {
		pc := cfg.Projects[id]
		if !isValidRewriteProjectID(id) {
			rep.Notes = append(rep.Notes, "project "+quote(id)+" skipped: id is not a valid rewrite project id")
			continue
		}

		record, notes := buildProjectRecord(id, pc, prefs, reg, deps)
		rep.Notes = appendPrefixed(rep.Notes, id, notes)

		if err := importProject(ctx, store, record, opts.DryRun, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func importProject(ctx context.Context, store Store, record domain.ProjectRecord, dryRun bool, rep *Report) error {
	_, exists, err := store.GetProject(ctx, record.ID)
	if err != nil {
		return fmt.Errorf("lookup project %s: %w", record.ID, err)
	}
	if exists {
		rep.ProjectsSkipped++
		return nil
	}
	if dryRun {
		rep.ProjectsImported++
		return nil
	}
	if err := store.UpsertProject(ctx, record); err != nil {
		return fmt.Errorf("write project %s: %w", record.ID, err)
	}
	rep.ProjectsImported++
	return nil
}

func appendPrefixed(dst []string, id string, notes []string) []string {
	for _, n := range notes {
		dst = append(dst, id+": "+n)
	}
	return dst
}

// quote wraps s in double quotes for note messages, rendering an empty string as
// "?" so a missing value is still legible.
func quote(s string) string {
	if s == "" {
		return `"?"`
	}
	return `"` + s + `"`
}

// defaultRepoOriginURL resolves a repo's git origin URL, "" when the repo is
// absent or has no origin. Matches the rewrite's resolveGitOriginURL.
func defaultRepoOriginURL(path string) string {
	if path == "" {
		return ""
	}
	cmd := aoprocess.Command("git", "-C", path, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
