import {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	getAgentActivityView as getPortableAgentActivityView,
	getAttentionZoneView as getPortableAttentionZoneView,
	getAttentionZoneViewForZone as getPortableAttentionZoneViewForZone,
	getKanbanColumnView as getPortableKanbanColumnView,
	getSessionStatusView as getPortableSessionStatusView,
	getSessionTimelinePillView as getPortableSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
	type AgentActivityView,
	type AttentionZone,
	type AttentionZoneView,
	type KanbanColumn,
	type KanbanColumnView,
	type ProductUITranslator,
	type SessionStatusView,
	type SessionTimelinePillStatus,
	type SessionTimelinePillView,
} from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { appI18n, type MessageKey } from "../i18n";
import { toKanbanColumn, type SessionActivity, type SessionStatus } from "../types/workspace";

function translator(t: TFunction): ProductUITranslator {
	return (key, values) => t(key as MessageKey, values);
}

export function getAgentActivityView(
	activity?: SessionActivity | null,
	t: TFunction = appI18n.t,
): AgentActivityView {
	return getPortableAgentActivityView(activity, translator(t));
}

export function getSessionStatusView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): SessionStatusView {
	return getPortableSessionStatusView(status, translator(t));
}

export function getAttentionZoneView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneView(status, translator(t));
}

export function getAttentionZoneViewForZone(
	zone: AttentionZone,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneViewForZone(zone, translator(t));
}

export type SessionStatusDotView = {
	className: string;
	breathe: boolean;
	/** The agent is waiting on the user; the sidebar swaps the dot for an alert icon. */
	needsInput?: true;
};

// The session dot carries two independent signals. Colour matches the card on
// the board: the lane the session sits in (building, validating, in review,
// ready; a merged PR is purple), which survives a running agent — `status` is
// activity-first, so it collapses to `working` the moment an agent wakes and
// would otherwise take every pull request tone with it. A session that needs
// the user overrides the lane with the card's needs-attention tone. Finished
// sessions (archive lane) go grey.
//
// Motion stays on raw agent activity, except once the PR is done for good
// (merged or closed): the card drops its loader there, so the dot holds still
// even if the agent keeps running.
export function getSessionStatusDotView(
	session: {
		activity?: SessionActivity | null;
		displayStatus?: string;
		kanbanColumn?: KanbanColumn;
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusDotView {
	const breathe = isAgentActivityWorking(session.activity);
	const sectionStatus: SessionStatus = session.scmStatus ?? session.status;
	const dot = (className: string): SessionStatusDotView => ({ className, breathe });

	if (session.displayStatus === "Closed without merge") {
		return { className: getSessionStatusView("exited", t).dotClassName, breathe: false };
	}
	if (sectionStatus === "exited") return dot("bg-passive");
	if (sessionNeedsInput(session)) return { ...dot("bg-status-needs-you"), needsInput: true };
	if (sessionNeedsYou(session)) return dot("bg-status-needs-you");

	const column = toKanbanColumn(session.kanbanColumn, sectionStatus);
	// The card paints a merged PR purple inside the Ready lane; the dot follows.
	const merged = session.displayStatus ? session.displayStatus === "Merged" : sectionStatus === "merged";
	if (column === "ready" && merged) {
		return { className: getSessionStatusView("merged", t).dotClassName, breathe: false };
	}
	return dot(getKanbanColumnView(column, t).dotClassName);
}

// The agent has stopped and is waiting on the user (a question or a decision).
function sessionNeedsInput(session: {
	activity?: SessionActivity | null;
	displayStatus?: string;
	status: SessionStatus;
}): boolean {
	return (
		session.status === "needs_input" ||
		session.activity?.state === "waiting_input" ||
		session.activity?.state === "blocked" ||
		session.displayStatus === "Blocked"
	);
}

// Mirrors the board card's needs-attention check; waiting on input is handled
// before this by sessionNeedsInput.
function sessionNeedsYou(session: {
	displayStatus?: string;
	status: SessionStatus;
}): boolean {
	switch (session.displayStatus) {
		case "CI failing":
		case "Changes requested":
			return true;
		case undefined:
			// A daemon too old to send `displayStatus`: the card falls back to the zone.
			return attentionZone(session.status) === "action";
		default:
			return false;
	}
}

export function getKanbanColumnView(
	column: KanbanColumn,
	t: TFunction = appI18n.t,
): KanbanColumnView {
	return getPortableKanbanColumnView(column, translator(t));
}

export function getSessionTimelinePillView(
	status: SessionTimelinePillStatus,
	t: TFunction = appI18n.t,
): SessionTimelinePillView {
	return getPortableSessionTimelinePillView(status, translator(t));
}

/** Live labels for the current locale (getters re-resolve on each access). */
export const attentionZoneLabel: Record<AttentionZone, string> = {
	get merge() {
		return getAttentionZoneViewForZone("merge").label;
	},
	get action() {
		return getAttentionZoneViewForZone("action").label;
	},
	get pending() {
		return getAttentionZoneViewForZone("pending").label;
	},
	get working() {
		return getAttentionZoneViewForZone("working").label;
	},
	get done() {
		return getAttentionZoneViewForZone("done").label;
	},
};

export {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	isAgentActivityWorking,
	isSessionIdle,
};
export type {
	AgentActivityView,
	AttentionZone,
	AttentionZoneView,
	KanbanColumnView,
	SessionStatusView,
	SessionTimelinePillStatus,
	SessionTimelinePillView,
};
