package sessionmanager

import "context"

// SetCardSummariesEnabled supplies the card summaries setting. While it
// reports true, new workers are asked to keep a one-line summary on their
// kanban card with `ao summary`; workers already running keep the prompt they
// started with.
func (m *Manager) SetCardSummariesEnabled(enabled func(context.Context) bool) {
	m.cardSummariesMu.Lock()
	defer m.cardSummariesMu.Unlock()
	m.cardSummaries = enabled
}

func (m *Manager) cardSummariesEnabled(ctx context.Context) bool {
	m.cardSummariesMu.Lock()
	enabled := m.cardSummaries
	m.cardSummariesMu.Unlock()
	return enabled != nil && enabled(ctx)
}

// cardSummaryPrompt asks a worker to keep its kanban card summary current.
// Kept short: it rides in every worker prompt while card summaries are on.
const cardSummaryPrompt = "## Card Summary\n" +
	"\n" +
	"Keep the one-line summary on your kanban card current with `ao summary \"<text>\"`: when you start and at each new step, naming the concrete thing in present tense, under 200 characters. Before you finish, set it once more with `ao summary --done \"<what you accomplished>\"`, in past tense. " +
	"Run it as its own command (no `&&`, pipes or redirects), in the same turn as your next tool call when you can. Updates are accepted at most once a minute; `--done` always is. If one is refused or fails, do not retry: carry on."
