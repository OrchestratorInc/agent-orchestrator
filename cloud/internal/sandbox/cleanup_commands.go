package sandbox

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A Cleanup run's budget: the sum of its steps' time limits
// (domain.ScriptStepTimeout: domain.DefaultCleanupStepTimeoutMinutes unless a
// step sets its own) and CleanupStepGrace for each, never below
// MinCleanupBudget (what every run had before per-step limits) nor above
// MaxCleanupBudget, so a deletion is delayed by a bounded time at most.
const (
	MinCleanupBudget = 10 * time.Minute
	MaxCleanupBudget = 2 * time.Hour
	// CleanupStepGrace covers timeout(1)'s kill grace (-k 5) and a step's
	// own start and end around its time limit.
	CleanupStepGrace = 10 * time.Second
	// MinCleanupQueueWait is how long a Cleanup run with no more than
	// MinCleanupBudget waits for a run slot before it is reported as not run
	// (CleanupQueueWait).
	MinCleanupQueueWait = 2 * time.Minute
)

// CleanupQueueWait is how long a Cleanup run whose budget is budget waits for
// a run slot: MinCleanupQueueWait, plus how far budget exceeds
// MinCleanupBudget (at most MaxCleanupBudget). A run whose steps set long
// time limits waits as long, because the runs ahead of it may take as long;
// the reconciler's deletion deadline grows by both (CleanupDeletionExtra), so
// a run that waited its whole queue wait still has its whole budget.
func CleanupQueueWait(budget time.Duration) time.Duration {
	return MinCleanupQueueWait + cleanupBudgetExtra(budget)
}

// CleanupDeletionExtra is how much longer than its default deadline the
// reconciler keeps re-requesting a deletion whose Cleanup budget is budget:
// the extra budget and the extra queue wait (CleanupQueueWait).
func CleanupDeletionExtra(budget time.Duration) time.Duration {
	return 2 * cleanupBudgetExtra(budget)
}

func cleanupBudgetExtra(budget time.Duration) time.Duration {
	return max(0, min(budget, MaxCleanupBudget)-MinCleanupBudget)
}

// CleanupBudget is the budget of a Cleanup run that may run any of the given
// lists of commands (each command led by its time limit's marker when it sets
// one): the largest list's, bounded as above. With no commands it is
// MinCleanupBudget.
func CleanupBudget(lists ...[]string) time.Duration {
	budget := MinCleanupBudget
	for _, commands := range lists {
		var total time.Duration
		for _, command := range commands {
			plain, timeout := domain.SplitScriptStepTimeout(command, domain.DefaultCleanupStepTimeoutMinutes)
			if strings.TrimSpace(plain) != "" {
				total += timeout + CleanupStepGrace
			}
		}
		budget = max(budget, total)
	}
	return min(budget, MaxCleanupBudget)
}

type cleanupCommandsKey struct{}

// WithCleanupCommands carries a session's current project Cleanup commands
// (Project settings > Scripts) to Provider.Delete. The reconciler reads them
// when it deletes the sandbox, so Cleanup uses the project's config at removal
// time, as a local project does, rather than the commands the worker was
// launched with. An empty list means the project has no Cleanup now.
func WithCleanupCommands(ctx context.Context, commands []string) context.Context {
	return context.WithValue(ctx, cleanupCommandsKey{}, append([]string{}, commands...))
}

// CleanupCommands returns the commands WithCleanupCommands carried, and false
// when the caller could not look them up (the provider then falls back to the
// commands the worker installed).
func CleanupCommands(ctx context.Context) ([]string, bool) {
	commands, ok := ctx.Value(cleanupCommandsKey{}).([]string)
	return commands, ok
}

type installedCleanupKey struct{}

// WithInstalledCleanupCommands carries the Cleanup commands the session's
// worker was last given (domain.InstalledCleanupCommands) to Provider.Delete:
// what the hook runs when the control plane cannot pass the project's current
// Cleanup in, so its output is read knowing the values they assign to
// secret-named variables.
func WithInstalledCleanupCommands(ctx context.Context, commands []string) context.Context {
	return context.WithValue(ctx, installedCleanupKey{}, append([]string{}, commands...))
}

// InstalledCleanupCommands returns the commands WithInstalledCleanupCommands
// carried, and false when none were stored.
func InstalledCleanupCommands(ctx context.Context) ([]string, bool) {
	commands, ok := ctx.Value(installedCleanupKey{}).([]string)
	return commands, ok
}

type skipCleanupKey struct{}

// WithoutCleanup tells Provider.Delete to delete at once without running or
// waiting for the project's Cleanup. The reconciler uses it for its last
// Delete before it gives up on a deletion, so a slow or restarted Cleanup can
// delay a deletion but never leave the workspace running.
func WithoutCleanup(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipCleanupKey{}, true)
}

// CleanupSkipped reports whether WithoutCleanup marked ctx.
func CleanupSkipped(ctx context.Context) bool {
	skip, _ := ctx.Value(skipCleanupKey{}).(bool)
	return skip
}

type cleanupOrgKey struct{}

// WithCleanupOrg names the organization whose sandbox Provider.Delete
// removes, so a provider can bound how many Cleanup runs one organization has
// at once.
func WithCleanupOrg(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, cleanupOrgKey{}, orgID)
}

// CleanupOrg returns the organization WithCleanupOrg named, or "".
func CleanupOrg(ctx context.Context) string {
	orgID, _ := ctx.Value(cleanupOrgKey{}).(string)
	return orgID
}

type cleanupNoResumeKey struct{}

// WithoutCleanupResume tells Provider.Delete not to resume a paused or
// stopped sandbox to run the project's Cleanup, with the reason, said to the
// user: the organization is over its plan, so its paused sandboxes stay
// paused. A running sandbox still runs its Cleanup.
func WithoutCleanupResume(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, cleanupNoResumeKey{}, reason)
}

// CleanupResumeRefused returns the reason WithoutCleanupResume gave, and
// whether it was given.
func CleanupResumeRefused(ctx context.Context) (string, bool) {
	reason, ok := ctx.Value(cleanupNoResumeKey{}).(string)
	return reason, ok
}

type cleanupQueueHoldKey struct{}

// WithCleanupQueueHold gives Provider.Delete a function to report how much
// longer than its own queue wait (CleanupQueueWait) the deletion's Cleanup
// run may wait for a slot, because the organization's runs ahead of it may
// take that long. The reconciler adds it to the deletion's deadline, so a run
// still waiting within its queue wait is not deleted without its Cleanup.
func WithCleanupQueueHold(ctx context.Context, hold func(time.Duration)) context.Context {
	return context.WithValue(ctx, cleanupQueueHoldKey{}, hold)
}

// ReportCleanupQueueHold reports hold to the function WithCleanupQueueHold
// gave, if any.
func ReportCleanupQueueHold(ctx context.Context, hold time.Duration) {
	if report, ok := ctx.Value(cleanupQueueHoldKey{}).(func(time.Duration)); ok && report != nil {
		report(hold)
	}
}
