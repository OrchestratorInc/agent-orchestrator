package domain

// ChatProviderHandoff reserves an independent provider-history boundary for a
// verified Terminal -> Chat handoff. It does not assert native ancestry. Nil
// means ordinary resume, which must retain exact provider identity.
//
// Session Manager proves the handoff; Chat prepares the native replay; storage
// rechecks this snapshot when publishing history and controller ownership in one
// transaction. This is an internal reservation, never a client-supplied override.
type ChatProviderHandoff struct {
	BoundaryID              string
	ConversationID          string
	PreviousSessionID       SessionID
	PreviousBranchID        string
	PreviousSequence        int64
	ExpectedControllerOwner SessionControllerOwner
	// RecoverySource, when set, names the retired owner whose stale identity the
	// previous branch still carries. Session Manager proves it before any
	// provider I/O; the publication transaction re-proves it and fails closed on
	// any change. It carries evidence only: nothing here authorizes rewriting an
	// existing branch row, and the recovery always appends a new boundary.
	RecoverySource *ChatProviderHandoffRecoverySource
}

// ChatProviderHandoffRecoverySource is the internal recovery identity of the
// terminated predecessor that still owns the previous branch.
type ChatProviderHandoffRecoverySource struct {
	SessionID              SessionID
	ProviderConversationID string
}
