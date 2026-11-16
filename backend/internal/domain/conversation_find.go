package domain

// ConversationFindWindow is a read-only page used to reveal an old find result.
type ConversationFindWindow struct {
	Conversation   ConversationRecord
	ActiveBranch   ConversationBranch
	Messages       []ConversationMessage
	Activities     []ConversationActivity
	Turns          []ConversationTurn
	BranchPoints   []ConversationBranchPoint
	OldestSequence int64
	HasMoreBefore  bool
}
