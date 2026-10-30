export type ReviewButtonProps = {
	/** Queued comments. Nothing is drawn without any. */
	count: number;
	/** Opens the review sheet. */
	onPress: () => void;
	/** What the button has to stay above besides the safe area: a toolbar floating there. */
	bottomOffset?: number;
};

/** How much of a list's bottom the button covers, margin included, so the last row can scroll clear of it. */
export const REVIEW_BUTTON_CLEARANCE = 96;
/** How far the button reaches up from the bottom safe area, its margin included: where a toast has to start. */
export const REVIEW_BUTTON_RISE = 80;

export function reviewButtonLabel(count: number): { title: string; accessibilityLabel: string } {
	const noun = count === 1 ? "comment" : "comments";
	return { title: `Send ${count} ${noun}`, accessibilityLabel: `Review ${count} ${noun} before sending` };
}
