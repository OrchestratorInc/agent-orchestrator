export type ReviewerPickerOption = {
	id: string;
	label: string;
};

/**
 * The reviewer and model rows of the review actions sheet. Both variants draw
 * native rows for a native list the caller hosts: SwiftUI `Picker`s for a
 * `Form` section on iOS, Material list items that open a dropdown menu inside a
 * Compose `Host` on Android.
 */
export type ReviewerPickerProps = {
	/** Only selectable reviewers; unavailable agents are never offered. */
	reviewers: readonly ReviewerPickerOption[];
	/** The session override, or "" for the project default. */
	selectedReviewer: string;
	/** The harness that actually runs reviews, shown next to "Project default". */
	effectiveReviewer: string;
	onSelectReviewer: (id: string) => void;
	/** Empty when the reviewer has no model/mode catalog. */
	models: readonly ReviewerPickerOption[];
	modelTitle: "Model" | "Mode";
	/** The configured model/mode id, or "" for the provider default. */
	selectedModel: string;
	onSelectModel: (id: string) => void;
	busy: boolean;
};

export function reviewerLabel(reviewers: readonly ReviewerPickerOption[], id: string): string {
	return reviewers.find((item) => item.id === id)?.label || id;
}

/** What the reviewer row reads when the session follows the project's choice. */
export function projectDefaultLabel(reviewers: readonly ReviewerPickerOption[], effectiveReviewer: string): string {
	return effectiveReviewer ? `Project default · ${reviewerLabel(reviewers, effectiveReviewer)}` : "Project default";
}

/**
 * The options to offer: the catalog, plus the current value when the catalog no
 * longer lists it, so the row never reads blank.
 */
export function withCurrentOption(options: readonly ReviewerPickerOption[], current: string): readonly ReviewerPickerOption[] {
	return !current || options.some((option) => option.id === current) ? options : [...options, { id: current, label: current }];
}
