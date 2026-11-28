import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { ReviewPRBlock as SharedReviewPRBlock } from "../../../../packages/shared/review/review-comments";
import type { PRReviewState } from "./session-reviews";

/**
 * The Reviews tab's comment list. The model lives in packages/shared so the
 * phone's PR page builds the same list; see buildReviewBlocks there.
 */
export {
	buildReviewBlocks,
	providerReviewId,
	reviewCommentIsOpen,
	reviewerRowKey,
	type ReviewComment,
	type ReviewOpenPR,
	type ReviewReply,
	type ReviewReviewer,
	type ReviewReviewerStatus,
	type ReviewVerdict,
} from "../../../../packages/shared/review/review-comments";

export type ReviewPRBlock = SharedReviewPRBlock<SessionPRSummary, PRReviewState>;
