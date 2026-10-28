// Package projectsnapshot prepares a VM snapshot per repository and harness:
// the harness snapshot with the repository already cloned. A session that
// boots from it only fetches what changed instead of cloning, which is what
// makes startup independent of repository size. Snapshots are shared by every
// project in an organization on the same repository and outlive them, so a
// project deleted and added again starts warm; one nobody boots from for
// IdleRetention is collected.
//
// Snapshots are built in a throwaway VM, never from a live session: a snapshot
// copies memory and disk, so building from a session would carry its worker
// token, credentials, and conversation into every later session. The clone
// token reaches the builder only through the clone process's environment.
package projectsnapshot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	// DefaultMaxAge rebuilds a snapshot once it is a day old, so the fetch a
	// session does at start stays small. An older snapshot is still used while
	// its replacement builds.
	DefaultMaxAge = 24 * time.Hour
	// IdleRetention is how long a snapshot no session has booted from is kept.
	// Active repositories use theirs on every session; this mostly reclaims
	// snapshots whose projects were deleted and never added back.
	IdleRetention = 7 * 24 * time.Hour
	// DefaultMaxConcurrentBuilds is how many builder VMs one replica runs at
	// once by default.
	DefaultMaxConcurrentBuilds = 2
	// collectInterval is how often idle snapshots are looked for, and stale
	// ones refreshed.
	collectInterval = time.Hour
	// refreshRetryInterval is how long Refresh waits for a free build slot.
	refreshRetryInterval = 5 * time.Second
	// warmTimeout bounds warming one snapshot.
	warmTimeout = 3 * time.Minute
	// collectDeleteTimeout bounds one provider snapshot delete. The record
	// stays locked meanwhile, so a lookup racing it waits at most this long.
	collectDeleteTimeout = 30 * time.Second
	// MarkerFile, inside the cloned repository's .git directory, tells the
	// worker the checkout came from a project snapshot and should move to the
	// latest origin/HEAD once, rather than keep the commit it was baked at.
	MarkerFile    = "ao-project-snapshot" // must equal worker.ProjectSnapshotMarker
	buildTimeout  = 15 * time.Minute
	execTimeout   = 4*time.Minute + 30*time.Second
	repositoryDir = "/workspace/repository"
	workerUser    = "ao-worker"
)

// VMs is the provider surface a build needs.
type VMs interface {
	Create(context.Context, sandbox.Spec) (sandbox.Environment, error)
	Delete(context.Context, sandbox.ID) error
	ExecRoot(ctx context.Context, id sandbox.ID, command string, env map[string]string, timeout time.Duration) (string, error)
	Snapshot(ctx context.Context, id sandbox.ID, slug string) (string, error)
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// Store persists the snapshot per repository and harness.
type Store interface {
	UseRepositorySandboxSnapshot(ctx context.Context, orgID, repositoryKey, provider, harness string) (domain.RepositorySandboxSnapshot, bool, error)
	RepositorySandboxSnapshot(ctx context.Context, orgID, repositoryKey, provider, harness string) (domain.RepositorySandboxSnapshot, bool, error)
	// ReplaceRepositorySandboxSnapshot returns the replaced snapshot only when
	// no live session's plan references it; a referenced one is retained for
	// the collector.
	ReplaceRepositorySandboxSnapshot(ctx context.Context, snapshot domain.RepositorySandboxSnapshot) (string, error)
	// CollectIdleRepositorySandboxSnapshots calls remove for each idle,
	// unreferenced snapshot and deletes its record only once remove succeeds.
	CollectIdleRepositorySandboxSnapshots(
		ctx context.Context, provider string, cutoff time.Time,
		remove func(ctx context.Context, snapshotID string) error,
	) ([]string, error)
	// AcquireRepositorySnapshotBuild claims one snapshot's build across
	// control-plane replicas; ok is false while another replica builds it.
	AcquireRepositorySnapshotBuild(
		ctx context.Context, orgID, repositoryKey, provider, harness string,
	) (release func(), ok bool, err error)
}

// Grants issues a short-lived clone token for a session's repository.
type Grants interface {
	IssueCheckoutGrant(ctx context.Context, orgID, sessionID string) (githubapp.CheckoutGrant, error)
}

// ProjectGrants issues a clone token for a project's repository when no
// session triggered the build (Ensure at connect, Refresh after a deploy).
type ProjectGrants interface {
	IssueProjectCheckoutGrant(ctx context.Context, orgID, projectID string) (githubapp.CheckoutGrant, error)
}

// RefreshStore lists the snapshots Refresh considers (postgres.Store).
type RefreshStore interface {
	RecentRepositorySandboxSnapshots(ctx context.Context, provider string, usedSince time.Time) ([]postgres.RepositorySnapshotRefresh, error)
}

// Request identifies the snapshot one session would use. SessionID is the
// session that triggered the build; its checkout grant authorizes the clone.
// RepositoryKey and RepositoryIdentity come from NewRequest.
type Request struct {
	OrgID              string
	ProjectID          string
	SessionID          string
	Harness            string
	BaseSnapshotID     string
	RepositoryURL      string
	RepositoryKey      string
	RepositoryIdentity string
}

// NewRequest describes the snapshot a session in project would use. ok is
// false when the project's repository is not a GitHub repository a snapshot
// can be keyed by, in which case its sessions clone as before.
func NewRequest(orgID string, project domain.Project, harness, baseSnapshotID string) (Request, bool) {
	identity, err := worker.GitHubRepositoryIdentity(project.RepositoryURL)
	if err != nil {
		return Request{}, false
	}
	// The repository id survives renames and transfers, and a deleted
	// repository recreated under the same name gets a new one. Projects
	// without one (anonymous public clones) fall back to the name.
	key := "url:" + identity
	if project.GitHubRepositoryID != nil {
		key = "github:" + strconv.FormatInt(*project.GitHubRepositoryID, 10)
	}
	return Request{
		OrgID:              orgID,
		ProjectID:          project.ID,
		Harness:            harness,
		BaseSnapshotID:     baseSnapshotID,
		RepositoryURL:      project.RepositoryURL,
		RepositoryKey:      key,
		RepositoryIdentity: identity,
	}, true
}

func (r Request) key() string {
	return r.OrgID + "/" + r.RepositoryKey + "/" + r.Harness
}

// Config configures a Builder.
type Config struct {
	Provider string
	VMs      VMs
	Store    Store
	// Grants may be nil, in which case only AllowAnonymous public clones work.
	Grants         Grants
	AllowAnonymous bool
	MaxAge         time.Duration
	// MaxConcurrentBuilds caps the builder VMs this replica runs at once.
	// Builder VMs count against the provider account's VM limit like session
	// VMs do, so an unbounded burst of builds could starve sessions.
	MaxConcurrentBuilds int
	// BaseSnapshot returns the harness snapshot new sessions on harness boot
	// from, or "" when there is none. Refresh needs it to tell a snapshot built
	// on an older base (before a deploy rebaked the harness snapshots).
	BaseSnapshot func(harness string) string
	// WarmSnapshots warms each repository snapshot once it is built, and
	// BaseHarnesses' harness snapshots at start (see Warm).
	WarmSnapshots bool
	BaseHarnesses []string
	Logger        *slog.Logger
}

// Builder looks up and (re)builds project snapshots.
type Builder struct {
	config   Config
	mu       sync.Mutex
	inflight map[string]bool
	warmed   map[string]bool
	now      func() time.Time
}

// New creates a Builder.
func New(config Config) *Builder {
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultMaxAge
	}
	if config.MaxConcurrentBuilds <= 0 {
		config.MaxConcurrentBuilds = DefaultMaxConcurrentBuilds
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Builder{config: config, inflight: map[string]bool{}, warmed: map[string]bool{}, now: time.Now}
}

// Lookup returns the snapshot a new session should boot from, and marks it
// used. It is only usable when it was built on the current harness snapshot
// and cloned from the repository the project names now; otherwise the session
// boots from the harness snapshot and Ensure rebuilds.
func (b *Builder) Lookup(ctx context.Context, request Request) (string, bool) {
	snapshot, ok, err := b.config.Store.UseRepositorySandboxSnapshot(
		ctx, request.OrgID, request.RepositoryKey, b.config.Provider, request.Harness)
	if err != nil {
		b.config.Logger.Warn("look up project snapshot", "project_id", request.ProjectID, "error", err)
		return "", false
	}
	if !ok || !usable(snapshot, request) {
		return "", false
	}
	return snapshot.SnapshotID, true
}

// usable reports whether snapshot can serve request. The worker refuses a
// checkout whose origin is not the session's repository, so a snapshot cloned
// before a rename or transfer must be rebuilt, not booted.
func usable(snapshot domain.RepositorySandboxSnapshot, request Request) bool {
	return snapshot.BaseSnapshotID == request.BaseSnapshotID &&
		snapshot.RepositoryIdentity == request.RepositoryIdentity
}

// Ensure starts a background build when the project has no usable snapshot or
// it is older than MaxAge. At most one build runs per project and harness.
func (b *Builder) Ensure(request Request) {
	// A build skipped here is not lost: every session create calls Ensure, so
	// the next one starts it once a slot is free.
	b.start(request)
}

// start begins Ensure's background build, reporting false when a build for
// the same snapshot is running or every build slot is taken.
func (b *Builder) start(request Request) bool {
	key := request.key()
	b.mu.Lock()
	if b.inflight[key] || len(b.inflight) >= b.config.MaxConcurrentBuilds {
		b.mu.Unlock()
		return false
	}
	b.inflight[key] = true
	b.mu.Unlock()
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.inflight, key)
			b.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		// The in-process guard above covers this replica; the store claim
		// covers the others. It is taken before the needs-build check so a
		// replica that loses the race sees the snapshot the winner recorded.
		release, claimed, err := b.config.Store.AcquireRepositorySnapshotBuild(
			ctx, request.OrgID, request.RepositoryKey, b.config.Provider, request.Harness)
		if err != nil {
			b.config.Logger.Warn("claim project snapshot build",
				"project_id", request.ProjectID, "harness", request.Harness, "error", err)
			return
		}
		if !claimed {
			return
		}
		defer release()
		if !b.needsBuild(ctx, request) {
			return
		}
		started := b.now()
		snapshotID, err := b.build(ctx, request)
		if errors.Is(err, sandbox.ErrAtCapacity) {
			// The account is at its VM limit (builder VMs count against it).
			// Sessions take priority; the next session create retries.
			b.config.Logger.Info("project snapshot build deferred: provider at capacity",
				"project_id", request.ProjectID, "harness", request.Harness)
			return
		}
		if err != nil {
			b.config.Logger.Warn("project snapshot build failed",
				"project_id", request.ProjectID, "harness", request.Harness, "error", err)
			return
		}
		b.config.Logger.Info("project snapshot ready",
			"project_id", request.ProjectID, "harness", request.Harness,
			"snapshot_id", snapshotID, "duration_ms", b.now().Sub(started).Milliseconds())
		// Warmed apart from the build, so its slot is free for the next one.
		if b.config.WarmSnapshots {
			go b.warm(context.Background(), snapshotID, "project_id", request.ProjectID, "harness", request.Harness)
		}
	}()
	return true
}

func (b *Builder) needsBuild(ctx context.Context, request Request) bool {
	snapshot, ok, err := b.config.Store.RepositorySandboxSnapshot(
		ctx, request.OrgID, request.RepositoryKey, b.config.Provider, request.Harness)
	if err != nil {
		b.config.Logger.Warn("look up project snapshot", "project_id", request.ProjectID, "error", err)
		return false
	}
	return !ok || !usable(snapshot, request) ||
		b.now().Sub(snapshot.CreatedAt) > b.config.MaxAge
}

func (b *Builder) build(ctx context.Context, request Request) (string, error) {
	cloneURL, token, err := b.cloneSource(ctx, request)
	if err != nil {
		return "", err
	}
	environment, err := b.config.VMs.Create(ctx, sandbox.Spec{
		RootFS: request.BaseSnapshotID,
		Labels: map[string]string{
			"ao.org_id":     request.OrgID,
			"ao.project_id": request.ProjectID,
			"ao.purpose":    "project-snapshot",
		},
	})
	if err != nil {
		return "", fmt.Errorf("create builder VM: %w", err)
	}
	defer func() {
		// The builder VM is disposable whatever happened; never leak it. No
		// session's worker ran in it, so there is no project Cleanup to run.
		deleteCtx, cancel := context.WithTimeout(sandbox.WithoutCleanup(context.Background()), time.Minute)
		defer cancel()
		if err := b.config.VMs.Delete(deleteCtx, environment.ID); err != nil {
			b.config.Logger.Warn("delete project snapshot builder VM", "provider_id", environment.ID, "error", err)
		}
	}()
	env := map[string]string{"AO_CLONE_URL": cloneURL}
	if token != "" {
		env["AO_GIT_TOKEN"] = token
	}
	if _, err := b.config.VMs.ExecRoot(ctx, environment.ID, cloneScript(), env, execTimeout); err != nil {
		return "", fmt.Errorf("clone repository in builder VM: %w", err)
	}
	snapshotID, err := b.config.VMs.Snapshot(ctx, environment.ID, snapshotSlug(request, b.now()))
	if err != nil {
		return "", fmt.Errorf("snapshot builder VM: %w", err)
	}
	previous, err := b.config.Store.ReplaceRepositorySandboxSnapshot(ctx, domain.RepositorySandboxSnapshot{
		OrgID:              request.OrgID,
		RepositoryKey:      request.RepositoryKey,
		RepositoryIdentity: request.RepositoryIdentity,
		Provider:           b.config.Provider,
		Harness:            request.Harness,
		SnapshotID:         snapshotID,
		BaseSnapshotID:     request.BaseSnapshotID,
	})
	if err != nil {
		_ = b.config.VMs.DeleteSnapshot(ctx, snapshotID)
		return "", fmt.Errorf("record project snapshot: %w", err)
	}
	if previous != "" {
		// The store retired the replaced snapshot rather than dropping it: a
		// session still running from it may need it for a recreate. Collecting
		// now deletes it at the provider at once when nothing references it,
		// and otherwise leaves it for a later run.
		b.Collect(ctx)
	}
	return snapshotID, nil
}

// Warm boots a throwaway VM from snapshotID, reads what a session's first
// seconds read (the worker, the agent CLIs, the repository), and deletes it.
// The first boots from a freshly made snapshot are several seconds slower than
// later ones (on staging: VM 2-3 s and worker start-to-connect 1.3-2.6 s, then
// 0.5 s and 0.7 s), so a real session should not be the first.
func (b *Builder) Warm(ctx context.Context, snapshotID string) error {
	ctx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	environment, err := b.config.VMs.Create(ctx, sandbox.Spec{
		RootFS: snapshotID,
		Labels: map[string]string{"ao.purpose": "snapshot-warm"},
	})
	if err != nil {
		return fmt.Errorf("create warming VM: %w", err)
	}
	defer func() {
		deleteCtx, cancel := context.WithTimeout(sandbox.WithoutCleanup(context.Background()), time.Minute)
		defer cancel()
		if err := b.config.VMs.Delete(deleteCtx, environment.ID); err != nil {
			b.config.Logger.Warn("delete snapshot warming VM", "provider_id", environment.ID, "error", err)
		}
	}()
	if _, err := b.config.VMs.ExecRoot(ctx, environment.ID, warmScript, nil, warmTimeout); err != nil {
		return fmt.Errorf("read the snapshot in the warming VM: %w", err)
	}
	return nil
}

// warm runs Warm and logs the outcome; a failed warm only costs the next
// session the slower first boot.
func (b *Builder) warm(ctx context.Context, snapshotID string, attrs ...any) {
	started := b.now()
	attrs = append(attrs, "snapshot_id", snapshotID)
	if err := b.Warm(ctx, snapshotID); err != nil {
		if ctx.Err() == nil {
			b.config.Logger.Warn("warm snapshot", append(attrs, "error", err)...)
		}
		return
	}
	b.config.Logger.Info("snapshot warmed", append(attrs, "duration_ms", b.now().Sub(started).Milliseconds())...)
}

// warmBases warms the harness snapshots sessions now boot from, once each per
// control-plane process: a deploy bakes new ones, and starts the process.
func (b *Builder) warmBases(ctx context.Context) {
	if !b.config.WarmSnapshots || b.config.BaseSnapshot == nil {
		return
	}
	for _, harness := range b.config.BaseHarnesses {
		base := b.config.BaseSnapshot(harness)
		b.mu.Lock()
		seen := base == "" || b.warmed[base]
		if !seen {
			b.warmed[base] = true
		}
		b.mu.Unlock()
		if !seen {
			b.warm(ctx, base, "harness", harness)
		}
	}
}

// warmScript reads, as root and without failing, the files a session's worker
// start, agent start and checkout read first, so the provider has them paged
// in. tar writes to a pipe: GNU tar skips reading files when its archive is
// /dev/null.
const warmScript = `set +e
cat /usr/local/bin/ao-worker /usr/local/bin/ao >/dev/null 2>&1
for tool in node git gh claude claude-agent-acp codex cursor-agent; do
  path="$(command -v "$tool" 2>/dev/null)" && cat "$(readlink -f "$path")" >/dev/null 2>&1
done
root="$(npm root -g 2>/dev/null)"
[ -n "$root" ] && tar -cf - "$root" 2>/dev/null | cat >/dev/null
[ -d ` + repositoryDir + ` ] && tar -cf - ` + repositoryDir + ` 2>/dev/null | cat >/dev/null
true`

// RunRefresher rebuilds snapshots left on an older base: once at start, which
// follows a deploy that rebaked the harness snapshots, and every
// collectInterval after that until ctx ends.
func (b *Builder) RunRefresher(ctx context.Context) {
	ticker := time.NewTicker(collectInterval)
	defer ticker.Stop()
	b.warmBases(ctx)
	for {
		b.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Refresh rebuilds the snapshots a session booted from within IdleRetention
// that were built on an older harness snapshot than the one sessions now boot
// from: a deploy rebakes the harness snapshots, which makes every repository
// snapshot unusable until its next session rebuilds it, so that session clones.
// It cannot see a deploy directly; a snapshot on a different base is the same
// fact. Each rebuild clones with its project's authority and waits for a free
// build slot, most recently used first.
func (b *Builder) Refresh(ctx context.Context) {
	store, ok := b.config.Store.(RefreshStore)
	if !ok || b.config.BaseSnapshot == nil {
		return
	}
	refreshes, err := store.RecentRepositorySandboxSnapshots(ctx, b.config.Provider, b.now().Add(-IdleRetention))
	if err != nil {
		if ctx.Err() == nil {
			b.config.Logger.Warn("list project snapshots to refresh", "error", err)
		}
		return
	}
	for _, refresh := range refreshes {
		base := b.config.BaseSnapshot(refresh.Snapshot.Harness)
		if base == "" || base == refresh.Snapshot.BaseSnapshotID {
			continue
		}
		request, ok := NewRequest(refresh.Snapshot.OrgID, refresh.Project, refresh.Snapshot.Harness, base)
		if !ok || request.RepositoryKey != refresh.Snapshot.RepositoryKey {
			continue
		}
		for !b.start(request) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(refreshRetryInterval):
			}
		}
		b.config.Logger.Info("refreshing project snapshot onto the current harness snapshot",
			"project_id", request.ProjectID, "harness", request.Harness)
	}
}

// RunCollector deletes idle snapshots every collectInterval until ctx ends.
func (b *Builder) RunCollector(ctx context.Context) {
	ticker := time.NewTicker(collectInterval)
	defer ticker.Stop()
	for {
		b.Collect(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Collect deletes snapshots no session has booted from within IdleRetention
// and no live session's plan still references (a long-running session may be
// recreated from its snapshot at any time). The provider copy is deleted
// first and the record only once that succeeds, so a failed provider delete
// is retried on the next run instead of leaking the copy.
func (b *Builder) Collect(ctx context.Context) {
	snapshotIDs, err := b.config.Store.CollectIdleRepositorySandboxSnapshots(
		ctx, b.config.Provider, b.now().Add(-IdleRetention),
		func(ctx context.Context, snapshotID string) error {
			deleteCtx, cancel := context.WithTimeout(ctx, collectDeleteTimeout)
			defer cancel()
			return b.config.VMs.DeleteSnapshot(deleteCtx, snapshotID)
		})
	for _, snapshotID := range snapshotIDs {
		b.config.Logger.Info("deleted idle project snapshot", "snapshot_id", snapshotID)
	}
	if err != nil && ctx.Err() == nil {
		b.config.Logger.Warn("collect idle project snapshots", "error", err)
	}
}

// cloneSource returns the URL and token to clone with: the triggering
// session's checkout grant, the project's when no session triggered the build,
// or an anonymous public clone where allowed.
func (b *Builder) cloneSource(ctx context.Context, request Request) (string, string, error) {
	if b.config.Grants != nil {
		var grant githubapp.CheckoutGrant
		var err error
		if request.SessionID != "" {
			grant, err = b.config.Grants.IssueCheckoutGrant(ctx, request.OrgID, request.SessionID)
		} else if projectGrants, ok := b.config.Grants.(ProjectGrants); ok {
			grant, err = projectGrants.IssueProjectCheckoutGrant(ctx, request.OrgID, request.ProjectID)
		} else {
			err = errors.New("no session to issue a checkout grant for")
		}
		if err == nil && grant.CloneURL != "" && grant.Token != "" {
			return grant.CloneURL, grant.Token, nil
		}
		if !b.config.AllowAnonymous {
			return "", "", fmt.Errorf("issue checkout grant: %w", err)
		}
	}
	if !b.config.AllowAnonymous || strings.TrimSpace(request.RepositoryURL) == "" {
		return "", "", fmt.Errorf("no checkout grant for project %s", request.ProjectID)
	}
	return strings.TrimSpace(request.RepositoryURL), "", nil
}

// cloneScript clones as the worker user with the token supplied only through
// the environment of this one command (read by a throwaway askpass helper that
// never contains it), marks the checkout for the worker, and fails if the
// token was written anywhere a snapshot would keep it.
func cloneScript() string {
	return `set -eu
askdir="$(mktemp -d)"
trap 'rm -rf "$askdir"' EXIT
cat > "$askdir/askpass" <<'ASKPASS'
#!/bin/sh
case "$1" in
*Username*) printf '%s\n' x-access-token;;
*Password*) printf '%s\n' "${AO_GIT_TOKEN:-}";;
*) exit 1;;
esac
ASKPASS
chmod 0755 "$askdir" "$askdir/askpass"
rm -rf ` + repositoryDir + `
mkdir -p /workspace
chown ` + workerUser + `:` + workerUser + ` /workspace
runuser --user ` + workerUser + ` -- env \
  GIT_ASKPASS="$askdir/askpass" GIT_ASKPASS_REQUIRE=force GIT_TERMINAL_PROMPT=0 \
  AO_GIT_TOKEN="${AO_GIT_TOKEN:-}" \
  git clone --origin origin --no-tags --filter=blob:none -- "$AO_CLONE_URL" ` + repositoryDir + `
runuser --user ` + workerUser + ` -- touch ` + repositoryDir + `/.git/` + MarkerFile + `
rm -rf "$askdir"
if [ -n "${AO_GIT_TOKEN:-}" ] && grep -rqsF -- "$AO_GIT_TOKEN" /workspace /tmp /root /home 2>/dev/null; then
  echo "clone token found on disk" >&2
  exit 1
fi
sync`
}

func snapshotSlug(request Request, now time.Time) string {
	project := strings.ToLower(request.ProjectID)
	if len(project) > 8 {
		project = project[:8]
	}
	harness := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(request.Harness))
	return fmt.Sprintf("ao-project-%s-%s-%d", project, harness, now.Unix())
}
