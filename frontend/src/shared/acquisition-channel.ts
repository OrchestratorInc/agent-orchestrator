// Closed vocabulary for the first-run "How did you hear about Orchestrator?"
// question. Shared by the main process (which persists the answer in
// ~/.ao/app-state.json) and the renderer (which renders the options and sends
// ao.onboarding.attribution_answered). Never add a free-text value.
export const ACQUISITION_CHANNELS = [
	"x_twitter",
	"youtube",
	"friend_colleague",
	"github",
	"hacker_news",
	"reddit",
	"search",
	"ai_assistant",
	"newsletter_blog",
	"conference_event",
	"other",
] as const;
export type AcquisitionChannel = (typeof ACQUISITION_CHANNELS)[number];

/** What the user chose: a channel, or an explicit skip. */
export type AcquisitionAnswer = AcquisitionChannel | "skipped";

/**
 * The persisted state. "pending" is written only when the marker is first
 * created, so the question is asked only on installs that go through
 * first-run onboarding; existing installs carry no value (null) and never see it.
 */
export type AcquisitionChannelState = AcquisitionAnswer | "pending" | null;

export function isAcquisitionAnswer(value: unknown): value is AcquisitionAnswer {
	return value === "skipped" || ACQUISITION_CHANNELS.some((channel) => channel === value);
}
