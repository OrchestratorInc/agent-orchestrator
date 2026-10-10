import { appI18n } from "../../i18n/instance";
import { isSessionLink } from "../../lib/session-links";
import { isWebLink } from "../../lib/external-link-policy";
import type { ActivityStatus, ConversationActivity, ConversationItem } from "../../types/conversation";

export type ActivityDisplayState = "running" | "success" | "failed" | "cancelled" | "recovered" | "pending";

export type ActivityDisplayDescriptor = {
	label: string;
	labelKey: string;
	state: ActivityDisplayState;
	batchPolicy: "ordinary" | "boundary";
	href?: string;
	linkLabelKey?: "chat.action.openSession" | "chat.action.openPullRequest";
	operationId?: string;
	ariaLabel: string;
};

const ACTION_LABELS = {
	"session.spawned": "chat.action.session.spawned",
	"session.terminated": "chat.action.session.terminated",
	"session.renamed": "chat.action.session.renamed",
	"session.restored": "chat.action.session.restored",
	"session.agent_switched": "chat.action.session.agent_switched",
	"pull_request.claimed": "chat.action.pull_request.claimed",
	"pull_request.created": "chat.action.pull_request.created",
} as const;

type AOActionName = keyof typeof ACTION_LABELS;

const COUNT_KEYS = {
	"session.spawned": "chat.action.count.session.spawned",
	"session.terminated": "chat.action.count.session.terminated",
	"session.renamed": "chat.action.count.session.renamed",
	"session.restored": "chat.action.count.session.restored",
	"session.agent_switched": "chat.action.count.session.agent_switched",
	"pull_request.claimed": "chat.action.count.pull_request.claimed",
	"pull_request.created": "chat.action.count.pull_request.created",
} as const;

export function isAOActionName(value: string): value is AOActionName {
	return Object.prototype.hasOwnProperty.call(ACTION_LABELS, value);
}

export function aoActionName(activity: ConversationActivity): string {
	const action = activity.detail?.action;
	return typeof action === "string" ? action : "";
}

export function summarizeAOActions(activities: ConversationActivity[]): string {
	const action = aoActionName(activities[0] ?? activityStub());
	const count = activities.length;
	if (isAOActionName(action) && activities.every((item) => aoActionName(item) === action)) {
		return appI18n.t(COUNT_KEYS[action], { count });
	}
	return appI18n.t("chat.action.performed", { count });
}

function activityStub(): ConversationActivity {
	return {
		kind: "activity",
		id: "",
		sequence: 0,
		revision: 0,
		activityKind: "ao_action",
		status: "completed",
		summary: "",
		createdAt: "",
	};
}

export function describeConversationActivity(activity: ConversationActivity): ActivityDisplayDescriptor | undefined {
	if (activity.activityKind !== "ao_action") return undefined;
	const action = aoActionName(activity);
	const known = isAOActionName(action);
	const labelKey = known ? ACTION_LABELS[action] : "chat.action.unknown";
	const label = appI18n.t(labelKey);
	const href = safeActionHref(activity.detail?.href);
	const state = displayState(activity.status);
	const operationId = text(activity.detail?.operationId);
	const ariaLabel = [label, statusLabel(state), text(activity.detail?.displayName)].filter(Boolean).join(", ");
	return {
		label,
		labelKey,
		state,
		batchPolicy: "ordinary",
		href,
		linkLabelKey: href ? (isSessionLink(href) ? "chat.action.openSession" : "chat.action.openPullRequest") : undefined,
		operationId,
		ariaLabel,
	};
}

export function isOrdinaryActivity(item: ConversationItem): boolean {
	return item.kind === "activity"
		&& item.activityKind !== "approval"
		&& item.activityKind !== "user_input"
		&& item.activityKind !== "error"
		&& item.activityKind !== "reasoning"
		&& item.detail?.event === undefined;
}

export function actionDetailLines(activity: ConversationActivity): string[] {
	const detail = activity.detail;
	if (!detail) return [];
	const lines: string[] = [];
	const previousName = text(detail.previousDisplayName);
	const name = text(detail.displayName);
	if (previousName && name && previousName !== name) lines.push(`${previousName} -> ${name}`);
	else if (name) lines.push(name);
	const previousHarness = text(detail.previousHarness);
	const harness = text(detail.harness);
	if (previousHarness && harness && previousHarness !== harness) lines.push(`${previousHarness} -> ${harness}`);
	else if (harness) lines.push(harness);
	const title = text(detail.prTitle);
	const number = typeof detail.prNumber === "number" && detail.prNumber > 0 ? `#${detail.prNumber}` : "";
	if (title || number) lines.push([number, title].filter(Boolean).join(" "));
	const descriptor = describeConversationActivity(activity);
	if (descriptor) lines.push(statusLabel(descriptor.state));
	const error = text(detail.error);
	if (error) lines.push(error);
	return lines;
}

function displayState(status: ActivityStatus): ActivityDisplayState {
	switch (status) {
		case "running":
			return "running";
		case "completed":
			return "success";
		case "failed":
			return "failed";
		case "cancelled":
			return "cancelled";
		case "recovered":
			return "recovered";
		case "pending":
		case "resolved":
			return "pending";
		default:
			return "pending";
	}
}

function statusLabel(state: ActivityDisplayState): string {
	switch (state) {
		case "running":
			return appI18n.t("chat.action.status.running");
		case "success":
			return appI18n.t("chat.action.status.completed");
		case "failed":
			return appI18n.t("chat.action.status.failed");
		case "cancelled":
			return appI18n.t("chat.action.status.cancelled");
		case "recovered":
			return appI18n.t("chat.action.status.recovered");
		case "pending":
			return appI18n.t("chat.action.status.pending");
		default:
			return appI18n.t("chat.action.status.pending");
	}
}

function safeActionHref(value: unknown): string | undefined {
	if (typeof value !== "string") return undefined;
	const href = value.trim();
	if (isSessionLink(href)) return href;
	if (!isWebLink(href)) return undefined;
	try {
		const url = new URL(href);
		if (url.protocol !== "https:" || url.username || url.password) return undefined;
		return url.toString();
	} catch {
		return undefined;
	}
}

function text(value: unknown): string {
	return typeof value === "string" ? value.trim() : "";
}
