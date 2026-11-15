package domain

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
