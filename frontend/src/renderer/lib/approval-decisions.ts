import type { ActivityKind, DecisionOption } from "../types/conversation";

/**
 * The words an approval's common choices are shown with, whichever provider
 * offered them. The chat card uses these English defaults; the notification
 * bell passes its translations of the same words, so both say the same thing.
 */
export type ApprovalDecisionLabels = {
	allowOnce: string;
	allowAlwaysCommand: string;
	allowAlwaysFiles: string;
	deny: string;
	alwaysDeny: string;
};

export const APPROVAL_DECISION_LABELS: ApprovalDecisionLabels = {
	allowOnce: "Allow once",
	allowAlwaysCommand: "Always allow this command",
	allowAlwaysFiles: "Always allow these file changes",
	deny: "Deny",
	alwaysDeny: "Always deny",
};

export function approvalDecisionKind(decision: DecisionOption): DecisionOption["kind"] | "unknown" {
	return decision.kind ?? "unknown";
}

function approvalDecisionRank(decision: DecisionOption): number {
	switch (approvalDecisionKind(decision)) {
		case "reject_once":
			return 10;
		case "reject_always":
			return 15;
		case "allow_once":
			return 20;
		case "allow_always":
			return 30;
		default:
			return 40;
	}
}

export function approvalDecisionLabel(
	decision: DecisionOption,
	subjectKind?: ActivityKind,
	labels: ApprovalDecisionLabels = APPROVAL_DECISION_LABELS,
): string {
	switch (approvalDecisionKind(decision)) {
		case "allow_always":
			return subjectKind === "file_change" ? labels.allowAlwaysFiles : labels.allowAlwaysCommand;
		case "allow_once":
			return labels.allowOnce;
		case "reject_once":
			return labels.deny;
		case "reject_always":
			return labels.alwaysDeny;
		default:
			return decision.label;
	}
}

export function repeatedDecisionKinds(decisions: DecisionOption[]): Set<string> {
	const seen = new Set<string>();
	const repeated = new Set<string>();
	for (const decision of decisions) {
		const kind = approvalDecisionKind(decision);
		if (!kind) continue;
		if (seen.has(kind)) repeated.add(kind);
		seen.add(kind);
	}
	return repeated;
}

/** Deny first, then allow once, then broader allows, then anything else, keeping the provider's order within each. */
export function orderedApprovalDecisions(decisions: DecisionOption[]): DecisionOption[] {
	return decisions
		.map((decision, index) => ({ decision, index }))
		.sort((left, right) => {
			const byRank = approvalDecisionRank(left.decision) - approvalDecisionRank(right.decision);
			return byRank || left.index - right.index;
		})
		.map(({ decision }) => decision);
}

/**
 * A label for each decision as an approval presents it. Some agents ask a
 * question through a permission request, offering each answer as its own
 * option of the same kind; a generic per-kind label would render identical
 * buttons, so a kind offered more than once keeps the provider's own wording.
 */
export function approvalDecisionLabeler(
	decisions: DecisionOption[],
	subjectKind?: ActivityKind,
	labels: ApprovalDecisionLabels = APPROVAL_DECISION_LABELS,
): (decision: DecisionOption) => string {
	const repeatedKinds = repeatedDecisionKinds(decisions);
	return (decision) =>
		repeatedKinds.has(approvalDecisionKind(decision) ?? "") && decision.label.trim()
			? decision.label
			: approvalDecisionLabel(decision, subjectKind, labels);
}

/** Decisions offered as `{ id, label, kind? }` objects, as a control plane stores them; anything else is dropped. */
export function parseApprovalDecisions(value: unknown): DecisionOption[] {
	if (!Array.isArray(value)) return [];
	const kinds: ReadonlyArray<NonNullable<DecisionOption["kind"]>> = ["allow_once", "allow_always", "reject_once", "reject_always"];
	const decisions: DecisionOption[] = [];
	for (const item of value) {
		if (!item || typeof item !== "object") continue;
		const { id, label, kind } = item as Record<string, unknown>;
		if (typeof id !== "string" || typeof label !== "string") continue;
		const known = kinds.find((candidate) => candidate === kind);
		decisions.push(known ? { id, label, kind: known } : { id, label });
	}
	return decisions;
}
