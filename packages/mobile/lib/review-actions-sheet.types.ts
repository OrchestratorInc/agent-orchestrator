import type { ReviewerPickerProps } from "./reviewer-picker.types";

export type ReviewPolicyKey = "autoReviewEnabled" | "autoInjectReview" | "autoInjectCI";

/** One automation switch: the desktop inspector's wording, and whether it is saving. */
export type ReviewPolicyRow = {
	key: ReviewPolicyKey;
	title: string;
	description: string;
	value: boolean;
	/** Loading the session, or saving this switch: a spinner stands in for it. */
	busy: boolean;
};

/** A GitHub reviewer who can be asked to look again. */
export type ExternalReviewerRow = {
	id: string;
	/** "3 comments" */
	detail: string;
	busy: boolean;
};

/**
 * Everything the review actions sheet shows, worked out by the route. Each
 * platform lays it out natively: a SwiftUI Form on iOS, Material list items in
 * the sheet's scroll view on Android.
 */
export type ReviewActionsSheetProps = {
	subtitle: string;
	/** Failures and warnings, shown above the sections and announced. */
	alerts: string[];
	policies: ReviewPolicyRow[];
	/** True while another action runs: every control waits for it. */
	locked: boolean;
	onPolicyChange: (key: ReviewPolicyKey, value: boolean) => void;
	/** Undefined when no reviewer agent can run. */
	reviewer?: ReviewerPickerProps;
	externalReviewers: ExternalReviewerRow[];
	onRerequest: (id: string) => void;
};
