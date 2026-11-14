import type { ItemAction } from "./item-actions-menu.types";
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

/** A review summary or an inline comment, with its "⋯" actions. */
export type ReviewFeedbackItem = {
	key: string;
	author: string;
	/** The verdict, or where a comment came from and whether it is resolved. */
	meta: string;
	/** `file:line` for an inline comment. */
	location?: string;
	/** The text, or what to say when there is none. */
	body: string;
	bodyMissing: boolean;
	notInjected: boolean;
	sent: boolean;
	menuLabel: string;
	actions: ItemAction[];
	working: boolean;
};

export type ReviewFeedbackSection = {
	id: "reviews" | "unresolved" | "resolved";
	title: string;
	footer: string;
	items: ReviewFeedbackItem[];
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
	feedback: ReviewFeedbackSection[];
	/** Set when GitHub has nothing for this pull request yet. */
	emptyFeedback?: string;
};
