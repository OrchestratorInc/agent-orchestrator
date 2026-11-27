/**
 * The pull request events AO reports into a session's chat (failing CI, review
 * comments, a changes-requested review, merge conflicts), as the desktop and
 * mobile timelines draw them.
 *
 * Local sessions carry them only as AO's message text, which each app reads.
 * AO Cloud sends the event itself with the message (the Cloud contract's
 * ChatAutomation): `cloudPullRequestAutomation` reads that.
 */

export type PullRequestRef = { number: number; title?: string; url?: string };
export type FailedCheck = { name: string; url?: string; log?: string };
export type ReviewComment = { path?: string; line?: number; author: string; body: string };

export type PullRequestAutomation =
	| { kind: "ciFailing"; pr: PullRequestRef; checks: FailedCheck[] }
	// `at` is when the event happened on the forge; absent when AO does not know it.
	| { kind: "reviewComments"; pr: PullRequestRef; comments: ReviewComment[]; at?: string }
	| { kind: "changesRequested"; pr: PullRequestRef; author?: string; body: string; at?: string }
	| { kind: "mergeConflict"; pr: PullRequestRef };

type Fields = Record<string, unknown>;

function fields(value: unknown): Fields | undefined {
	return value && typeof value === "object" && !Array.isArray(value) ? value as Fields : undefined;
}

function text(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

function positive(value: unknown): number | undefined {
	return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}

function pullRequest(value: unknown): PullRequestRef | undefined {
	const pr = fields(value);
	const number = positive(pr?.number);
	if (!pr || number === undefined) return undefined;
	const title = text(pr.title);
	const url = text(pr.url);
	return { number, ...(title ? { title } : {}), ...(url ? { url } : {}) };
}

/**
 * The event a Cloud `chat.user_message` (or the steer that carried it) reports,
 * from its payload's `automation`; undefined for a message that reports none,
 * a kind this build does not know, or a malformed one. The message's text is
 * shown instead then.
 */
export function cloudPullRequestAutomation(value: unknown): PullRequestAutomation | undefined {
	const automation = fields(value);
	const pr = pullRequest(automation?.pullRequest);
	if (!automation || !pr) return undefined;
	const at = text(automation.at);
	switch (automation.kind) {
		case "merge_conflict":
			return { kind: "mergeConflict", pr };
		case "ci_failing": {
			const checks = Array.isArray(automation.checks)
				? automation.checks.flatMap((entry): FailedCheck[] => {
					const check = fields(entry);
					const name = text(check?.name);
					const url = text(check?.url);
					return name ? [{ name, ...(url ? { url } : {}) }] : [];
				})
				: [];
			return checks.length > 0 ? { kind: "ciFailing", pr, checks } : undefined;
		}
		case "review_comment": {
			const comment = fields(automation.comment);
			const author = text(comment?.author);
			const body = typeof comment?.body === "string" ? comment.body : undefined;
			if (!comment || !author || body === undefined) return undefined;
			const path = text(comment.path);
			const line = positive(comment.line);
			return {
				kind: "reviewComments", pr,
				comments: [{ author, body, ...(path ? { path } : {}), ...(line ? { line } : {}) }],
				...(at ? { at } : {}),
			};
		}
		case "changes_requested": {
			const review = fields(automation.review);
			if (!review || typeof review.body !== "string") return undefined;
			const author = text(review.author);
			return { kind: "changesRequested", pr, body: review.body, ...(author ? { author } : {}), ...(at ? { at } : {}) };
		}
		default:
			return undefined;
	}
}
