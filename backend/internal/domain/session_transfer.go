package domain

import "strings"

// SessionTransfer is what one machine hands another to continue a session
// there: where the code is on origin, the agent's conversation, and the
// session's settings.
type SessionTransfer struct {
	TransferID string                `json:"transferId"`
	Source     SessionTransferSource `json:"source"`
	// RepoKey identifies the repository; the destination project must match it.
	RepoKey string `json:"repoKey"`
	Branch  string `json:"branch"`
	HeadSHA string `json:"headSha"`
	// PreservedRef is the origin ref holding uncommitted work; empty when there was none.
	PreservedRef string `json:"preservedRef,omitempty"`
	// Native describes the exported agent conversation; nil when there was none.
	Native *SessionTransferNative `json:"native,omitempty"`
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

// SessionTransferNative describes an exported native conversation archive.
type SessionTransferNative struct {
	NativeID   string `json:"nativeId"`
	SourceCwd  string `json:"sourceCwd"`
	CLIVersion string `json:"cliVersion,omitempty"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

// SessionTransferDestination records where a moved session continues.
type SessionTransferDestination struct {
	HostID    string    `json:"hostId"`
	Label     string    `json:"label"`
	SessionID SessionID `json:"sessionId"`
}

// SessionMovedTo marks a session that continues on another machine.
type SessionMovedTo struct {
	TransferID string    `json:"transferId"`
	HostID     string    `json:"hostId"`
	Label      string    `json:"label"`
	SessionID  SessionID `json:"sessionId"`
}

// RepositoryKey is host/namespace/name for an origin URL, lower-cased for
// GitHub, whose names are case-insensitive. Any other origin, such as a local
// path, keys as itself.
func RepositoryKey(origin string) string {
	origin = strings.TrimSpace(origin)
	id, err := ParseRepositoryIdentity(origin)
	if err != nil {
		return strings.TrimSuffix(origin, "/")
	}
	key := id.Host + "/" + id.Namespace + "/" + id.Name
	if id.Provider == "github" {
		key = strings.ToLower(key)
	}
	return key
}
