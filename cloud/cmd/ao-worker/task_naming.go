package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/tasknaming"
	"github.com/aoagents/agent-orchestrator/backend/pkg/taskworkspace"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

var taskNamingSlots = make(chan struct{}, tasknaming.MaxConcurrent)

type workspaceFailureCommandBuilder struct{ err error }

func (b workspaceFailureCommandBuilder) Build(context.Context, worker.Turn, worker.CredentialResponse, string) (workerexec.Command, error) {
	return workerexec.Command{}, b.err
}

// Keep Chat's durable turn handling and workspace transport alive while every
// provider launch remains blocked by the unresolved startup error.
func workspaceFailureChatRunner(c *client, workspace string, cause error, logger *slog.Logger) *workerexec.Supervisor {
	runner := &workerexec.Supervisor{Control: c, Builder: workspaceFailureCommandBuilder{cause}, Runner: workerexec.OSRunner{}, Workspace: workspace, Logger: logger, PollInterval: 100 * time.Millisecond}
	failure := checkoutStartupFailure(cause)
	runner.SetWorkspaceStartupError(failure.Message + ": " + failure.Detail)
	return runner
}

type taskNameJob struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan tasknaming.Name
	fallback tasknaming.Name
}

// The waiter owns the deadline. A provider's slow shutdown cannot extend the
// startup gate, and its late answer has no path back into a launched worker.
func startTaskNameCall(ctx context.Context, timeout time.Duration, fallback tasknaming.Name, call func(context.Context) (string, error)) *taskNameJob {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	job := &taskNameJob{ctx: ctx, cancel: cancel, done: make(chan tasknaming.Name, 1), fallback: fallback}
	select {
	case taskNamingSlots <- struct{}{}:
		go func() {
			defer func() { <-taskNamingSlots }()
			raw, err := call(ctx)
			name := fallback
			if err == nil && ctx.Err() == nil {
				name = tasknaming.Parse(raw, fallback)
			}
			job.done <- name
		}()
	default:
		job.done <- fallback
	}
	return job
}

func (j *taskNameJob) wait() tasknaming.Name {
	defer j.cancel()
	return tasknaming.Wait(j.ctx, j.done, j.fallback)
}

func startWorkerTaskName(ctx context.Context, c *client, launch worker.LaunchContext, dataDir string, logger *slog.Logger) *taskNameJob {
	fallback := tasknaming.Fallback(launch.Prompt, launch.SessionID)
	if logger == nil {
		logger = slog.Default()
	}
	return startTaskNameCall(ctx, tasknaming.Timeout, fallback, func(ctx context.Context) (raw string, err error) {
		started := time.Now()
		defer func() {
			name := fallback
			if err == nil && ctx.Err() == nil {
				name = tasknaming.Parse(raw, fallback)
			}
			generated := name.Title != "" || name.Slug != fallback.Slug
			logger.Info("task naming", "sessionID", launch.SessionID, "harness", launch.Harness, "durationMs", time.Since(started).Milliseconds(), "generated", generated, "fallback", !generated)
		}()
		var claim domain.TaskNameResult
		if err := c.do(ctx, "/worker/task-name", domain.TaskNameRequest{Claim: true}, &claim); err != nil {
			return "", err
		}
		if !claim.Generate {
			return "", errors.New("task naming capacity unavailable")
		}
		credential, err := c.agentCredential(ctx, "")
		if err != nil {
			return "", err
		}
		cfg, cleanup, err := (workerexec.HarnessBuilder{DataDir: dataDir}).BuildTaskNaming(ctx, launch, credential)
		credential.Secret = ""
		if err != nil {
			return "", err
		}
		defer cleanup()
		return tasknaming.RunBackground(ctx, cfg, tasknaming.SystemPrompt, launch.Prompt)
	})
}

func usesTaskWorkspace(launch worker.LaunchContext) bool {
	if launch.Kind != "worker" || launch.RepositoryURL == "" || worker.IsScratchRepositoryURL(launch.RepositoryURL) {
		return false
	}
	return launch.TaskNamingState == domain.TaskNamingPending || launch.TaskNamingState == domain.TaskNamingReserved ||
		launch.TaskNamingState == domain.TaskNamingDone && launch.TaskNameSlug != ""
}

func (c *client) nameTask(ctx context.Context, request domain.TaskNameRequest) (domain.TaskNameResult, error) {
	var result domain.TaskNameResult
	if err := c.do(ctx, "/worker/task-name", request, &result); err != nil {
		return result, err
	}
	if result.Branch == "" || result.Slug == "" || (result.State != domain.TaskNamingReserved && result.State != domain.TaskNamingDone) {
		return result, errors.New("control plane returned an incomplete task name")
	}
	return result, nil
}

type taskWorkspaceCheckpoint struct {
	Info  taskworkspace.Info `json:"info"`
	Fixed bool               `json:"fixed"`
	// A fixed checkout cannot use the shared worktree move journal. Record its
	// branch intent before git branch -m so either side of a crash is recoverable.
	TargetBranch string `json:"targetBranch,omitempty"`
}

func readTaskWorkspaceCheckpoint(ctx context.Context, dataDir string) (taskWorkspaceCheckpoint, error) {
	var record taskWorkspaceCheckpoint
	if err := ctx.Err(); err != nil {
		return record, err
	}
	body, err := os.ReadFile(filepath.Join(dataDir, "task-workspace.json"))
	if err == nil {
		err = json.Unmarshal(body, &record)
	}
	return record, err
}

func writeTaskWorkspaceCheckpoint(ctx context.Context, dataDir string, record taskWorkspaceCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir, ".task-workspace-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dataDir, "task-workspace.json")); err != nil {
		return err
	}
	dir, err := os.Open(dataDir)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func taskWorkspaceAdapter(ctx context.Context, canonical string, launch worker.LaunchContext, logger *slog.Logger) (*taskworkspace.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	managedRoot := filepath.Join(canonical, ".ao-task-worktrees")
	// Unlike local AO's data root, these components are repository-controlled.
	// Reject indirection before the shared adapter physically resolves its root.
	if launch.ProjectID == "" || filepath.Base(launch.ProjectID) != launch.ProjectID || launch.ProjectID == "." || launch.ProjectID == ".." {
		return nil, errors.New("invalid task workspace project directory")
	}
	for _, path := range []string{managedRoot, filepath.Join(managedRoot, launch.ProjectID)} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("managed task workspace parent must be a directory, not a symlink")
		}
		physical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		if physical != path {
			return nil, errors.New("managed task workspace parent must remain inside the canonical checkout")
		}
	}
	return taskworkspace.New(taskworkspace.Options{RepositoryPath: canonical, ManagedRoot: managedRoot, ProjectID: launch.ProjectID, SessionID: launch.SessionID, Logger: logger})
}

func taskWorkspaceOccupied(ctx context.Context, repository, managedParent string) ([]string, error) {
	out, err := (worker.ExecGitRunner{}).Run(ctx, repository, nil, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var occupied []string
	for _, ref := range strings.Fields(out) {
		if ref != "" {
			occupied = append(occupied, ref)
		}
	}
	if managedParent != "" {
		entries, err := os.ReadDir(managedParent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, entry := range entries {
			occupied = append(occupied, entry.Name())
		}
	}
	return occupied, nil
}

// taskNameOccupiedCandidates compresses arbitrarily large ref inventories to
// the bounded candidate family the control plane can select. The Git adapter
// still checks the complete local inventory when it performs the rename.
func taskNameOccupiedCandidates(branch, reservedSlug, slug string, occupied []string) []string {
	prefix := ""
	if reservedSlug != "" {
		prefix, _ = strings.CutSuffix(branch, "/"+reservedSlug+"/root")
	} else {
		branch = strings.TrimSuffix(branch, "/root")
		if i := strings.LastIndex(branch, "/"); i >= 0 {
			prefix = branch[:i]
		}
	}
	if reservedSlug != "" {
		slug = reservedSlug
	}
	var refs []string
	for _, value := range occupied {
		if strings.HasPrefix(value, "refs/") {
			refs = append(refs, value)
		}
	}
	var result []string
	for suffix := 1; suffix <= 100; suffix++ {
		candidate := slug
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", slug, suffix)
		}
		used := prefix != "" && taskBranchOccupied(prefix+"/"+candidate+"/root", refs, "")
		if !used {
			for _, value := range occupied {
				if !strings.HasPrefix(value, "refs/") && value == candidate {
					used = true
					break
				}
			}
		}
		if used {
			result = append(result, candidate)
		}
	}
	return result
}

func taskBranchMatches(branch string, reservation domain.TaskNameResult) bool {
	if branch == reservation.Branch {
		return true
	}
	namespace, ok := strings.CutSuffix(reservation.Branch, "/root")
	if !ok {
		return false
	}
	rest, ok := strings.CutPrefix(branch, namespace+"-")
	if !ok {
		return false
	}
	number, ok := strings.CutSuffix(rest, "/root")
	n, err := strconv.Atoi(number)
	return ok && err == nil && n >= 2 && strconv.Itoa(n) == number
}

// finalizeTaskWorkspace is the single pre-launch filesystem/control-plane gate.
// The canonical checkout has already been fetched, beside the naming call.
// Returning success means names are durable in both places; failures keep all
// recovery records for a fresh worker's retry.
func finalizeTaskWorkspace(ctx context.Context, logger *slog.Logger, c *client, bootstrap *worker.BootstrapResponse, canonical, dataDir string, fixed bool, name tasknaming.Name) (string, error) {
	if !fixed {
		physical, err := filepath.EvalSymlinks(canonical)
		if err != nil {
			return canonical, err
		}
		canonical = physical
	}
	launch := &bootstrap.Launch
	if launch.SessionID == "" {
		launch.SessionID = bootstrap.SessionID
	}
	managedParent := filepath.Join(canonical, ".ao-task-worktrees", launch.ProjectID)
	var adapter *taskworkspace.Workspace
	var err error
	if !fixed {
		adapter, err = taskWorkspaceAdapter(ctx, canonical, *launch, logger)
		if err != nil {
			return canonical, err
		}
		if err := excludeTaskWorktrees(ctx, canonical); err != nil {
			return canonical, err
		}
	}
	record, readErr := readTaskWorkspaceCheckpoint(ctx, dataDir)
	recoveredMove := false
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return canonical, readErr
	}
	if readErr == nil && (record.Fixed != fixed || (fixed && record.Info.Path != canonical) ||
		(!fixed && filepath.Dir(record.Info.Path) != managedParent)) {
		return canonical, errors.New("task workspace checkpoint does not match this worker's checkout")
	}
	if adapter != nil {
		// Recover by linked Git directory identity before trusting a stale path.
		info, found, err := adapter.Recover(ctx)
		if err != nil {
			return canonical, err
		}
		if found {
			record = taskWorkspaceCheckpoint{Info: info}
			readErr = nil
			recoveredMove = true
		}
	}
	if readErr != nil {
		if fixed {
			branch, err := currentTaskBranch(ctx, canonical)
			if err != nil {
				return canonical, err
			}
			if branch == "" {
				if launch.TaskNamingState != domain.TaskNamingDone {
					return canonical, errors.New("fixed task checkout is detached during naming")
				}
				branch = launch.Branch
			}
			record = taskWorkspaceCheckpoint{Info: taskworkspace.Info{Path: canonical, Branch: branch}, Fixed: true}
		} else if launch.TaskNamingState == domain.TaskNamingDone {
			record.Info = taskworkspace.Info{Path: filepath.Join(managedParent, launch.TaskNameSlug), Branch: launch.Branch}
		} else {
			initial := filepath.Join(managedParent, launch.SessionID)
			// A crash immediately after Create may leave its registered initial
			// worktree before the private checkpoint was written.
			if _, err := os.Lstat(initial); err == nil {
				branch, err := registeredTaskBranch(ctx, canonical, initial)
				if err != nil || !strings.HasPrefix(branch, "ao/task-naming-"+launch.SessionID+"/") {
					return canonical, errors.New("initial task workspace path is occupied")
				}
				record.Info = taskworkspace.Info{Path: initial, Branch: branch}
			} else if !errors.Is(err, os.ErrNotExist) {
				return canonical, err
			} else {
				record.Info, err = adapter.Create(ctx, "ao/task-naming-"+launch.SessionID+"/root", launch.DefaultBranch)
				if err != nil {
					return canonical, err
				}
			}
		}
	}
	if fixed {
		branch, err := currentTaskBranch(ctx, record.Info.Path)
		if err != nil {
			return canonical, err
		}
		if launch.TaskNamingState != domain.TaskNamingDone && (branch == "" || branch != record.Info.Branch && branch != record.TargetBranch) {
			return canonical, errors.New("fixed task checkout branch changed during naming")
		}
		if branch != "" {
			record.Info.Branch = branch
		}
		record.TargetBranch = ""
	} else {
		// Restore uses the local adapter; reject another checkout occupying its
		// target before Restore can consider repairing a missing registration.
		if _, err := os.Lstat(record.Info.Path); err == nil {
			branch, err := registeredTaskBranch(ctx, canonical, record.Info.Path)
			if err != nil {
				return canonical, err
			}
			if launch.TaskNamingState != domain.TaskNamingDone && (branch == "" || branch != record.Info.Branch) {
				return canonical, errors.New("managed task checkout branch changed during naming")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return canonical, err
		}
		record.Info, err = adapter.Restore(ctx, record.Info, launch.DefaultBranch)
		if err != nil {
			return canonical, err
		}
	}
	if err := writeTaskWorkspaceCheckpoint(ctx, dataDir, record); err != nil {
		return canonical, err
	}
	if launch.TaskNamingState == domain.TaskNamingDone {
		branch, err := currentTaskBranch(ctx, record.Info.Path)
		if err != nil {
			return canonical, err
		}
		if branch != launch.Branch {
			logger.Info("resuming task on changed HEAD", "sessionID", launch.SessionID, "detached", branch == "")
		}
		if adapter != nil {
			if err := adapter.Complete(ctx, record.Info); err != nil {
				return canonical, err
			}
		}
		return record.Info.Path, nil
	}
	if name.Slug == "" {
		name.Slug = tasknaming.Fallback(launch.Prompt, launch.SessionID).Slug
	}
	occupied, err := taskWorkspaceOccupied(ctx, canonical, managedParent)
	if err != nil {
		return canonical, err
	}
	reservation, err := c.nameTask(ctx, domain.TaskNameRequest{Title: name.Title, Slug: name.Slug, Occupied: taskNameOccupiedCandidates(launch.Branch, launch.TaskNameSlug, name.Slug, occupied)})
	if err != nil {
		return canonical, err
	}
	retryReservation := func() (domain.TaskNameResult, error) {
		occupied, err := taskWorkspaceOccupied(ctx, canonical, managedParent)
		if err != nil {
			return domain.TaskNameResult{}, err
		}
		reservation, err := c.nameTask(ctx, domain.TaskNameRequest{Retry: true, Occupied: taskNameOccupiedCandidates(reservation.Branch, reservation.Slug, reservation.Slug, occupied)})
		if err != nil {
			return reservation, err
		}
		// Persist the actual checkout before acknowledging an earlier move.
		if err := writeTaskWorkspaceCheckpoint(ctx, dataDir, record); err != nil {
			return reservation, err
		}
		if adapter != nil {
			err = adapter.Complete(ctx, record.Info)
		}
		return reservation, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		if !taskBranchMatches(record.Info.Branch, reservation) {
			if fixed {
				occupied, err = taskWorkspaceOccupied(ctx, canonical, "")
				if err != nil {
					return canonical, err
				}
				if taskBranchOccupied(reservation.Branch, occupied, record.Info.Branch) {
					reservation, err = retryReservation()
					if err != nil {
						return canonical, err
					}
					continue
				}
				record.TargetBranch = reservation.Branch
				if err := writeTaskWorkspaceCheckpoint(ctx, dataDir, record); err != nil {
					return canonical, err
				}
				_, err = (worker.ExecGitRunner{}).Run(ctx, record.Info.Path, nil, "branch", "-m", reservation.Branch)
				if err == nil {
					record.Info.Branch, record.TargetBranch = reservation.Branch, ""
				} else if _, probeErr := (worker.ExecGitRunner{}).Run(ctx, record.Info.Path, nil, "show-ref", "--verify", "refs/heads/"+reservation.Branch); probeErr == nil {
					record.TargetBranch = ""
					reservation, err = retryReservation()
					if err != nil {
						return canonical, err
					}
					continue
				}
			} else {
				// A prior worker may have obtained a new reservation immediately
				// before crashing. The saved actual path above must survive before
				// its old journal is acknowledged and a new move can be started.
				if recoveredMove {
					if err := adapter.Complete(ctx, record.Info); err != nil {
						return canonical, err
					}
					recoveredMove = false
				}
				record.Info, err = adapter.Name(ctx, record.Info, reservation.Branch, reservation.Slug)
			}
			if err != nil {
				return canonical, err
			}
		}
		if err := writeTaskWorkspaceCheckpoint(ctx, dataDir, record); err != nil {
			return canonical, err
		}
		result, completeErr := c.nameTask(ctx, domain.TaskNameRequest{Complete: true, Branch: record.Info.Branch})
		if completeErr == nil {
			if result.State != domain.TaskNamingDone || result.Branch != record.Info.Branch {
				return canonical, errors.New("control plane did not commit the actual task branch")
			}
			launch.TaskNamingState, launch.TaskNameSlug, launch.Branch, launch.DisplayName = result.State, result.Slug, result.Branch, result.Title
			if adapter != nil {
				if err := adapter.Complete(ctx, record.Info); err != nil {
					return canonical, err
				}
			}
			return record.Info.Path, nil
		}
		if !strings.Contains(completeErr.Error(), " returned 409:") {
			return canonical, completeErr
		}
		reservation, err = retryReservation()
		if err != nil {
			return canonical, err
		}
	}
	return canonical, errors.New("task naming could not reserve an unoccupied branch after five attempts")
}

func currentTaskBranch(ctx context.Context, workspace string) (string, error) {
	out, err := (worker.ExecGitRunner{}).Run(ctx, workspace, nil, "branch", "--show-current")
	return strings.TrimSpace(out), err
}

func registeredTaskBranch(ctx context.Context, canonical, path string) (string, error) {
	out, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, block := range strings.Split(out, "\n\n") {
		lines := strings.Split(block, "\n")
		if len(lines) == 0 || lines[0] != "worktree "+path {
			continue
		}
		for _, line := range lines {
			if line == "detached" {
				return "", nil
			}
			if branch, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				return branch, nil
			}
		}
	}
	return "", fmt.Errorf("task workspace %s is not its repository's registered worktree", path)
}

func excludeTaskWorktrees(ctx context.Context, canonical string) error {
	out, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(canonical, path)
	}
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const rule = "/.ao-task-worktrees/"
	for _, line := range strings.Split(string(body), "\n") {
		if line == rule {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\n" + rule + "\n")
	return errors.Join(err, f.Close())
}

// taskAdoptionLaunch keeps generated names away from the adoption helper until
// occupied refs have been collected. Its session-specific preparation branch
// is fresh even if the template already contains a branch with that name.
func taskAdoptionLaunch(ctx context.Context, launch worker.LaunchContext, workspace, dataDir, workingDirectory string) (worker.LaunchContext, error) {
	if launch.TaskNamingState == domain.TaskNamingDone {
		return launch, nil
	}
	if record, err := readTaskWorkspaceCheckpoint(ctx, dataDir); err == nil && record.Fixed {
		branch, err := currentTaskBranch(ctx, record.Info.Path)
		if err != nil {
			return launch, err
		}
		if branch == "" || branch != record.Info.Branch && branch != record.TargetBranch {
			return launch, errors.New("fixed task checkout branch changed during naming")
		}
		launch.Branch = branch
		return launch, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return launch, err
	}
	candidate := ""
	if workingDirectory != "" {
		path, err := worker.ExpandWorkingDirectory(workingDirectory, os.Getenv("HOME"), launch.RepositoryURL)
		if err != nil {
			return launch, err
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			candidate = path
		}
	} else if os.Getenv(worker.ReuseExistingCheckoutEnv) == "1" {
		candidate, _ = worker.FindExistingCheckout(ctx, worker.ExecGitRunner{}, os.Getenv("HOME"), launch.RepositoryURL, workspace, filepath.Dir(filepath.Clean(dataDir)))
	}
	branch := "ao/task-naming-" + launch.SessionID + "/root"
	if candidate != "" {
		var err error
		branch, err = freshTaskPreparationBranch(ctx, candidate, launch.SessionID)
		if err != nil {
			return launch, err
		}
	}
	launch.Branch = branch
	return launch, nil
}

func freshTaskPreparationBranch(ctx context.Context, repository, sessionID string) (string, error) {
	occupied, err := taskWorkspaceOccupied(ctx, repository, "")
	if err != nil {
		return "", err
	}
	base := "ao/task-naming-" + sessionID
	for n := 1; n <= 100; n++ {
		namespace := base
		if n > 1 {
			namespace += fmt.Sprintf("-%d", n)
		}
		if !taskBranchOccupied(namespace+"/root", occupied, "") {
			return namespace + "/root", nil
		}
	}
	return "", errors.New("no unused task preparation branch")
}

func taskRefBranch(ref string) string {
	if remote, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
		_, ref, _ = strings.Cut(remote, "/")
	} else {
		ref = strings.TrimPrefix(ref, "refs/heads/")
	}
	return ref
}

func taskBranchOccupied(branch string, occupied []string, current string) bool {
	namespace := strings.TrimSuffix(branch, "/root")
	for _, ref := range occupied {
		ref = taskRefBranch(ref)
		if ref == current {
			continue
		}
		if ref == namespace || strings.HasPrefix(ref, namespace+"/") || strings.HasPrefix(namespace, ref+"/") {
			return true
		}
	}
	return false
}

func prepareNamedWorkspace(ctx context.Context, logger *slog.Logger, c *client, bootstrap *worker.BootstrapResponse, canonical, dataDir, publicURL string, reuse worker.ReuseDecision, fixed bool, job *taskNameJob) (string, error) {
	_, checkoutErr := os.Stat(filepath.Join(canonical, ".git"))
	freshFixed := fixed && !reuse.Reused && errors.Is(checkoutErr, os.ErrNotExist)
	grant, err := c.checkoutGrant(ctx)
	if err != nil {
		if !anonymousCheckoutEnabled() {
			return canonical, fmt.Errorf("request checkout grant: %w", err)
		}
		grant = worker.CheckoutGrantResponse{CloneURL: bootstrap.Launch.RepositoryURL}
	}
	if reuse.Reused {
		if err := worker.ConfigureReusedCheckoutGit(dataDir, publicURL, bootstrap.SessionID); err != nil {
			return canonical, err
		}
		if err := worker.PrepareReusedCheckout(ctx, worker.ExecGitRunner{}, canonical, grant); err != nil {
			return canonical, err
		}
		if reuse.AdoptedNow {
			if err := worker.AdvanceAdoptedCheckout(ctx, worker.ExecGitRunner{}, canonical, bootstrap.Launch.DefaultBranch); err != nil {
				logger.Warn("could not advance adopted task checkout", "error", err)
			}
		}
	} else {
		if err := worker.PrepareCheckoutWithOptions(ctx, worker.ExecGitRunner{}, canonical, grant, worker.CheckoutOptions{PartialClone: os.Getenv(worker.PartialCloneEnv) == "1"}); err != nil {
			return canonical, err
		}
		// Selecting or restoring another branch can lazily fetch blobs from a
		// partial clone after the checkout grant's scoped environment is gone.
		// Install the shared credential helper before either operation, without
		// changing HEAD; linked worktrees inherit this repository configuration.
		if err := worker.ConfigureWorkerGitOnBranch(ctx, worker.ExecGitRunner{}, canonical, dataDir, publicURL, bootstrap.SessionID, bootstrap.Launch.Branch); err != nil {
			return canonical, err
		}
	}
	// A truly unborn new repository cannot host a linked worktree. Preserve
	// the legacy first-commit path, recording that choice durably before launch.
	legacyUnborn := false
	if !reuse.Reused && bootstrap.Launch.TaskNamingState == domain.TaskNamingPending {
		if _, err := readTaskWorkspaceCheckpoint(ctx, dataDir); errors.Is(err, os.ErrNotExist) {
			if _, headErr := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "rev-parse", "--verify", "HEAD^{commit}"); headErr != nil {
				commits, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "rev-list", "--all", "--max-count=1")
				if err != nil {
					return canonical, err
				}
				if strings.TrimSpace(commits) == "" {
					if job != nil {
						job.cancel()
					}
					var result domain.TaskNameResult
					if err := c.do(ctx, "/worker/task-name", domain.TaskNameRequest{Skip: true}, &result); err != nil {
						return canonical, err
					}
					if result.State != domain.TaskNamingDone || result.Slug != "" || result.Branch != bootstrap.Launch.Branch {
						return canonical, errors.New("control plane did not preserve the unborn task branch")
					}
					bootstrap.Launch.TaskNamingState, bootstrap.Launch.TaskNameSlug = result.State, result.Slug
					if err := worker.ConfigureWorkerGit(ctx, worker.ExecGitRunner{}, canonical, dataDir, publicURL, bootstrap.SessionID, result.Branch); err != nil {
						return canonical, err
					}
					legacyUnborn = true
				}
			}
		}
	}
	if freshFixed && bootstrap.Launch.TaskNamingState == domain.TaskNamingDone && !legacyUnborn {
		// A replacement sandbox just cloned the default branch. Select this
		// session's branch without resetting any pre-existing local ref.
		branch := bootstrap.Launch.Branch
		current, err := currentTaskBranch(ctx, canonical)
		if err != nil {
			return canonical, err
		}
		if current != branch {
			args := []string{"checkout", branch}
			if _, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
				base := "HEAD"
				if _, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "rev-parse", "--verify", "refs/remotes/origin/"+branch+"^{commit}"); err == nil {
					base = "refs/remotes/origin/" + branch
				}
				args = []string{"checkout", "--no-track", "-b", branch, base}
			}
			if _, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, args...); err != nil {
				return canonical, err
			}
		}
	}
	if fixed && bootstrap.Launch.TaskNamingState != domain.TaskNamingDone {
		if _, err := readTaskWorkspaceCheckpoint(ctx, dataDir); errors.Is(err, os.ErrNotExist) && !reuse.Reused {
			branch, err := freshTaskPreparationBranch(ctx, canonical, bootstrap.SessionID)
			if err != nil {
				return canonical, err
			}
			base := "HEAD"
			if defaultBranch := bootstrap.Launch.DefaultBranch; defaultBranch != "" {
				if _, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "rev-parse", "--verify", "refs/remotes/origin/"+defaultBranch+"^{commit}"); err == nil {
					base = "refs/remotes/origin/" + defaultBranch
				}
			}
			if _, err := (worker.ExecGitRunner{}).Run(ctx, canonical, nil, "checkout", "--no-track", "-b", branch, base); err != nil {
				return canonical, err
			}
		}
	}
	actual := canonical
	if !legacyUnborn {
		name := tasknaming.Name{}
		if job != nil {
			name = job.wait()
		}
		actual, err = finalizeTaskWorkspace(ctx, logger, c, bootstrap, canonical, dataDir, fixed, name)
		if err != nil {
			return canonical, fmt.Errorf("finalize task workspace: %w", err)
		}
	}
	cloneExtraRepos(ctx, logger, grant.Token, bootstrap.Launch.ExtraRepos, actual, dataDir)
	if err := worker.EnsureWorkspaceReviewBase(ctx, worker.ExecGitRunner{}, actual, bootstrap.Launch.DefaultBranch); err != nil {
		return canonical, err
	}
	return actual, nil
}
