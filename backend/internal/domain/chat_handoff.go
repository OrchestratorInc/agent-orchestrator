package domain

type ChatProviderHandoff struct {
	BoundaryID              string
	ConversationID          string
	PreviousSessionID       SessionID
	PreviousBranchID        string
	PreviousSequence        int64
	ExpectedControllerOwner SessionControllerOwner
}
