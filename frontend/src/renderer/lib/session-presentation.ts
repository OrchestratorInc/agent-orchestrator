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
	needsInput?: true;
};

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
	const merged = session.displayStatus ? session.displayStatus === "Merged" : sectionStatus === "merged";
	if (column === "ready" && merged) {
		return { className: getSessionStatusView("merged", t).dotClassName, breathe: false };
	}
	return dot(getKanbanColumnView(column, t).dotClassName);
}

export type SessionRowMarker = "attention" | "ready" | "review" | "merged";

export function getSessionRowMarker(session: {
	activity?: SessionActivity | null;
	displayStatus?: string;
	kanbanColumn?: KanbanColumn;
	scmStatus?: SessionStatus;
	status: SessionStatus;
}): SessionRowMarker | null {
	const sectionStatus: SessionStatus = session.scmStatus ?? session.status;
	const display = session.displayStatus;
	if (display === "Terminated" || sectionStatus === "terminated" || display === "Closed without merge") return null;

	if (sessionNeedsInput(session)) return "attention";
	if (display === "CI failing" || display === "Merge conflicts" || display === "Changes requested") return "attention";
	if (!display && (session.status === "ci_failed" || session.status === "changes_requested")) {
		return "attention";
	}

	switch (toKanbanColumn(session.kanbanColumn, sectionStatus)) {
		case "needs_review":
			return (display ? display === "Draft" : sectionStatus === "draft") ? null : "review";
		case "validating":
			return display === "Review failed" ? "review" : null;
		case "ready":
			if (display ? display === "Merged" : sectionStatus === "merged") return "merged";
			return (display ? display === "Mergeable" || display === "Approved" : sectionStatus === "mergeable" || sectionStatus === "approved")
				? "ready"
				: null;
		default:
			return null;
	}
}

export function isSessionRowWorking(session: {
	activity?: SessionActivity | null;
	displayStatus?: string;
	provisionState?: string;
}): boolean {
	if (session.displayStatus === "Terminated") return false;
	return session.provisionState === "provisioning" || isAgentActivityWorking(session.activity);
}

export function getSessionRowIndicator(
	session: Parameters<typeof getSessionRowMarker>[0] & Parameters<typeof isSessionRowWorking>[0],
): "working" | SessionRowMarker | null {
	return isSessionRowWorking(session) ? "working" : getSessionRowMarker(session);
}

export function sessionNeedsInput(session: {
	activity?: SessionActivity | null;
	displayStatus?: string;
	status: SessionStatus;
}): boolean {
	return (
		session.status === "needs_input" ||
		session.activity?.state === "waiting_input" ||
		session.activity?.state === "blocked" ||
		session.displayStatus === "Blocked" ||
		session.displayStatus === "Needs input"
	);
}

function sessionNeedsYou(session: {
	displayStatus?: string;
	status: SessionStatus;
}): boolean {
	switch (session.displayStatus) {
		case "CI failing":
		case "Merge conflicts":
		case "Changes requested":
			return true;
		case undefined:
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
