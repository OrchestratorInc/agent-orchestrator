package sessionimport

// Mirrors Grok's UserRunTurnTracker: after the first promptIndex, unmarked
// phantom runs no longer open rewind boundaries. Non-user updates end a run.
type grokPromptTracker struct {
	seenMarker, inUser bool
	current            *int
}

func (t *grokPromptTracker) user(index *int) bool {
	if index != nil {
		t.seenMarker = true
	}
	changed := (index == nil) != (t.current == nil) || index != nil && t.current != nil && *index != *t.current
	newRun := !t.inUser || t.seenMarker && changed
	t.inUser, t.current = true, index
	return newRun && (!t.seenMarker || index != nil)
}

func (t *grokPromptTracker) other() { t.inUser, t.current = false, nil }
