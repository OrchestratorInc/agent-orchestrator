package domain

import "errors"

// ErrConversationActivityNotFound reports an activity outside the active conversation lineage.
var ErrConversationActivityNotFound = errors.New("conversation activity not found")
