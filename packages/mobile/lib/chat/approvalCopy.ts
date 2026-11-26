import type { ApprovalMode } from "./types";

/*
 * The chat's permission modes as desktop names them (TurnSettingsBar's
 * APPROVAL_COPY and its per-agent variants), so a mode reads the same on the
 * phone as on the computer, in the same order. Keep the names in step. Each
 * one's description is shortened to a single line the phone's picker can show
 * whole: what the mode lets the agent do, at a glance.
 */

export type ApprovalCopy = { label: string; description?: string };

/** AO's generic approval modes, for agents without a vocabulary of their own. */
const APPROVAL_COPY: Record<ApprovalMode, ApprovalCopy> = {
	default: { label: "Use agent permissions", description: "Follows the agent's own settings" },
	"accept-edits": { label: "Accept edits", description: "Edits files without asking" },
	auto: { label: "Auto-approve", description: "Asks only about risky actions" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

const APPROVAL_ORDER: ApprovalMode[] = ["default", "accept-edits", "auto", "bypass-permissions"];

/** Antigravity asks or approves; it has no mode that never asks. */
const AGY_APPROVAL_COPY: Record<ApprovalMode, ApprovalCopy> = {
	...APPROVAL_COPY,
	default: { label: "Ask for approval", description: "Asks before acting" },
	"bypass-permissions": { label: "Auto-approve", description: "Approves everything" },
};

/**
 * Hermes' default governs only its file-edit tool; ordinary shell commands
 * still run without asking, so the copy says what it actually asks about.
 */
const HERMES_APPROVAL_COPY: Record<ApprovalMode, ApprovalCopy> = {
	...APPROVAL_COPY,
	default: { label: "Use agent permissions", description: "Asks before file edits only" },
};

/**
 * Codex has three distinct profiles. Its default is already full access in
 * AO's worktree, so it is named that rather than a fourth, vague "default".
 */
const CODEX_APPROVAL_COPY: Record<ApprovalMode, ApprovalCopy> = {
	default: { label: "Full access", description: "Edits and runs commands freely" },
	"accept-edits": { label: "Ask for approval", description: "Asks before edits and commands" },
	auto: { label: "Approve for me", description: "Asks only about unsafe actions" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

/** Each mode's name and description for an agent. */
export function approvalCopy(harness: string | undefined): Record<ApprovalMode, ApprovalCopy> {
	if (harness === "codex") return CODEX_APPROVAL_COPY;
	if (harness === "agy") return AGY_APPROVAL_COPY;
	if (harness === "hermes") return HERMES_APPROVAL_COPY;
	return APPROVAL_COPY;
}

/**
 * The modes an agent is offered. "Use agent permissions" hands the decision
 * back to the agent rather than choosing one, so the phone lists it last,
 * after the modes that choose; it stays, because a chat that never set a mode
 * runs in it and must show it as the one in force, and be able to go back to
 * it. Where the same mode has a name of its own (Codex's Full access,
 * Antigravity's Ask for approval) it is a real choice and keeps desktop's
 * place, first.
 *
 * A mode the chat already runs in stays listed even where the agent is not
 * offered it (an Antigravity chat saved with bypass-permissions), last, so it
 * shows as the one in force, with its own look, rather than no mode at all.
 */
export function approvalOrder(harness: string | undefined, current?: ApprovalMode): ApprovalMode[] {
	const order = offeredApprovals(harness);
	return current && !order.includes(current) ? [...order, current] : order;
}

/**
 * A mode's name and description as a chat shows it: the agent's own copy for a
 * mode it is offered, else AO's generic copy. A mode kept only because the chat
 * runs in it (Antigravity's bypass-permissions, which its copy names
 * "Auto-approve" like `auto`) would otherwise read the same as another row.
 */
export function approvalChoiceCopy(harness: string | undefined, mode: ApprovalMode): ApprovalCopy {
	return offeredApprovals(harness).includes(mode) ? approvalCopy(harness)[mode] : APPROVAL_COPY[mode];
}

function offeredApprovals(harness: string | undefined): ApprovalMode[] {
	if (harness === "codex") return APPROVAL_ORDER;
	if (harness === "agy") return APPROVAL_ORDER.filter((mode) => mode !== "bypass-permissions");
	return [...APPROVAL_ORDER.filter((mode) => mode !== "default"), "default"];
}

/**
 * The mode whose look (icon, warning colour) a mode takes. Codex names its
 * modes differently: its default has a free hand, so it looks like bypassing,
 * and its accept-edits asks first, so it looks like asking.
 */
export function approvalLook(harness: string | undefined, mode: ApprovalMode): ApprovalMode {
	if (harness !== "codex") return mode;
	return mode === "default" ? "bypass-permissions" : mode === "accept-edits" ? "default" : mode;
}
