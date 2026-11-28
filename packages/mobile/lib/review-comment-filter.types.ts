export type ReviewCommentView = "open" | "resolved";

export const reviewCommentViews: readonly ReviewCommentView[] = ["open", "resolved"];

export type ReviewCommentFilterProps = {
	view: ReviewCommentView;
	/** Each tab's count, shown in its segment. */
	counts: Record<ReviewCommentView, number>;
	onChange: (view: ReviewCommentView) => void;
};

/** "Open 3", "Resolved 12", as the desktop's tabs read. */
export function reviewCommentViewLabel(view: ReviewCommentView, count: number): string {
	return `${view === "open" ? "Open" : "Resolved"} ${count}`;
}
