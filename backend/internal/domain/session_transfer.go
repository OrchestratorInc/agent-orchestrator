package domain

import "strings"

// SessionTransfer is what one machine hands another to continue a session
// there: its branch head, a file with the work and conversation origin may not
// have, and the session's settings.
type SessionTransfer struct {
	TransferID string                `json:"transferId"`
	Source     SessionTransferSource `json:"source"`
	// RepoKey identifies the repository; the destination project must match it.
	RepoKey string `json:"repoKey"`
	Branch  string `json:"branch"`
	HeadSHA string `json:"headSha"`
	// PreservedRef names the uncommitted work in the bundle; empty when there was none.
	PreservedRef string `json:"preservedRef,omitempty"`
	// Archive is the one file the move carries; nil when origin has all the
	// work and no conversation moves.
	Archive *SessionTransferArchive `json:"archive,omitempty"`
	// Bundle is true when Archive holds a git bundle.
	Bundle bool `json:"bundle,omitempty"`
	// Native describes the agent conversation in Archive; nil when none moves.
	Native *SessionTransferNative `json:"native,omitempty"`
	// Mode is the session's mode on the source. A session that resumes its own
	// conversation runs in its CLI's terminal instead.
	Mode SessionMode `json:"mode,omitempty"`
	// HandoffNote starts the agent on a new conversation when the moved one
	// cannot be resumed.
	HandoffNote     string          `json:"handoffNote"`
	Harness         AgentHarness    `json:"harness"`
	Model           string          `json:"model,omitempty"`
	Effort          string          `json:"effort,omitempty"`
	Permissions     PermissionMode  `json:"permissions,omitempty"`
	DisplayName     string          `json:"displayName,omitempty"`
	IssueID         IssueID         `json:"issueId,omitempty"`
	RequestedOutput RequestedOutput `json:"requestedOutput,omitempty"`
	PullRequests    []string        `json:"pullRequests,omitempty"`
}

// SessionTransferSource names the session a transfer came from.
type SessionTransferSource struct {
	Label     string    `json:"label"`
	SessionID SessionID `json:"sessionId"`
}

// SessionTransferArchive describes the one file a move carries.
type SessionTransferArchive struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SessionTransferNative describes the agent's own conversation in a move.
type SessionTransferNative struct {
	NativeID   string `json:"nativeId"`
	SourceCwd  string `json:"sourceCwd"`
	CLIVersion string `json:"cliVersion,omitempty"`
}

// SessionTransferDestination records where a moved session continues.
type SessionTransferDestination struct {
	HostID    string    `json:"hostId"`
	Label     string    `json:"label"`
	ProjectID ProjectID `json:"projectId"`
	SessionID SessionID `json:"sessionId"`
}

// SessionMovedTo marks a session that continues on another machine.
type SessionMovedTo struct {
	TransferID string    `json:"transferId"`
	HostID     string    `json:"hostId"`
	Label      string    `json:"label"`
	ProjectID  ProjectID `json:"projectId"`
	SessionID  SessionID `json:"sessionId"`
}

// Pending reports a move that was prepared but not yet finished.
func (m SessionMovedTo) Pending() bool { return m.SessionID == "" }

// TransferRepoKey names the repository a move must find on the other
// computer: RepositoryKey, or the remote without credentials when it names no
// provider repository, such as a local path or a git:// path on the network.
func TransferRepoKey(origin string) string {
	clone := CloneURL(origin)
	if key := RepositoryKey(clone); key != "" {
		return key
	}
	return strings.TrimSuffix(clone, "/")
}

// CloneURL is a Git remote without the credentials, query or fragment its URL
// may carry. An SSH user name stays: it selects the server's Git account.
func CloneURL(origin string) string {
	origin = strings.TrimSpace(origin)
	scheme, rest, ok := strings.Cut(origin, "://")
	if !ok {
		return origin
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	authority, path, hasPath := strings.Cut(rest, "/")
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		user, _, _ := strings.Cut(authority[:i], ":")
		authority = authority[i+1:]
		if strings.Contains(scheme, "ssh") {
			authority = user + "@" + authority
		}
	}
	if hasPath {
		authority += "/" + path
	}
	return scheme + "://" + authority
}
