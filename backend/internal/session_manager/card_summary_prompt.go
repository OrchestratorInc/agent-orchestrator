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
const cardSummaryPrompt = "## Card Summary\n" +
	"\n" +
	"Run `ao summary \"<text>\"` to set the one-line summary on your kanban card. The human watches the board to follow what you are doing right now, so keep it specific, under 200 characters, and in plain words.\n" +
	"\n" +
	"- Run it as its own command: no `&&`, pipes, or redirects. When you can, issue it in the same turn as your next tool call rather than as a step of its own.\n" +
	"- Set it as soon as you start, then again each time you move to a new step: reading a part of the code, changing a specific area, running tests or a build, fixing a failure, committing. Name the concrete thing, in present tense.\n" +
	"- Before you finish, set it once more with `--done`, in past tense, so the card shows what you accomplished.\n" +
	"- Updates are accepted at most once a minute; `--done` is always accepted. If one is refused, do not retry it: carry on and set the next one at your next step.\n" +
	"- It is best-effort. If the command fails, carry on; never let it hold up your work.\n" +
	"\n" +
	"Examples: `ao summary \"Reading the retry logic in the OpenAI provider\"`, `ao summary \"Adding backoff tests for failed provider calls\"`, `ao summary --done \"Added retries with backoff to every provider call, with tests\"`."
