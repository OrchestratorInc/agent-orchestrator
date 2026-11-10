package domain

// SessionBranchPrefix is the first path segment of the branches Orchestrator
// generates for new sessions, for example "orchestrator/<session>/root".
const SessionBranchPrefix = "orchestrator"

// LegacySessionBranchPrefix is the prefix sessions created before the rename
// recorded. Their branches keep working: PR attribution matches both.
const LegacySessionBranchPrefix = "ao"

// SessionBranchPrefixes lists every generated-branch prefix, newest first.
var SessionBranchPrefixes = []string{SessionBranchPrefix, LegacySessionBranchPrefix}
