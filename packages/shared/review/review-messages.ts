import type { ReviewComment, ReviewPRBlock } from "./review-comments";

/**
 * What the worker receives when a review comment is sent to it, by hand or
 * all of a PR's open ones at once. The desktop's Reviews tab and the phone's
 * PR page send these same words.
 */

/** Control characters would garble the agent's terminal; provider text is otherwise sent as written. */
export function sanitizeWorkerMessagePart(value: string): string {
	return value.replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, "");
}

/** One comment as the worker reads it: who, where, what, and a link back. */
export function reviewCommentWorkerMessage(comment: ReviewComment, block: Pick<ReviewPRBlock, "number" | "webUrl">): string {
	const reviewerId = comment.source === "orchestrator" ? comment.harness || "reviewer" : comment.author;
	if (comment.kind === "inline") {
		return formatInlineReviewCommentMessage({ body: comment.body, file: comment.file, line: comment.line, reviewerId, url: comment.url });
	}
	return formatReviewSummaryMessage({
		body: comment.body,
		pullRequestUrl: block.webUrl,
		reviewerId,
		source: comment.source === "orchestrator" ? "agent" : "external",
		url: comment.url,
	});
}

/** A PR's open comments in one message, so the worker takes them as one round. */
export function reviewCommentsWorkerMessage(block: Pick<ReviewPRBlock, "number" | "webUrl">, comments: readonly ReviewComment[]): string {
	const lines = [
		`Pull request #${block.number} has ${comments.length} open review ${comments.length === 1 ? "comment" : "comments"}. Address the actionable ones, run the relevant tests, commit the fixes, and push the branch.`,
	];
	comments.forEach((comment, index) => {
		const reviewer = sanitizeWorkerMessagePart(comment.source === "orchestrator" ? `Orchestrator review (${comment.harness || "reviewer"})` : `@${comment.author}`);
		const location = comment.file ? sanitizeWorkerMessagePart(`${comment.file}${comment.line ? `:${comment.line}` : ""}`) : comment.kind === "summary" ? "review summary" : "general PR comment";
		lines.push("", `${index + 1}. ${reviewer} · ${location}`, sanitizeWorkerMessagePart(comment.body.trim() || "No comment body provided."));
		if (comment.url) lines.push(`URL: ${sanitizeWorkerMessagePart(comment.url)}`);
	});
	return lines.join("\n");
}

export function formatInlineReviewCommentMessage(comment: { body?: string; file?: string; line?: number; reviewerId?: string; url?: string }): string {
	const reviewer = sanitizeWorkerMessagePart(comment.reviewerId?.trim() || "unknown reviewer");
	const file = sanitizeWorkerMessagePart(comment.file?.trim() || "");
	const location = file ? `${file}${comment.line ? `:${comment.line}` : ""}` : "general PR comment";
	const body = sanitizeWorkerMessagePart(comment.body?.trim() || "No comment body provided.");
	const url = sanitizeWorkerMessagePart(comment.url?.trim() || "");
	const lines = [
		`A reviewer left an unresolved inline comment on your PR. Address it, commit the fix, and push the branch to GitHub.`,
		"",
		`Reviewer: @${reviewer}`,
		`Location: ${location}`,
		"",
		"Comment:",
		body,
	];
	if (url) {
		lines.push("", `Comment URL: ${url}`);
	}
	lines.push("", "You should not need to re-fetch review data unless you need additional context beyond what Orchestrator has provided here.");
	return lines.join("\n");
}

export function formatReviewSummaryMessage(summary: { body: string; pullRequestUrl?: string; reviewerId: string; source: "agent" | "external"; url?: string | null }): string {
	const reviewer = sanitizeWorkerMessagePart(summary.reviewerId.trim() || "reviewer");
	const body = sanitizeWorkerMessagePart(summary.body.trim());
	const url = sanitizeWorkerMessagePart((summary.url || summary.pullRequestUrl || "").trim());
	const source = summary.source === "agent" ? "Orchestrator review" : "external PR review";
	const lines = [
		`An ${source} from ${reviewer} has feedback for your pull request. Address the actionable items, run relevant tests, commit the fixes, and push the branch.`,
		"",
		"Review summary:",
		body,
	];
	if (url) lines.push("", `Review URL: ${url}`);
	return lines.join("\n");
}

/**
 * What a reply to a review or top-level comment opens with: its first
 * paragraph quoted (cut at a word past 240 characters) and an @mention, as the
 * PR tab's Reply writes it, so the reply nests under it on the provider.
 */
export function replyQuote(author: string, body: string): string {
	let excerpt = body.trim().split(/\n\s*\n/)[0] ?? "";
	if (excerpt.length > 240) {
		const space = excerpt.lastIndexOf(" ", 240);
		excerpt = excerpt.slice(0, space > 0 ? space : 240);
	}
	const quoted = excerpt.split("\n").map((line) => `> ${line}`.trimEnd()).join("\n");
	return `${quoted}\n\n@${author} `;
}
