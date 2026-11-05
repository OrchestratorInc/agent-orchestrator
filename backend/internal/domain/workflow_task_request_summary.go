package domain

// WorkflowTaskRequestSummary is the redacted, typed projection of a stored task request's
// payload.
//
// It exists because the raw payload is arbitrary JSON: it can name repository scopes, absolute
// paths, prompts, credentials, or environment values, and only the decide path is meant to
// interpret it. Every value here is either a bounded integer, a truncated display string, or a
// closed-character-class identifier, so reading a request never leaks its body. The projection
// is built by collaboration.RedactTaskRequest, where it is tested.
type WorkflowTaskRequestSummary struct {
	// TaskCount is how many tasks the request proposes to add. Zero means the payload did not
	// declare one.
	TaskCount int `json:"taskCount"`
	// TitleHints are display-only candidate titles, already truncated. They are never used to
	// resolve anything.
	TitleHints []string `json:"titleHints"`
	// Repos are workflow-local repo KEYS only — never paths and never project ids.
	Repos []string `json:"repos"`
	// BudgetDelta is the requested change in budget, when the payload declared an integer.
	BudgetDelta *int64 `json:"budgetDelta,omitempty"`
	// ConcurrencyDelta is the requested change in concurrency, when declared.
	ConcurrencyDelta *int64 `json:"concurrencyDelta,omitempty"`
	// NotesRef is an opaque artifact reference, validated against the same closed character
	// class message payload refs use. It never names a path.
	NotesRef string `json:"notesRef,omitempty"`
}
