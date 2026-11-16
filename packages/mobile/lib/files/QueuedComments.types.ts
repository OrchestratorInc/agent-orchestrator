import type { ReviewComment } from "./reviewStore";

export type QueuedCommentsProps = {
	comments: readonly ReviewComment[];
	onRemove: (id: string) => void;
	/** Holds the list still while the review is going out. */
	disabled?: boolean;
};

/** What a screen reader hears for one queued comment. */
export function queuedCommentLabel(comment: Pick<ReviewComment, "label" | "feedback">): string {
	return `Comment on ${comment.label}: ${comment.feedback}`;
}
