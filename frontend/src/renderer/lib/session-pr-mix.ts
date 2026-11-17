import type { TFunction } from "i18next";
import { appI18n, type PluralMessageKey } from "../i18n";
import type { PullRequestFacts } from "../types/workspace";

/** Where one live PR stands, in the words of the daemon's display statuses. */
export type PRMixBucket =
	| "ciFailing"
	| "conflicts"
	| "changesRequested"
	| "awaitingReview"
	| "draft"
	| "waitingOnBase"
	| "approved"
	| "mergeable";

// Most to least in need of a person, the order the card lists them in.
const BUCKET_ORDER: readonly PRMixBucket[] = [
	"ciFailing",
	"conflicts",
	"changesRequested",
	"awaitingReview",
	"draft",
	"waitingOnBase",
	"approved",
	"mergeable",
];

const BUCKET_KEYS: Record<PRMixBucket, PluralMessageKey> = {
	ciFailing: "session.prMix.ciFailing",
	conflicts: "session.prMix.conflicts",
	changesRequested: "session.prMix.changesRequested",
	awaitingReview: "session.prMix.awaitingReview",
	draft: "session.prMix.draft",
	waitingOnBase: "session.prMix.waitingOnBase",
	approved: "session.prMix.approved",
	mergeable: "session.prMix.mergeable",
};

// The display statuses that describe a PR's own state. Worker and AO-loop
// phrases ("Working", "Reviewing", "Fixing CI failures") describe the session,
// not a PR, so a card showing one gets no counts.
const DISPLAY_BUCKETS: Readonly<Record<string, PRMixBucket>> = {
	"CI failing": "ciFailing",
	"Merge conflicts": "conflicts",
	"Changes requested": "changesRequested",
	"Awaiting review": "awaitingReview",
	Draft: "draft",
	"Waiting on base PR": "waitingOnBase",
	Approved: "approved",
	Mergeable: "mergeable",
};

/**
 * Reads one live PR the way the daemon places it on the board
 * (backend/pkg/contract/kanban.go) when no AO loop is turning it: its own
 * problems first, then whether it is still in review, and a PR that would be
 * ready on its own waits while its base is another open PR's branch.
 */
export function prMixBucket(pr: PullRequestFacts): PRMixBucket | undefined {
	if (pr.state === "merged" || pr.state === "closed") return undefined;
	if (pr.state === "draft") return "draft";
	if (pr.ci === "failing") return "ciFailing";
	if (pr.mergeability === "conflicting") return "conflicts";
	if (pr.review === "changes_requested") return "changesRequested";
	const readyOnItsOwn = pr.review === "approved" || pr.mergeability === "mergeable";
	if (!readyOnItsOwn) return "awaitingReview";
	if (pr.stackParent) return "waitingOnBase";
	return pr.mergeability === "mergeable" ? "mergeable" : "approved";
}

export type SessionPRMix = { lead?: string; detail: string };

type PRMixGroup = { bucket: PRMixBucket; count: number; parent?: number };

/**
 * The counts behind a multi-PR session's status line. The daemon's display
 * status speaks for the least ready live PR; when the session's other PRs
 * stand elsewhere, the lead gains its count ("2 awaiting review") and the rest
 * follow ("1 waiting on #8277 · 1 merged"). Nothing when there is one PR,
 * when every live PR agrees, or when these facts do not place any PR where the
 * daemon's phrase does: the card keeps the daemon's phrase rather than
 * contradict it.
 */
export function sessionPRMix(
	session: { displayStatus?: string; prs: readonly PullRequestFacts[] },
	t: TFunction = appI18n.t,
): SessionPRMix | undefined {
	const leadBucket = session.displayStatus ? DISPLAY_BUCKETS[session.displayStatus] : undefined;
	if (!leadBucket) return undefined;
	const groups = new Map<string, PRMixGroup>();
	let merged = 0;
	for (const pr of session.prs) {
		if (pr.state === "merged") merged += 1;
		const bucket = prMixBucket(pr);
		if (!bucket) continue;
		const parent = bucket === "waitingOnBase" ? pr.stackParent : undefined;
		const key = parent === undefined ? bucket : `${bucket}:${parent}`;
		const group = groups.get(key) ?? { bucket, count: 0, parent };
		group.count += 1;
		groups.set(key, group);
	}
	const ordered = [...groups.values()].sort(
		(a, b) => BUCKET_ORDER.indexOf(a.bucket) - BUCKET_ORDER.indexOf(b.bucket) || (a.parent ?? 0) - (b.parent ?? 0),
	);
	const lead = ordered.find((group) => group.bucket === leadBucket);
	if (!lead) return undefined;
	const rest = ordered.filter((group) => group !== lead);
	if (rest.length === 0 && merged === 0) return undefined;
	const phrase = (group: PRMixGroup) => t(BUCKET_KEYS[group.bucket], { count: group.count, number: group.parent });
	const detail = [...rest.map(phrase), ...(merged > 0 ? [t("session.prMix.merged", { count: merged })] : [])];
	// One PR in the lead state keeps the daemon's own phrase.
	return lead.count > 1 ? { lead: phrase(lead), detail: detail.join(" · ") } : { detail: detail.join(" · ") };
}
