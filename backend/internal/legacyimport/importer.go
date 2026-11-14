package legacyimport

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type Store interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	UpsertProject(ctx context.Context, r domain.ProjectRecord) error
}

type Options struct {
	Root          string
	DryRun        bool
	Now           time.Time
	RepoOriginURL func(path string) string
}

type Report struct {
	DryRun           bool     `json:"dryRun"`
	ProjectsImported int      `json:"projectsImported"`
	ProjectsSkipped  int      `json:"projectsSkipped"`
	Notes            []string `json:"notes,omitempty"`
}

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

func LegacyConfigError(ctx context.Context, root string) error {
	if root == "" {
		return nil
	}
	_, err := loadLegacyConfig(root)
	return err
}

var rewriteProjectID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isValidRewriteProjectID(id string) bool {
	return id != "" && id != "." && !strings.Contains(id, "..") &&
		!strings.ContainsAny(id, `/\`) && rewriteProjectID.MatchString(id)
}

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

func quote(s string) string {
	if s == "" {
		return `"?"`
	}
	return `"` + s + `"`
}

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
