package telemetrymeta

// spawnFailureCauses is the closed vocabulary ao.sessions.spawn_failed reports
// as cause and Sentry accepts as the cause tag. Every value names a condition,
// never error text; docs/telemetry.md lists what each one means.
var spawnFailureCauses = map[string]struct{}{
	// Any step.
	"timeout": {}, "permission": {}, "disk_full": {}, "path_too_long": {}, "killed": {}, "other": {},
	// Worktree and branch steps (git and AO's own refusals).
	"branch_exists": {}, "path_exists": {}, "locked": {}, "not_a_repo": {},
	"git_missing": {}, "unsafe_repo": {}, "repo_missing": {}, "in_use": {},
	"repo_corrupt": {}, "ref_conflict": {}, "ref_missing": {}, "no_default_branch": {},
	"invalid_branch": {}, "git_error": {},
	// Agent and Chat controller starts.
	"not_logged_in": {}, "binary_missing": {}, "agent_error": {}, "host_failed": {},
	"exited": {}, "no_response": {}, "incompatible": {},
}

// IsSpawnFailureCause reports whether value is a spawn failure cause.
func IsSpawnFailureCause(value string) bool {
	_, ok := spawnFailureCauses[value]
	return ok
}
