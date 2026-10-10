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
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
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
	// collectInterval is how often idle snapshots are looked for.
	collectInterval = time.Hour
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
	ReplaceRepositorySandboxSnapshot(ctx context.Context, snapshot domain.RepositorySandboxSnapshot) (string, error)
	DeleteIdleRepositorySandboxSnapshots(ctx context.Context, provider string, cutoff time.Time) ([]string, error)
}

// Grants issues a short-lived clone token for a session's repository.
type Grants interface {
	IssueCheckoutGrant(ctx context.Context, orgID, sessionID string) (githubapp.CheckoutGrant, error)
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
	Logger         *slog.Logger
}

// Builder looks up and (re)builds project snapshots.
type Builder struct {
	config   Config
	mu       sync.Mutex
	inflight map[string]bool
	now      func() time.Time
}

// New creates a Builder.
func New(config Config) *Builder {
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultMaxAge
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Builder{config: config, inflight: map[string]bool{}, now: time.Now}
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
	key := request.key()
	b.mu.Lock()
	if b.inflight[key] {
		b.mu.Unlock()
		return
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
		if !b.needsBuild(ctx, request) {
			return
		}
		started := b.now()
		snapshotID, err := b.build(ctx, request)
		if err != nil {
			b.config.Logger.Warn("project snapshot build failed",
				"project_id", request.ProjectID, "harness", request.Harness, "error", err)
			return
		}
		b.config.Logger.Info("project snapshot ready",
			"project_id", request.ProjectID, "harness", request.Harness,
			"snapshot_id", snapshotID, "duration_ms", b.now().Sub(started).Milliseconds())
	}()
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
		// The builder VM is disposable whatever happened; never leak it.
		deleteCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
		if err := b.config.VMs.DeleteSnapshot(ctx, previous); err != nil {
			b.config.Logger.Warn("delete replaced project snapshot", "snapshot_id", previous, "error", err)
		}
	}
	return snapshotID, nil
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

// Collect deletes snapshots no session has booted from within IdleRetention:
// first the record, so no new session can pick one up, then the provider copy.
// A provider delete that fails leaves an unreferenced snapshot behind, which
// the provider's own retention reclaims.
func (b *Builder) Collect(ctx context.Context) {
	snapshotIDs, err := b.config.Store.DeleteIdleRepositorySandboxSnapshots(
		ctx, b.config.Provider, b.now().Add(-IdleRetention))
	if err != nil {
		if ctx.Err() == nil {
			b.config.Logger.Warn("collect idle project snapshots", "error", err)
		}
		return
	}
	for _, snapshotID := range snapshotIDs {
		if err := b.config.VMs.DeleteSnapshot(ctx, snapshotID); err != nil {
			b.config.Logger.Warn("delete idle project snapshot", "snapshot_id", snapshotID, "error", err)
			continue
		}
		b.config.Logger.Info("deleted idle project snapshot", "snapshot_id", snapshotID)
	}
}

// cloneSource returns the URL and token to clone with: the triggering
// session's checkout grant, or an anonymous public clone where allowed.
func (b *Builder) cloneSource(ctx context.Context, request Request) (string, string, error) {
	if b.config.Grants != nil {
		grant, err := b.config.Grants.IssueCheckoutGrant(ctx, request.OrgID, request.SessionID)
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
