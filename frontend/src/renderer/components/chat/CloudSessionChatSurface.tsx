import { CLOUD_CHAT_MESSAGE_BYTES, EXCERPT_PREAMBLE, isExcerptSnapshot, formatSnapshotExcerpt, formatNumberedAnnotations } from "../../../shared/chat-excerpt";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ChatFileReference } from "../../../shared/chat-file-reference";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useChatModelChoices } from "../../hooks/useChatModelChoices";
import { useCloudCp } from "../../hooks/useCloudCp";
import { invalidateCloudModelCatalogs } from "../../hooks/useCloudModelCatalog";
import { useCloudOffering } from "../../hooks/useCloudOffering";
import { QUEUED_DELETE_FEATURE, QUEUED_EDIT_FEATURE, QUEUED_STEER_FEATURE } from "../../lib/cloud-offering";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { useChatMessageFocus, useChatNavigationRequest, type ChatMessageFocusTarget } from "../../hooks/useChatMessageFocus";
import { useCloudPromptSuggestion } from "../../hooks/useCloudPromptSuggestion";
import { sessionUiKey } from "../../lib/hosts";
import { useCloudSessionLive } from "../../hooks/useCloudSessionLive";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { useCloudSessionEventStream } from "../../hooks/useCloudSessionEvents";
import { useComposerLease } from "../../hooks/useComposerLease";
import { cloudSessionsQueryKey, refreshCloudSessionLifecycle, sharedCloudWorkspacesQueryKey } from "../../hooks/useWorkspaceQuery";
import { useUiStore } from "../../stores/ui-store";
import { sessionArtifactPath } from "../../lib/session-links";
import { appI18n } from "../../i18n";
import { CLOUD_AGENT_PROVIDERS } from "../../lib/cloud-agents";
import type { CloudCpAgentProvider, CloudCpClientEvent, CloudCpSessionChatSettings } from "../../lib/cloud-cp";
import { cloudStartupProblem, isWorkspaceStartupError, resumeCloudSession, type CloudStartupProblem } from "../../lib/cloud-lifecycle";
import { forgetCloudOpening, useCloudOpening } from "../../lib/cloud-opening";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { cloudSessionScriptsEnded, groupScriptRuns, parseScriptStepPayload, SCRIPT_STEP_EVENT, type ScriptRun } from "../../lib/cloud-script-runs";
import { cloudChatHistoryKey, loadCloudChatHistory, loadOlderCloudChatEvents, mergeCloudEvents, streamedCloudChatEvents, type CloudChatHistory } from "../../lib/cloud-chat-history";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import type { ActivityKind, ApprovalMode, ChatConfigOption, ConversationActivity, ConversationContentSummary, ConversationItem, ConversationMessage, ConversationPlan, ConversationSnapshot, ConversationTurn, ConversationUsage, DiffFile, FileChangeFile, PlanStep, SystemEventDetail, TurnSettings } from "../../types/conversation";
import type { TerminalTarget } from "../../types/terminal";
import type { SessionProvisionStep, WorkspaceSession } from "../../types/workspace";
import { requestWorkspaceRetry } from "../CloudSessionStartupError";
import { scriptRunStatusText, scriptRunTitle } from "../ScriptRunPanel";
import { agentLabel } from "../../lib/agent-options";
import { formatEffortLabel } from "../settings/EffortPicker";
import { cloudModelLabel } from "../../lib/agent-model-choices";
import { ComposerErrorNotice, switchEffort, type AgentSwitchProps, type PendingAgentSwitch } from "./AgentSwitchPicker";
import { AGENT_SWITCH_FAILED } from "./ChatComposer";
import { useCloudAgentSwitch, type AgentSwitchTransition } from "../../hooks/useCloudAgentSwitch";
import { apiErrorMessage } from "../../lib/api-client";
import { apiErrorException } from "../../lib/api-error-exception";
import { ChatWorkspace } from "./ChatWorkspace";
import { AdaptiveStreamPacingContext } from "./ChatTimelineItems";
import { MAX_CLOUD_CP_BINARY_BYTES } from "../../../shared/cloud-binary";
import { CloudAttachmentSourceProvider } from "./CloudMessageAttachments";
import { cloudAttachmentReference, cloudMessageAttachments, uploadCloudAttachment, type CloudAttachment } from "../../lib/cloud-attachments";
import type { ApprovalModeLimits } from "./TurnSettingsBar";
import { SharedComposerNotice } from "./SharedComposerNotice";
import { artifactAtPath } from "../../../../../packages/shared/artifacts/deliverables";

type EventPayload = {
	attempt?: unknown;
	reason?: unknown;
	attachments?: unknown;
	status?: unknown;
	step?: unknown;
	exitCode?: unknown;
	durationMs?: unknown;
	output?: unknown;
	command?: unknown;
	dir?: unknown;
	user?: unknown;
	groups?: unknown;
	logPath?: unknown;
	hint?: unknown;
	clientMessageId?: unknown;
	requestId?: unknown;
	decision?: unknown;
	decisions?: unknown;
	summary?: unknown;
	toolKind?: unknown;
	steering?: unknown;
	queuedTurnId?: unknown;
	deleteRequested?: unknown;
	error?: unknown;
	text?: unknown;
	model?: unknown;
	harness?: unknown;
	reasoningEffort?: unknown;
	origin?: unknown;
	senderLabel?: unknown;
	senderSessionId?: unknown;
	senderProjectId?: unknown;
	senderDisplayName?: unknown;
	displayText?: unknown;
	turnId?: unknown;
	activity?: unknown;
	itemId?: unknown;
	authorUserId?: unknown;
	authorName?: unknown;
	subjectKind?: unknown;
	content?: unknown;
};

type CloudTurnSettings = TurnSettings & { executionMode?: "agent" | "plan" };

function allowedApprovalModes(harness: string, ceiling?: "read-only" | "standard" | "trusted"): ApprovalMode[] {
	if (ceiling === "read-only" || !ceiling) return [];
	if (ceiling === "standard") return harness === "codex" ? ["accept-edits", "auto"] : ["default", "accept-edits", "auto"];
	return ["default", "accept-edits", "auto", "bypass-permissions"];
}

function defaultApprovalMode(harness: string, modes: ApprovalMode[], ceiling?: "read-only" | "standard" | "trusted"): ApprovalMode | undefined {
	const preferred: ApprovalMode | undefined = ceiling === "trusted"
		? "bypass-permissions"
		: ceiling === "standard" ? (harness === "codex" ? "auto" : "accept-edits") : undefined;
	return preferred && modes.includes(preferred) ? preferred : modes[0];
}

const APPROVAL_STEP_DOWN: ApprovalMode[] = ["bypass-permissions", "auto", "accept-edits", "default"];
const CODEX_APPROVAL_STEP_DOWN: ApprovalMode[] = ["bypass-permissions", "default", "auto", "accept-edits"];

export function effectiveApprovalMode(harness: string, wanted: ApprovalMode, offered: readonly ApprovalMode[]): ApprovalMode | undefined {
	const order = harness === "codex" ? CODEX_APPROVAL_STEP_DOWN : APPROVAL_STEP_DOWN;
	const start = order.indexOf(wanted);
	if (start < 0) return offered.includes(wanted) ? wanted : undefined;
	return order.slice(start).find((mode) => offered.includes(mode));
}

const CLAUDE_MODE_APPROVAL = new Map<string, ApprovalMode>([
	["default", "default"],
	["acceptEdits", "accept-edits"],
	["auto", "auto"],
	["bypassPermissions", "bypass-permissions"],
]);

function planExitApprovalMode(approval: ConversationItem | undefined, decisionId: string): ApprovalMode | undefined {
	if (approval?.kind !== "activity" || approval.activityKind !== "approval" || approval.detail?.toolKind !== "switch_mode") return undefined;
	return CLAUDE_MODE_APPROVAL.get(decisionId);
}

function modelApprovalModes(sessionModes: ApprovalMode[], model?: { approvalModes?: ApprovalMode[] }): ApprovalMode[] {
	const offered = model?.approvalModes;
	return offered ? sessionModes.filter((mode) => offered.includes(mode)) : sessionModes;
}

function savedApprovalMode(value: unknown): ApprovalMode | undefined {
	return value === "default" || value === "accept-edits" || value === "auto" || value === "bypass-permissions" ? value : undefined;
}

export function readCloudTurnSettings(key: string): CloudTurnSettings {
	try {
		const saved = JSON.parse(localStorage.getItem(key) ?? "null");
		if (!saved || typeof saved !== "object") return {};
		return {
			executionMode: saved.executionMode === "plan" || saved.executionMode === "agent" ? saved.executionMode : undefined,
			model: typeof saved.model === "string" ? saved.model : undefined,
			reasoningEffort: typeof saved.reasoningEffort === "string" ? saved.reasoningEffort : undefined,
			approvalMode: saved.approvalModeChosen === true ? savedApprovalMode(saved.approvalMode) : undefined,
		};
	} catch {
		return {};
	}
}

function writeCloudTurnSettings(key: string, settings: CloudTurnSettings) {
	const { approvalMode, ...rest } = settings;
	localStorage.setItem(key, JSON.stringify(approvalMode ? { ...rest, approvalMode, approvalModeChosen: true } : rest));
}

function rememberAgentSwitchPick(key: string, pick: PendingAgentSwitch) {
	const reasoningEffort = switchEffort(pick);
	if (!pick.model && !reasoningEffort) return;
	try {
		writeCloudTurnSettings(key, { ...readCloudTurnSettings(key), model: pick.model || undefined, reasoningEffort });
	} catch {
	}
}

function readSeenNativeSettings(key: string): { model?: string; reasoningEffort?: string } | undefined {
	try {
		const saved = JSON.parse(localStorage.getItem(key) ?? "null");
		if (!saved || typeof saved !== "object") return undefined;
		return {
			model: typeof saved.model === "string" ? saved.model : undefined,
			reasoningEffort: typeof saved.reasoningEffort === "string" ? saved.reasoningEffort : undefined,
		};
	} catch {
		return undefined;
	}
}

function writeSeenNativeSettings(key: string, native: { model?: string; reasoningEffort?: string }) {
	try {
		localStorage.setItem(key, JSON.stringify({ model: native.model || undefined, reasoningEffort: native.reasoningEffort || undefined }));
	} catch {
	}
}

function readRememberedApprovalMode(key: string): ApprovalMode | undefined {
	try {
		const saved = JSON.parse(localStorage.getItem(key) ?? "null");
		return saved && typeof saved === "object" ? savedApprovalMode(saved.approvalMode) : undefined;
	} catch {
		return undefined;
	}
}

function eventPayload(event: CloudCpClientEvent): EventPayload {
	return event.payload && typeof event.payload === "object" ? (event.payload as EventPayload) : {};
}

function eventAttachments(event: CloudCpClientEvent): CloudAttachment[] {
	const listed = eventPayload(event).attachments;
	if (!Array.isArray(listed)) return [];
	return listed.flatMap((item: unknown) => {
		if (!item || typeof item !== "object") return [];
		const { id, fileName, contentType, size } = item as Record<string, unknown>;
		return typeof id === "string" && typeof fileName === "string" && typeof contentType === "string" && typeof size === "number"
			? [{ id, fileName, contentType, size }] : [];
	});
}

function eventText(event: CloudCpClientEvent): string | undefined {
	const text = eventPayload(event).text;
	return typeof text === "string" && text.trim() !== "" ? text : undefined;
}

function queuedSteerTurnID(event: CloudCpClientEvent): string | undefined {
	if (event.type !== "chat.turn_steer_requested" && event.type !== "chat.turn_steered" && event.type !== "chat.turn_steer_failed") return undefined;
	const queuedTurnID = eventPayload(event).queuedTurnId;
	return typeof queuedTurnID === "string" && queuedTurnID ? queuedTurnID : undefined;
}

function steerOrigin(payload: ReturnType<typeof eventPayload>): { origin: string; senderDisplayName?: string } {
	if (payload.origin === "automation") {
		return { origin: "automation", ...(typeof payload.senderLabel === "string" && payload.senderLabel ? { senderDisplayName: payload.senderLabel } : {}) };
	}
	return { origin: "human" };
}

export const QUEUED_STEER_STALE_MS = 90_000;

function queuedSteerStale(requested: CloudCpClientEvent, now: number | undefined): boolean {
	return now !== undefined && Date.parse(requested.createdAt) + QUEUED_STEER_STALE_MS <= now;
}

export function couldNotSteer(reason?: string): string {
	const why = reason?.trim().replace(/\.$/, "");
	return `Couldn't steer${why ? `: ${why}` : ""}. It's back in the queue; select Steer to try again.`;
}

export function cloudQueuedSteerErrors(events: readonly CloudCpClientEvent[], now?: number): Record<string, string> {
	const errors: Record<string, string> = {};
	const pending = new Map<string, CloudCpClientEvent>();
	for (const event of events) {
		const queuedTurnID = queuedSteerTurnID(event);
		if (!queuedTurnID) continue;
		delete errors[queuedTurnID];
		pending.delete(queuedTurnID);
		if (event.type === "chat.turn_steer_requested") pending.set(queuedTurnID, event);
		if (event.type !== "chat.turn_steer_failed") continue;
		const reason = eventPayload(event).error;
		errors[queuedTurnID] = couldNotSteer(typeof reason === "string" ? reason : undefined);
	}
	for (const [queuedTurnID, requested] of pending) {
		if (queuedSteerStale(requested, now)) errors[queuedTurnID] = couldNotSteer("the agent did not answer in time");
	}
	return errors;
}

export function cloudSteeringQueuedTurns(events: readonly CloudCpClientEvent[], now?: number): Set<string> {
	const pending = new Map<string, CloudCpClientEvent>();
	const ended = new Set<string>();
	for (const event of events) {
		const queuedTurnID = queuedSteerTurnID(event);
		if (queuedTurnID) {
			if (event.type === "chat.turn_steer_requested") pending.set(queuedTurnID, event);
			else pending.delete(queuedTurnID);
			continue;
		}
		const turnID = eventTurnID(event);
		if (turnID && (event.type === "chat.turn_interrupted" || event.type === "chat.turn_started" || event.type === "chat.turn_completed" || event.type === "chat.turn_aborted")) ended.add(turnID);
	}
	return new Set([...pending].filter(([turnID, requested]) => !ended.has(turnID) && !queuedSteerStale(requested, now)).map(([turnID]) => turnID));
}

export function nextQueuedSteerDeadline(events: readonly CloudCpClientEvent[], after: number): number | undefined {
	const pending = new Map<string, CloudCpClientEvent>();
	for (const event of events) {
		const queuedTurnID = queuedSteerTurnID(event);
		if (!queuedTurnID) continue;
		if (event.type === "chat.turn_steer_requested") pending.set(queuedTurnID, event);
		else pending.delete(queuedTurnID);
	}
	const deadlines = [...pending.values()].map((event) => Date.parse(event.createdAt) + QUEUED_STEER_STALE_MS).filter((deadline) => deadline > after);
	return deadlines.length > 0 ? Math.min(...deadlines) : undefined;
}

function queuedSteerRefusal(error: unknown): Error {
	if (!(error instanceof CloudCpError)) {
		return new Error(couldNotSteer(error instanceof TypeError || !(error instanceof Error)
			? "AO Cloud could not be reached" : error.message));
	}
	switch (error.code) {
		case "TURN_NOT_STEERABLE":
			return new Error(couldNotSteer("the agent's running turn can't take guidance"));
		case "STEER_CONTENT_UNSUPPORTED":
			return new Error(couldNotSteer("a steer carries text alone, and this message has files"));
		case "TURN_NOT_QUEUED":
			return new Error("This message is no longer queued: the agent started on it, or it ended.");
		case "STEER_IN_PROGRESS":
			return new Error("This message is already being steered into the running turn.");
		default:
			return new Error(couldNotSteer(error.message));
	}
}

export function lastChosenChatSettings(events: readonly CloudCpClientEvent[] | undefined): Pick<CloudTurnSettings, "model" | "reasoningEffort"> | undefined {
	let reasoningEffort: string | undefined;
	for (let index = (events?.length ?? 0) - 1; index >= 0; index--) {
		const event = events![index];
		if (event.type !== "chat.user_message") continue;
		const payload = eventPayload(event);
		if (reasoningEffort === undefined && typeof payload.reasoningEffort === "string" && payload.reasoningEffort !== "") reasoningEffort = payload.reasoningEffort;
		if (typeof payload.model === "string" && payload.model !== "") return { model: payload.model, reasoningEffort };
	}
	return undefined;
}

function eventTurnID(event: CloudCpClientEvent): string | undefined {
	const turnID = eventPayload(event).turnId;
	return typeof turnID === "string" && turnID !== "" ? turnID : undefined;
}

export function ownMessageTurns(events: readonly CloudCpClientEvent[] | undefined, viewerUserId: string | undefined): Set<string> {
	const turns = new Set<string>();
	if (!viewerUserId) return turns;
	for (const event of events ?? []) {
		const turnID = eventTurnID(event);
		if (event.type === "chat.user_message" && turnID && eventPayload(event).authorUserId === viewerUserId) turns.add(turnID);
	}
	return turns;
}

export class AlreadyDeliveredError extends Error {
	constructor() {
		super("Not deleted: the agent had already taken this message as a steer into its running turn, so it was delivered.");
		this.name = "AlreadyDeliveredError";
	}
}

function queuedDeleteRefusal(error: unknown): Error | undefined {
	if (!(error instanceof CloudCpError)) return error instanceof Error ? error : new Error("The message could not be deleted. Try again.");
	switch (error.code) {
		case "TURN_FINISHED":
			return undefined;
		case "TURN_NOT_QUEUED":
			return new Error("This message is no longer queued, so it was not deleted. If the agent started on it, stop that turn instead.");
		case "SESSION_TERMINATED":
			return new Error("This session has ended, so this message can no longer be deleted.");
		case "ALREADY_STEERED":
			return new AlreadyDeliveredError();
		case "STEER_IN_PROGRESS":
			return new Error("This message is being steered into the running turn, so it can't be deleted now. Try again once the steer settles.");
		case "forbidden":
			return new Error("Only the person who sent this message, whoever started the session, or a workspace admin can delete it.");
		default:
			return error;
	}
}

export function appendCloudEvents(existing: CloudCpClientEvent[], incoming: CloudCpClientEvent[]): CloudCpClientEvent[] {
	const lastSequence = existing.at(-1)?.sequence ?? 0;
	return [...existing, ...incoming.filter((event) => event.sequence > lastSequence)];
}

const STREAM_FLUSH_MS = 32;
export const CHAT_EVENTS_SAFETY_POLL_MS = 10_000;
export const CHAT_EVENTS_FALLBACK_POLL_MS = 1_000;
export const CHAT_EVENTS_CACHE_MS = 60 * 60_000;

function useCloudChatEventStream(orgId: string | undefined, sessionId: string, enabled: boolean): boolean {
	const { baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const batch = useRef<{ key: string; events: CloudCpClientEvent[]; timer?: number }>({ key: "", events: [] });
	const streamKey = `${orgId ?? ""}|${sessionId}`;
	useEffect(() => () => {
		if (batch.current.timer !== undefined) window.clearTimeout(batch.current.timer);
		batch.current = { key: "", events: [] };
	}, [streamKey]);
	const onEvent = useCallback((event: CloudCpClientEvent) => {
		if (!orgId || event.sessionId !== sessionId || typeof event.sequence !== "number") return;
		if (batch.current.key !== streamKey) batch.current = { key: streamKey, events: [] };
		const pending = batch.current;
		pending.events.push(event);
		pending.timer ??= window.setTimeout(() => {
			pending.timer = undefined;
			const events = pending.events;
			pending.events = [];
			if (!events.length || batch.current !== pending) return;
			queryClient.setQueryData<CloudCpClientEvent[]>(["cloud-chat-events", orgId, sessionId], (current) =>
				mergeCloudEvents(current ?? [], streamedCloudChatEvents(queryClient, orgId, sessionId, current ?? [], events)));
		}, STREAM_FLUSH_MS);
	}, [orgId, queryClient, sessionId, streamKey]);
	const resumeAfter = useCallback(() => {
		const loaded = queryClient.getQueryData<CloudCpClientEvent[]>(["cloud-chat-events", orgId ?? "", sessionId]);
		return loaded ? loaded.at(-1)?.sequence ?? 0 : "loading" as const;
	}, [orgId, queryClient, sessionId]);
	return useCloudSessionEventStream({ baseUrl, orgId, sessionId, enabled, onEvent, resumeAfter });
}

function cloudPlan(detail: Record<string, unknown>): ConversationPlan | undefined {
	const raw = detail.plan;
	if (!raw || typeof raw !== "object") return undefined;
	const plan = raw as Record<string, unknown>;
	if (!Array.isArray(plan.steps)) return undefined;
	const steps = plan.steps.flatMap((value): PlanStep[] => {
		if (!value || typeof value !== "object") return [];
		const step = value as Record<string, unknown>;
		if (typeof step.text !== "string") return [];
		const status = step.status === "in_progress" || step.status === "completed" ? step.status : "pending";
		return [{ text: step.text, status }];
	});
	return {
		steps,
		...(typeof plan.explanation === "string" && plan.explanation.trim() !== "" ? { explanation: plan.explanation } : {}),
		...(plan.spansTurns === true ? { spansTurns: true } : {}),
	};
}

function cloudUsage(detail: Record<string, unknown>): ConversationUsage | undefined {
	const raw = detail.usage;
	if (!raw || typeof raw !== "object") return undefined;
	const usage = raw as Record<string, unknown>;
	const count = (value: unknown) => (typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0);
	if (typeof usage.contextUsed !== "number") return undefined;
	return {
		contextUsed: count(usage.contextUsed),
		contextWindow: count(usage.contextWindow),
		inputTokens: count(usage.inputTokens),
		outputTokens: count(usage.outputTokens),
		cachedTokens: count(usage.cachedTokens),
		totalTokens: count(usage.totalTokens),
		...(typeof usage.cost === "number" ? { cost: usage.cost } : {}),
		...(typeof usage.currency === "string" ? { currency: usage.currency } : {}),
	};
}

const APPROVAL_SUBJECT_KINDS: readonly ActivityKind[] = ["command", "file_change", "mcp_tool"];

function cloudTurnDiff(patch: string): (DiffFile & { patch: string })[] {
	const files: (DiffFile & { patch: string })[] = [];
	let current: (DiffFile & { patch: string }) | undefined;
	let inHunk = false;
	for (const line of patch.split("\n")) {
		if (line.startsWith("diff --git ")) {
			if (current) files.push(current);
			const path = line.match(/^diff --git a\/.+ b\/(.+)$/)?.[1];
			current = path ? { path, status: "modified", additions: 0, deletions: 0, patch: line + "\n" } : undefined;
			inHunk = false;
		} else if (current) {
			current.patch += line + "\n";
			if (line.startsWith("new file mode")) current.status = "added";
			else if (line.startsWith("deleted file mode")) current.status = "deleted";
			else if (line.startsWith("rename from ")) { current.status = "renamed"; current.oldPath = line.slice(12); }
			else if (line.startsWith("rename to ")) { current.status = "renamed"; current.path = line.slice(10); }
			else if (line.startsWith("+++ b/")) current.path = line.slice(6);
			else if (line.startsWith("@@")) inHunk = true;
			else if (inHunk && line.startsWith("+")) current.additions++;
			else if (inHunk && line.startsWith("-")) current.deletions++;
		}
	}
	if (current) files.push(current);
	return files;
}

function projectSetupFailure(payload: EventPayload): SystemEventDetail["setupFailure"] {
	const text = (value: unknown) => (typeof value === "string" && value.trim() !== "" ? value.trim() : undefined);
	const groups = Array.isArray(payload.groups) ? payload.groups.filter((group): group is string => typeof group === "string") : [];
	const failure = {
		command: text(payload.command), user: text(payload.user), groups: groups.length > 0 ? groups : undefined,
		dir: text(payload.dir), logPath: text(payload.logPath),
	};
	return Object.values(failure).some((value) => value !== undefined) ? failure : undefined;
}

function projectSetupActivity(event: CloudCpClientEvent): ConversationActivity {
	const payload = eventPayload(event);
	const status = payload.status === "succeeded" || payload.status === "timed_out" ? payload.status : "failed";
	const step = typeof payload.step === "number" && payload.step > 0 ? payload.step : undefined;
	const exitCode = typeof payload.exitCode === "number" ? payload.exitCode : undefined;
	const durationMs = typeof payload.durationMs === "number" ? payload.durationMs : undefined;
	const output = typeof payload.output === "string" && payload.output.trim() !== "" ? payload.output : undefined;
	const hint = typeof payload.hint === "string" && payload.hint.trim() !== "" ? payload.hint : undefined;
	return {
		kind: "activity", id: `cloud-project-setup-${event.sequence}`, sequence: event.sequence, revision: 1,
		activityKind: "system", status: status === "succeeded" ? "completed" : "failed",
		summary: status === "succeeded" ? "Setup passed" : status === "timed_out" ? "Setup timed out" : "Setup failed",
		detail: {
			event: "workspace.setup", setupStatus: status, setupStep: step, exitCode, durationMs, output,
			setupFailure: projectSetupFailure(payload), setupHint: hint,
		},
		createdAt: event.createdAt,
	};
}

function modelUnavailableActivity(event: CloudCpClientEvent, sessionAgent: string): ConversationActivity | undefined {
	const payload = eventPayload(event);
	const model = typeof payload.model === "string" ? payload.model.trim() : "";
	if (!model) return undefined;
	const harness = typeof payload.harness === "string" && payload.harness ? payload.harness : sessionAgent;
	return {
		kind: "activity", id: `cloud-model-unavailable-${event.sequence}`, sequence: event.sequence, revision: 1,
		activityKind: "system", status: "completed",
		summary: appI18n.t("chat.cloudModelUnavailableNotice", { model, agent: agentLabel(harness) }),
		detail: { event: "session.model_unavailable" },
		createdAt: event.createdAt,
	};
}

function withRunningAgentSwitch(items: ConversationItem[], running: (AgentSwitchTransition & { id: string; createdAt: string }) | undefined): ConversationItem[] {
	if (!running?.targetHarness) return items;
	const switchedAt = Date.parse(running.createdAt);
	const arrived = items.some((item) =>
		item.kind === "activity" && item.detail?.event === "provider_switch" &&
		item.detail.targetHarness === running.targetHarness && Date.parse(item.createdAt) >= switchedAt);
	if (arrived) return items;
	const last = items.reduce((highest, item) => Math.max(highest, item.sequence), 0);
	const marker: ConversationActivity = {
		kind: "activity", id: `cloud-agent-switching-${running.id}`, sequence: last + 0.5, revision: 0,
		activityKind: "system", status: "running",
		summary: appI18n.t("agentSwitch.switching"),
		detail: {
			event: "provider_switch", switching: true,
			sourceHarness: running.sourceHarness, targetHarness: running.targetHarness,
			sourceModel: running.sourceModel, targetModel: running.targetModel, targetEffort: running.targetReasoningEffort,
		},
		createdAt: running.createdAt,
	};
	return [...items, marker];
}

function agentSwitchedActivity(event: CloudCpClientEvent): ConversationActivity | undefined {
	const payload = eventPayload(event) as Record<string, unknown>;
	const text = (key: string) => {
		const value = payload[key];
		return typeof value === "string" && value ? value : undefined;
	};
	const sourceHarness = text("sourceHarness");
	const targetHarness = text("targetHarness");
	if (!sourceHarness || !targetHarness) return undefined;
	return {
		kind: "activity", id: `cloud-agent-switched-${event.sequence}`, sequence: event.sequence, revision: 1,
		activityKind: "system", status: "completed",
		summary: appI18n.t("agentSwitch.switchedFromTo", { from: agentLabel(sourceHarness), to: agentLabel(targetHarness) }),
		detail: {
			event: "provider_switch", sourceHarness, targetHarness,
			sourceModel: text("sourceModel"), targetModel: text("targetModel"), targetEffort: text("targetReasoningEffort"),
		},
		createdAt: event.createdAt,
	};
}

function scriptRunActivity(run: ScriptRun, createdAt: string): ConversationActivity {
	return {
		kind: "activity", id: `cloud-script-run-${run.runId}`, sequence: run.firstSequence, revision: run.lastSequence,
		activityKind: "system", status: run.status === "running" ? "running" : run.status === "failed" ? "failed" : "completed",
		summary: appI18n.t("scriptRun.activitySummary", { phase: scriptRunTitle(run, appI18n.t), status: scriptRunStatusText(run, appI18n.t) }),
		detail: { event: "workspace.script.run", scriptRun: run },
		createdAt,
	};
}

function latestSetupRun(session: WorkspaceSession, events: readonly CloudCpClientEvent[]): ScriptRun | undefined {
	return groupScriptRuns(events, { sessionEnded: cloudSessionScriptsEnded(session.cloud) })
		.filter((run) => run.phase === "setup").at(-1);
}

export function cloudSetupStartupSteps(session: WorkspaceSession, events: readonly CloudCpClientEvent[]): SessionProvisionStep[] | undefined {
	if (events.some((event) => event.type === "chat.turn_started")) return undefined;
	const setup = latestSetupRun(session, events);
	if (setup?.status !== "running") return undefined;
	return [
		{ id: "worktree", status: "done" },
		{ id: "setup", status: "running", startedAt: setup.startedAt },
		{ id: "agent", status: "pending" },
	];
}

function workspaceStartupFailure(session: WorkspaceSession): Extract<CloudStartupProblem, { phase: "failed" }> | undefined {
	const problem = cloudStartupProblem(session);
	return problem?.phase === "failed" && isWorkspaceStartupError(problem.code) ? problem : undefined;
}

const startupStepOrder = ["worktree", "setup", "agent"] as const;

export function cloudStartupChecklist(
	session: WorkspaceSession,
	events: readonly CloudCpClientEvent[],
	failure: Extract<CloudStartupProblem, { phase: "failed" }> | undefined,
	retrying: boolean,
): Pick<WorkspaceSession, "provisionState" | "provisionSteps" | "provisionError"> | undefined {
	if (!failure) {
		const steps = cloudSetupStartupSteps(session, events);
		return steps ? { provisionState: "provisioning", provisionSteps: steps } : undefined;
	}
	const failedStep = failure.code === "project_setup_failed" ? "setup" : failure.code === "agent_start_failed" ? "agent" : "worktree";
	const failedIndex = startupStepOrder.indexOf(failedStep);
	const setup = latestSetupRun(session, events);
	const setupStartedAt = retrying && failedStep === "setup" && setup?.status === "running" ? setup.startedAt : undefined;
	return {
		provisionState: retrying ? "provisioning" : "failed",
		provisionSteps: startupStepOrder.map((id, index): SessionProvisionStep => ({
			id,
			status: index < failedIndex ? "done" : index === failedIndex ? "running" : "pending",
			...(id === "setup" && setupStartedAt ? { startedAt: setupStartedAt } : {}),
		})),
		provisionError: [failure.message, failure.detail && `Output: ${failure.detail}`].filter(Boolean).join(" "),
	};
}

function setupAttempt(item: ConversationItem): { passed: boolean; failed: boolean; order: number } | undefined {
	if (item.kind !== "activity") return undefined;
	if (item.detail?.event === "workspace.setup") {
		const status = item.detail.setupStatus;
		return { passed: status === "succeeded", failed: status !== "succeeded", order: item.sequence };
	}
	const run = item.detail?.event === "workspace.script.run" ? item.detail.scriptRun : undefined;
	if (run?.phase !== "setup") return undefined;
	return { passed: run.status === "succeeded", failed: run.status === "failed" || run.status === "interrupted", order: run.firstSequence };
}

export function withoutFailedSetupAttempts(items: ConversationItem[], startFailed: boolean): ConversationItem[] {
	const lastPassed = Math.max(-1, ...items.map(setupAttempt).filter((attempt) => attempt?.passed).map((attempt) => attempt!.order));
	return items.filter((item) => {
		const attempt = setupAttempt(item);
		return !attempt?.failed || (!startFailed && attempt.order > lastPassed);
	});
}

function promptAuthorName(payload: EventPayload, viewerUserId: string | undefined): string | undefined {
	if (!viewerUserId || typeof payload.authorUserId !== "string" || payload.authorUserId === viewerUserId) return undefined;
	return typeof payload.authorName === "string" && payload.authorName.trim() !== "" ? payload.authorName.trim() : undefined;
}

export function fromSessionChatSettings(shared: CloudCpSessionChatSettings | undefined): CloudTurnSettings {
	return {
		...(shared?.model ? { model: shared.model } : {}),
		...(shared?.reasoningEffort ? { reasoningEffort: shared.reasoningEffort } : {}),
		...(shared?.approvalMode ? { approvalMode: shared.approvalMode } : {}),
		...(shared?.mode === "read-only" ? { executionMode: "plan" as const } : {}),
	};
}

export function toSessionChatSettings(settings: CloudTurnSettings): CloudCpSessionChatSettings {
	return {
		...(settings.model ? { model: settings.model } : {}),
		...(settings.reasoningEffort ? { reasoningEffort: settings.reasoningEffort } : {}),
		...(settings.approvalMode ? { approvalMode: settings.approvalMode } : {}),
		...(settings.executionMode === "plan" ? { mode: "read-only" as const } : {}),
	};
}

const EXCERPT_PAIRED_TEXT_MAX = 16 * 1024;

function excerptContext(text: string): string {
	return text.length <= EXCERPT_PAIRED_TEXT_MAX ? text : `${text.slice(0, EXCERPT_PAIRED_TEXT_MAX)}\n[truncated]`;
}

export function cloudExcerptText(excerpts: readonly ChatDraftExcerptReference[], snapshot: Pick<ConversationSnapshot, "items">): string {
	const seenTurns = new Set<string>();
	return [...excerpts].sort((a, b) => (a.order ?? 0) - (b.order ?? 0)).map((excerpt) => {
		if (excerpt.snapshot) return formatSnapshotExcerpt(excerpt, seenTurns);
		const source = snapshot.items.find((item) => item.kind === "message" && item.id === excerpt.messageId);
		const turnId = source?.kind === "message" ? source.turnId : undefined;
		const turnMessages = turnId ? snapshot.items.filter((item): item is ConversationMessage => item.kind === "message" && item.turnId === turnId) : [];
		const user = turnMessages.filter((item) => item.role === "user").map((item) => item.text).join("\n\n");
		const assistant = turnMessages.filter((item) => item.role === "assistant").map((item) => item.text).join("\n\n");
		const paired = user || assistant ? `\n---\nFull paired turn:\nUser:\n${excerptContext(user)}\nAssistant:\n${excerptContext(assistant)}` : "";
		return `\n\n${EXCERPT_PREAMBLE}\n\nSelected text:\n---\n${excerpt.text.trim()}${paired}\n---`;
	}).join("");
}

export function cloudMessageBody(text: string, display?: { text: string; content: ConversationContentSummary[] }): { text: string; displayText?: string; content?: ConversationContentSummary[] } {
	if (!display?.content.length || (display.text !== "" && !text.includes(display.text))) return { text };
	return { text, ...(display.text ? { displayText: display.text } : {}), content: display.content };
}

export function cloudAnnotationContent(excerpts: readonly ChatDraftExcerptReference[] = [], fileReferences: readonly ChatFileReference[] = []): ConversationContentSummary[] {
	return [
		...excerpts.map((excerpt) => {
			let snapshot = excerpt.snapshot;
			if (snapshot) {
				const { turn: _turn, ...displaySnapshot } = snapshot;
				snapshot = displaySnapshot;
			}
			return {
				type: "excerpt", text: excerpt.text, comment: excerpt.comment, snapshot, annotationId: excerpt.id, annotationOrder: excerpt.order,
				sourceConversationId: excerpt.conversationId, sourceMessageId: excerpt.messageId, sourceRevision: excerpt.revision,
			};
		}),
		...fileReferences.map((reference) => ({ type: "file_reference", fileReference: reference, annotationId: reference.id, annotationOrder: reference.order })),
	].sort((a, b) => (a.annotationOrder ?? 0) - (b.annotationOrder ?? 0));
}

function eventContent(payload: EventPayload): ConversationContentSummary[] | undefined {
	if (!Array.isArray(payload.content)) return undefined;
	const content = payload.content.flatMap((value): ConversationContentSummary[] => {
		if (!value || typeof value !== "object") return [];
		const block = value as Record<string, unknown>;
		const number = (field: unknown) => (typeof field === "number" ? field : undefined);
		const text = (field: unknown) => (typeof field === "string" ? field : undefined);
		const snapshot = isExcerptSnapshot(block.snapshot) ? block.snapshot : undefined;
		if (block.type === "excerpt" && (typeof block.text === "string" || snapshot?.badgeOnly === true)) {
			return [{
				type: "excerpt", text: text(block.text) ?? "", comment: text(block.comment), annotationId: text(block.annotationId), annotationOrder: number(block.annotationOrder),
				sourceConversationId: text(block.sourceConversationId), sourceMessageId: text(block.sourceMessageId), sourceRevision: number(block.sourceRevision), snapshot,
			}];
		}
		const reference = block.fileReference;
		if (block.type === "file_reference" && reference && typeof reference === "object" && typeof (reference as ChatFileReference).path === "string") {
			return [{ type: "file_reference", fileReference: reference as ChatFileReference, annotationId: text(block.annotationId), annotationOrder: number(block.annotationOrder) }];
		}
		return [];
	});
	return content.length > 0 ? content : undefined;
}

export function toSnapshot(session: WorkspaceSession, events: CloudCpClientEvent[], viewerUserId?: string, { partial = false }: { partial?: boolean } = {}): ConversationSnapshot {
	const turns = new Map<string, ConversationTurn>();
	const assistant = new Map<string, ConversationMessage>();
	const approvals = new Map<string, ConversationActivity>();
	const activities = new Map<string, ConversationActivity>();
	const turnPatches = new Map<string, (DiffFile & { patch: string })[]>();
	const steerableTurns = new Set<string>();
	const items: ConversationItem[] = [];
	const humanMessages = new Map<string, ConversationMessage>();
	const promptedTurns = new Set<string>();
	const startedTurns = new Set<string>();
	const withdrawnTurns = new Set<string>();
	let usage: ConversationUsage | undefined;
	const scriptRuns = new Map(groupScriptRuns(events, { sessionEnded: cloudSessionScriptsEnded(session.cloud) }).map((run) => [run.runId, run]));
	const placedRuns = new Set<string>();
	const unclaimedSetupRuns = [...scriptRuns.values()].filter((run) => run.phase === "setup");
	const firstTurnStart = events.find((event) => event.type === "chat.turn_started")?.sequence ?? Infinity;
	const windowStart = events[0]?.sequence ?? 0;
	const windowSpan = (events.at(-1)?.sequence ?? 0) - windowStart + 2;
	const firstPrompt = partial ? undefined : events.find((event) => event.type === "chat.user_message")?.sequence;
	const runSequence = (run: ScriptRun) =>
		run.phase === "setup" && firstPrompt !== undefined && firstPrompt < run.firstSequence && run.firstSequence < firstTurnStart
			? firstPrompt - 0.5 : run.firstSequence;
	for (const event of events) {
		if (event.type === "chat.user_message_edited") {
			const edited = humanMessages.get(eventTurnID(event) ?? "");
			const text = eventPayload(event).text;
			if (edited && typeof text === "string") {
				const attachments = eventAttachments(event);
				edited.text = text;
				delete edited.content;
				if (attachments.length > 0) edited.cloudAttachments = attachments;
				else delete edited.cloudAttachments;
				edited.revision += 1;
			}
			continue;
		}
		const turnID = eventTurnID(event);
		if (turnID && !turns.has(turnID)) {
			const startedEarlier = partial && event.type !== "chat.user_message" && event.type !== "chat.turn_started";
			const promptAbove = partial && event.type !== "chat.user_message";
			turns.set(turnID, {
				groupKey: `turn:${turnID}`,
				...(startedEarlier
					? { id: turnID, state: "running", requestedAt: event.createdAt, startedAt: event.createdAt }
					: { id: turnID, state: "queued", requestedAt: event.createdAt }),
				...(promptAbove ? { anchorSequence: windowStart - 1 + (event.sequence - windowStart + 1) / windowSpan } : {}),
			});
		}
		if (turnID && event.type === "chat.user_message") promptedTurns.add(turnID);
		if (turnID && event.type === "chat.turn_started") {
			const turn = turns.get(turnID)!;
			turn.state = "running";
			turn.startedAt = event.createdAt;
			startedTurns.add(turnID);
		}
		if (turnID && event.type === "chat.turn_capabilities" && eventPayload(event).steering === true) {
			steerableTurns.add(turnID);
		}
		if (turnID && (event.type === "chat.turn_completed" || event.type === "chat.turn_interrupted" || event.type === "chat.turn_aborted")) {
			const turn = turns.get(turnID)!;
			turn.state = event.type === "chat.turn_completed" ? "completed" : event.type === "chat.turn_interrupted" ? "interrupted" : "failed";
			turn.completedAt = event.createdAt;
			const error = eventPayload(event).error;
			turn.errorMessage = typeof error === "string" ? error : undefined;
			const notRun = event.type === "chat.turn_aborted" && eventPayload(event).reason === "author_cannot_write";
			const neverStarted = notRun || (!startedTurns.has(turnID) && (promptedTurns.has(turnID) || eventPayload(event).attempt === 0));
			if (event.type === "chat.turn_interrupted" && (eventPayload(event).reason === "deleted" || eventPayload(event).reason === "steered")) {
				turn.state = "cancelled";
				delete turn.startedAt;
				withdrawnTurns.add(turnID);
			} else if (neverStarted) {
				delete turn.startedAt;
				if (event.type === "chat.turn_aborted") {
					turn.notRun = true;
					if (turn.errorMessage) {
						const reason = turn.errorMessage.replace(/^Not run:\s*/i, "");
						turn.errorMessage = reason.charAt(0).toUpperCase() + reason.slice(1);
					}
				}
			}
		}
		if (event.type === "chat.approval_requested") {
			const payload = eventPayload(event);
			const requestId = typeof payload.requestId === "string" ? payload.requestId : undefined;
			const summary = typeof payload.summary === "string" ? payload.summary : "Permission required";
			const decisions = Array.isArray(payload.decisions) ? payload.decisions.filter((value): value is { id: string; label: string; kind?: "allow_once" | "allow_always" | "reject_once" | "reject_always" } =>
				Boolean(value && typeof value === "object" && typeof value.id === "string" && typeof value.label === "string")) : [];
			if (requestId) {
				const activity: ConversationActivity = {
					kind: "activity", id: `cloud-approval-${requestId}`, turnId: turnID, sequence: event.sequence,
					revision: 1, activityKind: "approval", status: "pending", summary, requestId,
					decisions, detail: {
						method: "ACP session/request_permission",
						toolKind: typeof payload.toolKind === "string" ? payload.toolKind : undefined,
						subjectKind: APPROVAL_SUBJECT_KINDS.find((kind) => kind === payload.subjectKind),
					},
					createdAt: event.createdAt,
				};
				approvals.set(requestId, activity);
				items.push(activity);
			}
			continue;
		}
		if (event.type === "chat.approval_decided") {
			const payload = eventPayload(event);
			const requestId = typeof payload.requestId === "string" ? payload.requestId : "";
			const activity = approvals.get(requestId);
			if (activity) {
				activity.status = "completed";
				activity.revision += 1;
				activity.detail = { ...activity.detail, decision: typeof payload.decision === "string" ? payload.decision : undefined };
			}
			continue;
		}
		if (turnID && event.type === "chat.activity") {
			const raw = eventPayload(event).activity;
			if (!raw || typeof raw !== "object") continue;
			const value = raw as Record<string, unknown>;
			if (typeof value.id !== "string" || typeof value.kind !== "string") continue;
			const detail = value.detail && typeof value.detail === "object" ? value.detail as Record<string, unknown> : {};
			if (value.kind === "turn_plan") {
				const plan = cloudPlan(detail);
				if (plan) turns.get(turnID)!.plan = plan;
				continue;
			}
			if (value.kind === "usage") {
				usage = cloudUsage(detail) ?? usage;
				continue;
			}
			if (value.kind === "turn_diff") {
				if (typeof detail.diff === "string") {
					const files = cloudTurnDiff(detail.diff);
					turns.get(turnID)!.diff = { files, truncated: detail.truncated === true };
					turnPatches.set(turnID, files);
				}
				continue;
			}
			if (value.kind === "system") {
				const key = `${turnID}:${value.id}`;
				if (detail.event !== "approval_mode.substituted" || typeof value.summary !== "string" || activities.has(key)) continue;
				const activity: ConversationActivity = {
					kind: "activity", id: `cloud-activity-${key}`, turnId: turnID, sequence: event.sequence, revision: 1,
					activityKind: "system", status: "completed", summary: value.summary,
					detail: { event: "approval_mode.substituted" }, createdAt: event.createdAt,
				};
				activities.set(key, activity);
				items.push(activity);
				continue;
			}
			if (!["command", "file_change", "reasoning", "plan", "mcp_tool"].includes(value.kind)) continue;
			const key = `${turnID}:${value.id}`;
			const previous = activities.get(key);
			if (previous) {
				previous.status = value.status === "completed" || value.status === "failed" ? value.status : "running";
				previous.summary = typeof value.summary === "string" ? value.summary : previous.summary;
				previous.detail = { ...previous.detail, ...detail };
				if (typeof detail.textDelta === "string") previous.detail.text = (previous.detail.text ?? "") + detail.textDelta;
				if (typeof detail.outputDelta === "string") {
					previous.detail.output = (previous.detail.output ?? "") + detail.outputDelta;
					previous.detail.outputSource = "stream";
					previous.detail.outputMayBePartial = true;
				}
				previous.revision++;
			} else {
				const activity: ConversationActivity = {
					kind: "activity", id: `cloud-activity-${key}`, turnId: turnID, sequence: event.sequence,
					providerItemId: value.id,
					revision: 1, activityKind: value.kind as ConversationActivity["activityKind"],
					status: value.status === "completed" || value.status === "failed" ? value.status : "running",
					summary: typeof value.summary === "string" ? value.summary : value.kind,
					detail: { ...detail, text: typeof detail.textDelta === "string" ? detail.textDelta : undefined,
						output: typeof detail.outputDelta === "string" ? detail.outputDelta : typeof detail.output === "string" ? detail.output : undefined },
					createdAt: event.createdAt,
				};
				activities.set(key, activity);
				items.push(activity);
			}
			continue;
		}
		if (event.type === SCRIPT_STEP_EVENT) {
			const runId = parseScriptStepPayload(event.payload)?.runId;
			const run = runId ? scriptRuns.get(runId) : undefined;
			if (run && !placedRuns.has(run.runId)) {
				placedRuns.add(run.runId);
				const activity: ConversationActivity = { ...scriptRunActivity(run, event.createdAt), sequence: runSequence(run) };
				const before = activity.sequence < run.firstSequence ? items.findIndex((item) => item.sequence > activity.sequence) : -1;
				if (before >= 0) items.splice(before, 0, activity);
				else items.push(activity);
			}
			continue;
		}
		if (event.type === "session.model_unavailable") {
			const activity = modelUnavailableActivity(event, session.provider);
			if (activity) items.push(activity);
			continue;
		}
		if (event.type === "chat.agent_switched") {
			const activity = agentSwitchedActivity(event);
			if (!activity) continue;
			const held = items.findIndex((item) => item.kind === "message" && item.role === "user" &&
				item.turnId !== undefined && turns.get(item.turnId)?.state === "queued");
			if (held >= 0) {
				activity.sequence = items[held].sequence - 0.5;
				items.splice(held, 0, activity);
			} else {
				items.push(activity);
			}
			continue;
		}
		if (event.type === "workspace.setup") {
			let covering = -1;
			for (let index = 0; index < unclaimedSetupRuns.length; index++) {
				if (unclaimedSetupRuns[index].firstSequence < event.sequence) covering = index;
			}
			if (covering >= 0) {
				unclaimedSetupRuns.splice(covering, 1);
				continue;
			}
			items.push(projectSetupActivity(event));
			continue;
		}
		const text = eventText(event);
		const attachments = event.type === "chat.user_message" ? eventAttachments(event) : [];
		if (!text && attachments.length === 0) continue;
		if (event.type === "chat.user_message") {
			const payload = eventPayload(event);
			const automation = payload.origin === "automation";
			const content = automation ? undefined : eventContent(payload);
			const senderSessionId = automation && typeof payload.senderSessionId === "string" ? payload.senderSessionId : undefined;
			const senderProjectId = automation && typeof payload.senderProjectId === "string" ? payload.senderProjectId : undefined;
			const senderDisplayName = automation && typeof payload.senderDisplayName === "string" ? payload.senderDisplayName : undefined;
			const message: ConversationMessage = {
				kind: "message", id: `cloud-event-${event.sequence}`, sequence: event.sequence, revision: 1,
				turnId: turnID, role: "user", origin: automation ? "automation" : "human",
				authorName: automation ? undefined : promptAuthorName(payload, viewerUserId),
				text: automation && typeof payload.displayText === "string" ? payload.displayText
					: content ? (typeof payload.displayText === "string" ? payload.displayText : "") : text ?? "",
				...(content ? { content } : {}),
				senderLabel: automation && typeof payload.senderLabel === "string" ? payload.senderLabel : undefined,
				senderSessionId, senderProjectId, senderDisplayName,
				...(attachments.length > 0 ? { cloudAttachments: attachments } : {}),
				streaming: false, delivery: "accepted", createdAt: event.createdAt,
			};
			items.push(message);
			if (!automation && turnID) humanMessages.set(turnID, message);
			continue;
		}
		if (!text) continue;
		if (event.type === "chat.turn_steer_requested" || event.type === "chat.turn_steer_failed") continue;
		if (event.type === "chat.turn_steered") {
			const payload = eventPayload(event);
			const clientMessageID = payload.clientMessageId;
			items.push({
				kind: "activity", id: `cloud-steer-${event.sequence}`, turnId: turnID, sequence: event.sequence,
				revision: 1, activityKind: "system", status: "completed", summary: `Steered: ${text}`,
				detail: {
					event: "steer", text, ...steerOrigin(payload), clientMessageId: typeof clientMessageID === "string" ? clientMessageID : undefined,
					...(payload.deleteRequested === true ? { deliveredBeforeDelete: true } : {}),
				},
				createdAt: event.createdAt,
			});
			continue;
		}
		if (event.type !== "chat.assistant_delta") continue;
		if (session.provider === "codex" && text.trim() === "Reading additional input from stdin...") continue;
		const itemID = eventPayload(event).itemId;
		const assistantKey = `${turnID ?? `event-${event.sequence}`}:${typeof itemID === "string" ? itemID : "reply"}`;
		const previous = assistant.get(assistantKey);
		if (previous) {
			previous.text += text;
			previous.revision += 1;
			continue;
		}
		const message: ConversationMessage = {
			kind: "message", id: `cloud-assistant-${assistantKey}`, turnId: turnID, sequence: event.sequence,
			revision: 1, role: "assistant", origin: "provider", text, streaming: true, createdAt: event.createdAt,
		};
		assistant.set(assistantKey, message);
		items.push(message);
	}
	for (const activity of activities.values()) {
		if (activity.activityKind !== "file_change" || !activity.turnId || !Array.isArray(activity.detail?.files)) continue;
		const files = activity.detail.files;
		if (!files.every((file): file is FileChangeFile => typeof file === "object" && file !== null && "path" in file)) continue;
		const patches = turnPatches.get(activity.turnId) ?? [];
		activity.detail.files = files.map((file) => {
			const match = patches.find((patch) => patch.path === file.path);
			return match ? { ...file, patch: file.patch ?? match.patch } : file;
		});
	}
	for (const activity of activities.values()) {
		const turnState = activity.turnId ? turns.get(activity.turnId)?.state : undefined;
		if (activity.status === "running" && activity.turnId && turnState !== "running") {
			activity.status = turnState === "interrupted" ? "cancelled" : "recovered";
			activity.revision++;
		}
	}
	for (const message of assistant.values()) {
		if (!message.turnId || turns.get(message.turnId)?.state !== "running") message.streaming = false;
	}
	for (const approval of approvals.values()) {
		if (approval.status === "pending" && approval.turnId && ["completed", "interrupted", "failed"].includes(turns.get(approval.turnId)?.state ?? "")) {
			approval.status = "cancelled";
			approval.revision += 1;
		}
	}
	const shownItems = withdrawnTurns.size > 0 ? items.filter((item) => !item.turnId || !withdrawnTurns.has(item.turnId)) : items;
	const orderedTurns = [...turns.values()];
	if (!orderedTurns.some((turn) => turn.state === "running") && !cloudSetupStartupSteps(session, events) && !workspaceStartupFailure(session)) {
		const next = orderedTurns.find((turn) => turn.state === "queued");
		if (next) {
			next.state = "running";
			const setupEnd = [...scriptRuns.values()].filter((run) => run.phase === "setup" && run.status !== "running").at(-1)?.lastEventAt;
			if (setupEnd && Date.parse(setupEnd) > Date.parse(next.requestedAt)) next.startedAt = setupEnd;
		}
	}
	const hasRunningTurn = orderedTurns.some((turn) => turn.state === "running");
	const activeTurn = orderedTurns.find((turn) => turn.state === "running");
	return {
		conversationId: `cloud:${session.id}`, sessionId: session.id, harness: session.provider, mode: "chat",
		controller: { state: hasRunningTurn ? "busy" : "ready" }, turns: orderedTurns, items: shownItems,
		latestSequence: events.at(-1)?.sequence ?? 0, oldestSequence: events[0]?.sequence ?? 1,
		hasMoreBefore: partial, settings: {}, capabilities: activeTurn && steerableTurns.has(activeTurn.id) ? ["steer"] : [],
		...(usage ? { usage } : {}),
		supportsFileReferences: true,
	};
}

const SANDBOX_CHECKOUT_ROOT = "/workspace/repository/";

export async function cloudWorkspaceFilePath(
	path: string,
	search: (query: string) => Promise<string[]>,
): Promise<string | undefined> {
	if (!path.startsWith("/")) return path;
	if (path.startsWith(SANDBOX_CHECKOUT_ROOT)) return path.slice(SANDBOX_CHECKOUT_ROOT.length) || undefined;
	const name = path.split("/").filter(Boolean).at(-1);
	if (!name) return undefined;
	const candidates = (await search(name).catch(() => [])).filter((candidate) => path.endsWith(`/${candidate}`));
	return candidates.sort((a, b) => b.length - a.length)[0];
}

function isWorkerUnavailable(error: unknown): boolean {
	return error instanceof CloudCpError && error.code === "WORKER_UNAVAILABLE";
}

function isTransportTimeout(error: unknown): boolean {
	return error instanceof CloudCpError && (error.code === "TRANSPORT_TIMEOUT" || error.code === "WORKER_TIMEOUT");
}

function isWorkerOperationFailed(error: unknown): boolean {
	return error instanceof CloudCpError && error.code === "WORKER_OPERATION_FAILED";
}
const MODELS_FAILED_RETRIES = 6;
function isAgentSettingUp(error: unknown): boolean {
	return error instanceof CloudCpError && (error.code === "CHAT_AGENT_INSTALLING" || error.code === "CHAT_ADAPTER_INSTALLING");
}
const AGENT_SETUP_RETRIES = 36;
const AGENT_SETUP_RETRY_DELAY = 5_000;
function modelsRetryDelay(failures: number, error: unknown): number {
	if (isAgentSettingUp(error)) return AGENT_SETUP_RETRY_DELAY;
	return isWorkerOperationFailed(error) ? Math.min(2_000 * 2 ** failures, 15_000) : Math.min(1_000 * 2 ** failures, 30_000);
}

export function CloudSessionChatSurface({
	session,
	headerActions,
	sessionTabAction,
	sessionTabActionPinned = false,
	onOpenFiles,
	onOpenFile,
	onOpenArtifact,
	controllerTransitioning,
	newWorkDisabled,
	onConversationWorkChange,
	reviewerTerminal,
	onOpenReviewerTerminal,
	reviewerTarget,
	onSelectChat,
	shellTerminals,
	shellTarget,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	daemonReady,
	theme,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	tabStripAction,
	workspaceTabs,
	workspaceActiveTabKey,
	workspaceFileActive,
	reviewerChat,
	onOpenReviewerChat,
	reviewerChatSelected,
	onOpenLinkInBrowser,
}: {
	session: WorkspaceSession;
	onOpenLinkInBrowser?: (uri: string) => Promise<void>;
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	reviewerChatSelected?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void; onClose?: () => void }>;
	workspaceActiveTabKey?: string;
	workspaceFileActive?: boolean;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionPinned?: boolean;
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerTarget?: Extract<TerminalTarget, { kind: "reviewer" }>;
	onSelectChat?: () => void;
	shellTerminals?: ShellTerminal[];
	shellTarget?: Extract<TerminalTarget, { kind: "shell" }>;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	daemonReady?: boolean;
	theme?: "light" | "dark";
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string) => void;
	onOpenArtifact?: (target: { path: string }) => void;
	controllerTransitioning?: boolean;
	newWorkDisabled?: boolean;
	onConversationWorkChange?: (state: {
		controllerBusy: boolean;
		hasRunningTurn: boolean;
		queuedTurnCount: number;
	}) => void;
}) {
	const cloud = session.cloud;
	const openSessionLink = useSessionLinkNavigation(undefined, "cloud");
	const openLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser);
	const { client, ready, userId, baseUrl } = useCloudCp();
	const live = useCloudSessionLive(cloud?.orgId, session.id, Boolean(cloud && ready && session.isTerminated !== true));
	const me = live?.you || userId;
	const settingsLocked = Boolean(live && !live.canConfigure);
	const queryClient = useQueryClient();
	const workspaceFailure = workspaceStartupFailure(session);
	const [retriedFailureAt, setRetriedFailureAt] = useState<string>();
	const rerunning = retriedFailureAt !== undefined && retriedFailureAt === workspaceFailure?.at;
	const retryAbort = useRef<AbortController | undefined>(undefined);
	useEffect(() => () => retryAbort.current?.abort(), []);
	const retryStart = useMutation({
		mutationFn: () => {
			retryAbort.current?.abort();
			const abort = new AbortController();
			retryAbort.current = abort;
			return requestWorkspaceRetry(client, cloud?.orgId ?? "", session.id, abort.signal);
		},
		onSuccess: () => {
			setRetriedFailureAt(workspaceFailure?.at);
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
			void queryClient.invalidateQueries({ queryKey: ["cloud-session"] });
		},
	});
	const settingsKey = `cloud-chat-settings:${cloud?.orgId ?? ""}:${session.id}:${session.provider}`;
	const nativeKey = `cloud-chat-native:${cloud?.orgId ?? ""}:${session.id}:${session.provider}`;
	const projectKey = `cloud-chat-approval:${cloud?.orgId ?? ""}:${session.workspaceId}:${session.provider}`;
	const sessionApprovalModes = useMemo(() => allowedApprovalModes(session.provider, cloud?.permissionMode), [session.provider, cloud?.permissionMode]);
	const shownDefaultApprovalMode = defaultApprovalMode(session.provider, sessionApprovalModes, cloud?.permissionMode);
	const readSettings = () => {
		const saved = readCloudTurnSettings(settingsKey);
		const selectedMode = saved.approvalMode ?? readRememberedApprovalMode(projectKey);
		return { ...saved, approvalMode: selectedMode && sessionApprovalModes.includes(selectedMode) ? selectedMode : undefined };
	};
	const [selected, setSelected] = useState<{ key: string; settings: CloudTurnSettings }>(() => ({ key: settingsKey, settings: readSettings() }));
	const ownSettings = selected.key === settingsKey ? selected.settings : readSettings();
	const settings = settingsLocked && live ? fromSessionChatSettings(live.chatSettings) : ownSettings;
	const [rememberedMode, setRememberedMode] = useState<{ key: string; mode?: ApprovalMode }>(() => ({ key: projectKey, mode: readRememberedApprovalMode(projectKey) }));
	const settingsRef = useRef({ key: settingsKey, settings });
	if (settingsRef.current.key !== settingsKey) settingsRef.current = { key: settingsKey, settings };
	const manualSelectionRef = useRef({ key: settingsKey, version: 0 });
	if (manualSelectionRef.current.key !== settingsKey) manualSelectionRef.current = { key: settingsKey, version: 0 };
	const catalogSyncRef = useRef({ key: settingsKey, version: 0 });
	if (catalogSyncRef.current.key !== settingsKey) catalogSyncRef.current = { key: settingsKey, version: 0 };
	const shareSettings = useMutation({
		mutationFn: async (next: CloudTurnSettings) => {
			if (!cloud) return;
			await client.setSessionChatSettings(cloud.orgId, session.id, toSessionChatSettings(next));
		},
	});
	const updateSettings = (next: CloudTurnSettings) => {
		if (settingsLocked) return;
		manualSelectionRef.current.version++;
		settingsRef.current = { key: settingsKey, settings: next };
		setSelected({ key: settingsKey, settings: next });
		try {
			writeCloudTurnSettings(settingsKey, next);
		} catch {
		}
		if (live?.canConfigure) shareSettings.mutate(next);
	};
	const workerConnected = session.runtimeConnected === true;
	const liveRef = useRef({ connected: workerConnected, terminated: session.isTerminated === true });
	liveRef.current = { connected: workerConnected, terminated: session.isTerminated === true };
	const seededSettingsRef = useRef("");
	const liveCanConfigure = live?.canConfigure === true;
	const liveSettingsEmpty = live ? Object.keys(live.chatSettings ?? {}).length === 0 : false;
	const shareSettingsNow = shareSettings.mutate;
	useEffect(() => {
		if (!liveCanConfigure || !liveSettingsEmpty || seededSettingsRef.current === settingsKey) return;
		seededSettingsRef.current = settingsKey;
		if (Object.keys(toSessionChatSettings(settingsRef.current.settings)).length > 0) shareSettingsNow(settingsRef.current.settings);
	}, [liveCanConfigure, liveSettingsEmpty, settingsKey, shareSettingsNow]);
	const modelsQuery = useQuery({
		queryKey: ["cloud-chat-models", cloud?.orgId ?? "", session.id, session.provider, session.terminalGeneration ?? ""],
		enabled: Boolean(cloud && ready && workerConnected && session.isTerminated !== true &&
			(session.provider === "codex" || session.provider === "claude-code")),
		staleTime: 0,
		refetchOnMount: "always",
		retry: (failures, error) => liveRef.current.connected && !liveRef.current.terminated &&
			(isWorkerUnavailable(error) || (isTransportTimeout(error) && failures < 3) ||
				(isWorkerOperationFailed(error) && failures < MODELS_FAILED_RETRIES) ||
				(isAgentSettingUp(error) && failures < AGENT_SETUP_RETRIES)),
		retryDelay: modelsRetryDelay,
		queryFn: async ({ signal }) => {
			const selectionVersionAtFetch = catalogSyncRef.current.version;
			const catalog = await client.listChatModels(cloud!.orgId, session.id, { signal });
			if (catalog.accountListed && userId) {
				void invalidateCloudModelCatalogs(queryClient, { baseUrl, userId, orgId: cloud!.orgId, agent: session.provider });
			}
			return { ...catalog, selectionVersionAtFetch };
		},
	});
	const streaming = useCloudChatEventStream(cloud?.orgId, session.id, Boolean(cloud && ready));
	const eventsQuery = useQuery({
		queryKey: ["cloud-chat-events", cloud?.orgId ?? "", session.id],
		enabled: Boolean(cloud && ready),
		gcTime: CHAT_EVENTS_CACHE_MS,
		refetchInterval: streaming ? CHAT_EVENTS_SAFETY_POLL_MS : CHAT_EVENTS_FALLBACK_POLL_MS,
		queryFn: async () => cloud ? loadCloudChatHistory(queryClient, client, cloud.orgId, session.id) : [] as CloudCpClientEvent[],
	});
	const historyKey = cloudChatHistoryKey(cloud?.orgId ?? "", session.id);
	const history = useQuery<CloudChatHistory | null>({
		queryKey: historyKey,
		queryFn: () => queryClient.getQueryData<CloudChatHistory | null>(historyKey) ?? null,
		enabled: false,
		staleTime: Infinity,
		gcTime: Infinity,
	}).data ?? undefined;
	const loadOlder = useCallback((recent?: boolean) => {
		if (cloud) void loadOlderCloudChatEvents(queryClient, client, cloud.orgId, session.id, { recent }).catch(() => {});
	}, [client, cloud, queryClient, session.id]);
	const recentPending = Boolean(history?.recentPending && eventsQuery.data);
	useEffect(() => {
		if (recentPending) loadOlder(true);
	}, [loadOlder, recentPending]);
	const onLoadOlder = useCallback(() => loadOlder(), [loadOlder]);
	const catalogAgent = (CLOUD_AGENT_PROVIDERS as readonly string[]).includes(session.provider);
	const catalogQuery = useQuery({
		queryKey: ["cloud-agent-models", baseUrl, userId, cloud?.orgId ?? "", session.provider],
		enabled: Boolean(cloud && ready && catalogAgent && session.isTerminated !== true && !modelsQuery.data),
		staleTime: 5 * 60_000,
		refetchOnWindowFocus: false,
		refetchOnReconnect: false,
		retry: false,
		queryFn: ({ signal }) => client.listUserAgentModels(cloud!.orgId, session.provider as CloudCpAgentProvider, { signal }),
	});
	const lastChosen = useMemo(() => lastChosenChatSettings(eventsQuery.data), [eventsQuery.data]);
	const catalogDefaultUnknown = !modelsQuery.data &&
		(!eventsQuery.data || (!lastChosen && catalogQuery.isPending && catalogQuery.fetchStatus !== "idle"));
	const listedModels = useMemo(() => (modelsQuery.data?.models ?? catalogQuery.data?.models ?? []).map((model) => ({
		...model,
		displayName: cloudModelLabel(session.provider, model),
		...(catalogDefaultUnknown && model.default ? { default: false } : {}),
	})), [catalogDefaultUnknown, catalogQuery.data, modelsQuery.data, session.provider]);
	const pendingSettings: Pick<CloudTurnSettings, "model" | "reasoningEffort"> | undefined =
		modelsQuery.data || !eventsQuery.data ? undefined
			: lastChosen ?? (catalogQuery.data && { model: catalogQuery.data.models.find((model) => model.default)?.id });
	const pendingModel = pendingSettings?.model;
	const pendingEffort = pendingSettings?.reasoningEffort;
	const shownSettings = useMemo<CloudTurnSettings>(() => settings.model ? settings : {
		...settings,
		model: pendingModel,
		reasoningEffort: settings.reasoningEffort ?? pendingEffort,
	}, [pendingEffort, pendingModel, settings]);
	const selectedModel = listedModels.find((model) => model.id === shownSettings.model) ?? listedModels.find((model) => model.default);
	const selectedModelApprovalModes = selectedModel?.approvalModes;
	const approvalModes = useMemo(
		() => modelApprovalModes(sessionApprovalModes, selectedModelApprovalModes ? { approvalModes: selectedModelApprovalModes } : undefined),
		[sessionApprovalModes, selectedModelApprovalModes],
	);
	const [wakeError, setWakeError] = useState<CloudCpError>();
	useEffect(() => {
		setWakeError(undefined);
		if (!cloud || !ready || liveRef.current.connected || liveRef.current.terminated) return;
		let active = true;
		resumeCloudSession(client, cloud.orgId, session.id).catch((error: unknown) => {
			if (active && error instanceof CloudCpError && error.code === "SESSION_TERMINATED") setWakeError(error);
		});
		return () => { active = false; };
	}, [client, cloud?.orgId, ready, session.id]);
	useEffect(() => {
		const native = modelsQuery.data;
		if (!modelsQuery.isFetchedAfterMount || !native) return;
		const chosenSince = native.selectionVersionAtFetch !== manualSelectionRef.current.version;
		catalogSyncRef.current = { key: settingsKey, version: manualSelectionRef.current.version };
		const current = settingsRef.current.key === settingsKey ? settingsRef.current.settings : readSettings();
		const catalog = native.models ?? [];
		const runnable = !current.model || catalog.length === 0 || catalog.some((model) => model.id === current.model);
		const seen = readSeenNativeSettings(nativeKey);
		writeSeenNativeSettings(nativeKey, native);
		const nativeChanged = seen !== undefined &&
			((seen.model ?? "") !== (native.model ?? "") || (seen.reasoningEffort ?? "") !== (native.reasoningEffort ?? ""));
		const picked = Boolean(current.model || current.reasoningEffort);
		if (runnable && (chosenSince || (picked && !nativeChanged) || (!native.model && !native.reasoningEffort))) return;
		const next: CloudTurnSettings = {
			...current,
			...(runnable ? {} : { model: undefined, reasoningEffort: undefined }),
			...(native.model ? { model: native.model } : {}),
			...(native.model || native.reasoningEffort ? { reasoningEffort: native.reasoningEffort || undefined } : {}),
		};
		settingsRef.current = { key: settingsKey, settings: next };
		setSelected({ key: settingsKey, settings: next });
		if (live?.canConfigure) shareSettings.mutate(next);
		try { writeCloudTurnSettings(settingsKey, { ...next, approvalMode: readCloudTurnSettings(settingsKey).approvalMode }); } catch { }
	}, [modelsQuery.dataUpdatedAt, settingsKey]);
	const seenEventRef = useRef<{ sessionId: string; sequence: number } | undefined>(undefined);
	useEffect(() => {
		const events = eventsQuery.data;
		if (!events?.length) return;
		const seen = seenEventRef.current?.sessionId === session.id ? seenEventRef.current.sequence : undefined;
		seenEventRef.current = { sessionId: session.id, sequence: events.at(-1)!.sequence };
		if (seen === undefined || session.runtimeConnected === true) return;
		if (events.some((event) => event.sequence > seen && (event.type === "worker.connected" || event.type === "worker.ready"))) {
			void refreshCloudSessionLifecycle(queryClient);
		}
	}, [eventsQuery.data, queryClient, session.id, session.runtimeConnected]);
	const invalidate = () =>
		queryClient.invalidateQueries({ queryKey: ["cloud-chat-events", cloud?.orgId ?? "", session.id] });
	const myRole = live?.viewers.find((viewer) => viewer.userId === me)?.role;
	const readOnly = myRole === "viewer" || (myRole === undefined && cloud?.shared === true);
	const lease = useComposerLease({ client, orgId: cloud?.orgId, sessionId: session.id, userId: me, live, canWrite: !readOnly });
	const leaseRef = useRef(lease);
	leaseRef.current = lease;
	const attachmentSource = useMemo(
		() => cloud && ready && baseUrl ? { baseUrl, orgId: cloud.orgId, sessionId: session.id } : undefined,
		[baseUrl, cloud, ready, session.id],
	);
	const offering = useCloudOffering(Boolean(cloud), cloud?.orgId);
	const attachmentMaxBytes = offering.maxAttachmentBytes;
	const cloudAttachments = useMemo(() => attachmentMaxBytes === undefined
		? undefined : { maxBytes: Math.min(attachmentMaxBytes, MAX_CLOUD_CP_BINARY_BYTES) }, [attachmentMaxBytes]);
	const stageAttachments = useMemo(() => {
		if (!attachmentSource || !cloudAttachments || readOnly || session.isTerminated === true) return undefined;
		return (attachments: { mimeType: string; data: string; name?: string }[], signal?: AbortSignal) => Promise.all(attachments.map(async (attachment) =>
			cloudAttachmentReference(await uploadCloudAttachment(attachmentSource.baseUrl, attachmentSource.orgId, attachmentSource.sessionId, {
				name: attachment.name || "attachment", data: attachment.data,
			}, { signal }))));
	}, [attachmentSource, cloudAttachments, readOnly, session.isTerminated]);
	const send = useMutation({
		mutationFn: async ({ text, attachmentIds, clientMessageId, display, afterSwitch }: { text: string; attachmentIds?: string[]; clientMessageId?: string; display?: { text: string; content: ConversationContentSummary[] }; afterSwitch?: boolean }) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			const selectedSettings: CloudTurnSettings = !afterSwitch && settingsRef.current.key === settingsKey ? settingsRef.current.settings : {};
			const picked = selectedSettings.approvalMode && sessionApprovalModes.includes(selectedSettings.approvalMode)
				? selectedSettings.approvalMode : undefined;
			const turnModel = listedModels.find((model) => model.id === (selectedSettings.model ?? pendingModel)) ?? listedModels.find((model) => model.default);
			const approvalMode = picked && (effectiveApprovalMode(session.provider, picked, modelApprovalModes(sessionApprovalModes, turnModel)) ?? picked);
			const mode = selectedSettings.executionMode === "plan" && modelsQuery.data?.modes?.includes("plan")
				? "read-only" : cloud.permissionMode;
			const composerEpoch = leaseRef.current.epoch;
			const request = {
				text,
				...(attachmentIds?.length ? { attachmentIds } : {}),
				...(selectedSettings.model ? { model: selectedSettings.model } : {}),
				...(selectedSettings.reasoningEffort ? { reasoningEffort: selectedSettings.reasoningEffort } : {}),
				...(mode ? { mode } : {}),
				...(approvalMode && mode !== "read-only" ? { approvalMode } : {}),
				...(composerEpoch !== undefined ? { composerEpoch } : {}),
			};
			const message = cloudMessageBody(text, display);
			if (!message.content) return client.sendSessionMessage(cloud.orgId, session.id, request, { idempotencyKey: clientMessageId });
			try {
				return await client.sendSessionMessage(cloud.orgId, session.id, { ...request, ...message }, { idempotencyKey: clientMessageId });
			} catch (error) {
				if (!(error instanceof CloudCpError) || error.status !== 400) throw error;
				return client.sendSessionMessage(cloud.orgId, session.id, request, { idempotencyKey: clientMessageId });
			}
		},
		onSuccess: () => {
			leaseRef.current.sent();
			void invalidate();
		},
	});
	const editQueued = useMutation({
		mutationFn: async ({ turnId, text }: { turnId: string; text: string }) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			const message = cloudMessageAttachments(text, undefined, []);
			try {
				return await client.editQueuedMessage(cloud.orgId, session.id, turnId, {
					text: message.text,
					...(message.attachmentIds.length ? { attachmentIds: message.attachmentIds } : {}),
				});
			} catch (error) {
				if (error instanceof CloudCpError && (error.code === "TURN_NOT_QUEUED" || error.code === "SESSION_TERMINATED")) {
					const reason = error.code === "SESSION_TERMINATED"
						? "This session has ended, so this message can no longer be edited." : error.message;
					throw Object.assign(new Error(reason), { code: "CHAT_TURN_NOT_QUEUED" });
				}
				if (error instanceof CloudCpError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429) {
					throw Object.assign(new Error(error.message), { code: "CHAT_QUEUED_EDIT_REFUSED" });
				}
				throw error;
			}
		},
		onSuccess: () => void invalidate(),
	});
	const queuedEditOffered = offering.features.includes(QUEUED_EDIT_FEATURE) && !readOnly && session.isTerminated !== true;
	const ownTurns = useMemo(() => ownMessageTurns(eventsQuery.data, me), [eventsQuery.data, me]);
	const canEditQueuedMessage = useCallback(
		(message: ConversationMessage) => Boolean(message.turnId && ownTurns.has(message.turnId) && !message.content?.length),
		[ownTurns],
	);
	const queuedDeleteOffered = offering.features.includes(QUEUED_DELETE_FEATURE);
	const cancelQueued = useMutation({
		mutationFn: async (turnId: string) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			const heldThrough = eventsQuery.data?.at(-1)?.sequence ?? 0;
			let pending = false;
			try {
				if (queuedDeleteOffered) pending = (await client.deleteQueuedMessage(cloud.orgId, session.id, turnId)).pending === true;
				else await client.cancelTurn(cloud.orgId, session.id, turnId);
			} catch (error) {
				const refusal = queuedDeleteRefusal(error);
				if (refusal) throw refusal;
				return;
			}
			if (!pending) return;
			let after = heldThrough;
			const deadline = Date.now() + QUEUED_STEER_STALE_MS;
			while (Date.now() < deadline) {
				let page: Awaited<ReturnType<typeof client.listChatEvents>>;
				try {
					page = await client.listChatEvents(cloud.orgId, session.id, { after, limit: 100 });
				} catch {
					await new Promise((resolve) => setTimeout(resolve, 1000));
					continue;
				}
				for (const event of page.events) {
					const payload = eventPayload(event);
					if (event.type === "chat.turn_interrupted" && payload.turnId === turnId && payload.reason === "deleted") return;
					if (event.type === "chat.turn_steered" && payload.queuedTurnId === turnId) throw new AlreadyDeliveredError();
				}
				after = page.nextAfter > after ? page.nextAfter : after;
				if (!page.hasMore) await new Promise((resolve) => setTimeout(resolve, 500));
			}
		},
		onSettled: () => void invalidate(),
	});
	const directsEveryTurn = Boolean(cloud && (
		(me && cloud.createdByUserId === me) || lease.holding ||
		(!cloud.shared && offering.org?.id === cloud.orgId && (offering.org.role === "owner" || offering.org.role === "admin"))
	));
	const canCancelQueuedMessage = useCallback(
		(message: ConversationMessage) => Boolean(message.turnId && (directsEveryTurn || ownTurns.has(message.turnId))),
		[directsEveryTurn, ownTurns],
	);
	const wantedApprovalMode = settings.approvalMode && sessionApprovalModes.includes(settings.approvalMode)
		? settings.approvalMode : shownDefaultApprovalMode;
	const shownApprovalMode = wantedApprovalMode && (effectiveApprovalMode(session.provider, wantedApprovalMode, approvalModes) ?? wantedApprovalMode);
	const selectedModelName = selectedModel?.displayName.split(" · ")[0] || selectedModel?.id || "this model";
	const approvalModeLimits = useMemo<ApprovalModeLimits | undefined>(() => {
		const unavailable = sessionApprovalModes.filter((mode) => !approvalModes.includes(mode));
		return unavailable.length > 0 ? {
			model: selectedModelName,
			unavailable,
			requested: wantedApprovalMode !== shownApprovalMode ? wantedApprovalMode : undefined,
		} : undefined;
	}, [approvalModes, selectedModelName, sessionApprovalModes, shownApprovalMode, wantedApprovalMode]);
	const startFailed = Boolean(workspaceFailure);
	const chatInFront = !workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget;
	const startStopped = startFailed && !rerunning;
	const [steerClock, setSteerClock] = useState(() => Date.now());
	const steerDeadline = useMemo(() => nextQueuedSteerDeadline(eventsQuery.data ?? [], steerClock), [eventsQuery.data, steerClock]);
	useEffect(() => {
		if (steerDeadline === undefined) return;
		const timer = window.setTimeout(() => setSteerClock(Date.now()), steerDeadline - Date.now() + 250);
		return () => window.clearTimeout(timer);
	}, [steerClock, steerDeadline]);
	const cloudSwitch = useCloudAgentSwitch(session);
	const [chosenSwitch, setChosenSwitch] = useState<PendingAgentSwitch>();
	const pendingSwitch = chosenSwitch?.harness !== session.provider ? chosenSwitch : undefined;
	useEffect(() => {
		if (chosenSwitch && (chosenSwitch.harness === session.provider || !chatInFront)) setChosenSwitch(undefined);
	}, [chatInFront, chosenSwitch, session.provider]);
	const [switchError, setSwitchError] = useState<unknown>();
	const runningSwitch = cloudSwitch.running;
	const snapshot = useMemo(() => {
		const projected = toSnapshot(session, eventsQuery.data ?? [], me, { partial: history?.hasOlder });
		const failedSetupDropped = withoutFailedSetupAttempts(projected.items, startFailed);
		const items = withRunningAgentSwitch(failedSetupDropped, runningSwitch);
		const controller = startStopped ? { ...projected.controller, state: "stopped" as const } : projected.controller;
		return { ...projected, items, controller, settings: { ...shownSettings, approvalMode: shownApprovalMode } };
	}, [eventsQuery.data, history?.hasOlder, me, runningSwitch, session, shownSettings, shownApprovalMode, startFailed, startStopped]);
	const opening = useCloudOpening(cloud?.orgId, session.id);
	const chatHasHumanMessage = useMemo(
		() => (eventsQuery.data ?? []).some((event) => event.type === "chat.user_message"),
		[eventsQuery.data],
	);
	const openingEchos = useMemo(
		() => opening && !chatHasHumanMessage
			? [{ clientMessageId: opening.clientMessageId, text: opening.text, createdAt: opening.createdAt, opening: true as const }]
			: undefined,
		[chatHasHumanMessage, opening],
	);
	const openingSettled = snapshot.turns.some((turn) => turn.state !== "running" && turn.state !== "queued");
	useEffect(() => {
		if (opening && chatHasHumanMessage && openingSettled && cloud?.orgId) forgetCloudOpening(queryClient, cloud.orgId, session.id);
	}, [chatHasHumanMessage, cloud?.orgId, opening, openingSettled, queryClient, session.id]);
	const chatSession = useMemo<WorkspaceSession>(() => {
		const startup = cloudStartupChecklist(session, eventsQuery.data ?? [], workspaceFailure, rerunning);
		return startup ? { ...session, ...startup } : session;
	}, [eventsQuery.data, rerunning, session, workspaceFailure]);
	const activeTurn = snapshot.turns.find((turn) => turn.state === "running");
	const snapshotRef = useRef(snapshot);
	snapshotRef.current = snapshot;
	const annotationNavigation = useChatNavigationRequest<{ id: string; text: string; messageId?: string }>(session.id);
	const navigateToMessage = annotationNavigation.navigate;
	const focusMessage = useCallback((target: ChatMessageFocusTarget) => {
		const replySequence = /^cloud-search-.+-(\d+)$/.exec(target.messageId)?.[1];
		const reply = replySequence === undefined ? undefined
			: snapshotRef.current.items.find((item) => item.kind === "message" && item.role === "assistant" && item.sequence === Number(replySequence));
		navigateToMessage({ id: target.id, text: target.text, messageId: reply?.id ?? target.messageId });
	}, [navigateToMessage]);
	useChatMessageFocus(sessionUiKey(session.id), undefined, eventsQuery.isSuccess, focusMessage);
	const promptSuggestion = useCloudPromptSuggestion(session, snapshot,
		chatInFront && !send.isPending && !activeTurn && !startStopped && !readOnly && !lease.locked && !newWorkDisabled && !controllerTransitioning);
	const queuedTurnCount = snapshot.turns.filter((turn) => turn.state === "queued").length;
	useEffect(() => {
		onConversationWorkChange?.({
			controllerBusy: snapshot.controller.state === "busy",
			hasRunningTurn: Boolean(activeTurn),
			queuedTurnCount,
		});
	}, [activeTurn, onConversationWorkChange, queuedTurnCount, snapshot.controller.state]);
	const interrupt = useMutation({
		mutationFn: async () => {
			if (!cloud || !activeTurn) return;
			await client.cancelTurn(cloud.orgId, session.id, activeTurn.id);
		},
		onSettled: () => void invalidate(),
	});
	const decide = useMutation({
		mutationFn: async ({ requestId, decisionId }: { requestId: string; decisionId: string; key: string; leavesPlanFor?: ApprovalMode }) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			await client.decideChatApproval(cloud.orgId, session.id, requestId, decisionId);
		},
		onSuccess: (_, { key, leavesPlanFor }) => {
			if (!leavesPlanFor || settingsRef.current.key !== key) return;
			const current = settingsRef.current.settings;
			updateSettings({
				...current,
				executionMode: "agent",
				approvalMode: sessionApprovalModes.includes(leavesPlanFor) ? leavesPlanFor : current.approvalMode,
			});
		},
		onSettled: () => void invalidate(),
	});
	const steer = useMutation({
		mutationFn: async ({ text, clientMessageId }: { text: string; clientMessageId?: string }) => {
			if (!cloud || !activeTurn) return { status: "not-accepted" as const, reason: "There is no active turn." };
			const key = clientMessageId ?? crypto.randomUUID();
			const composerEpoch = leaseRef.current.epoch;
			const accepted = await client.steerTurn(cloud.orgId, session.id, activeTurn.id,
				{ text, ...(composerEpoch !== undefined ? { composerEpoch } : {}) }, { idempotencyKey: key });
			leaseRef.current.sent();
			let after = accepted.event.sequence;
			for (let attempt = 0; attempt < 60; attempt++) {
				const page = await client.listChatEvents(cloud.orgId, session.id, { after, limit: 100 });
				for (const event of page.events) {
					if (eventPayload(event).clientMessageId !== key) continue;
					if (event.type === "chat.turn_steered") return { status: "accepted" as const };
					if (event.type === "chat.turn_steer_failed") return { status: "not-accepted" as const, reason: String(eventPayload(event).error ?? "The provider declined the steer.") };
				}
				after = page.nextAfter > after ? page.nextAfter : after;
				await new Promise((resolve) => setTimeout(resolve, 500));
			}
			throw new Error("The provider's steer result is still unknown. Your text was not queued as a new turn.");
		},
		onSettled: () => void invalidate(),
	});
	const queuedSteerOffered = offering.features.includes(QUEUED_STEER_FEATURE) && !readOnly && session.isTerminated !== true;
	const steerQueued = useMutation({
		mutationFn: async (turnId: string) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			const composerEpoch = leaseRef.current.epoch;
			let after: number;
			try {
				const accepted = await client.steerQueuedMessage(cloud.orgId, session.id, turnId,
					composerEpoch !== undefined ? { composerEpoch } : {}, { idempotencyKey: crypto.randomUUID() });
				after = accepted.event.sequence;
			} catch (error) {
				throw queuedSteerRefusal(error);
			}
			leaseRef.current.sent();
			const deadline = Date.now() + QUEUED_STEER_STALE_MS;
			while (Date.now() < deadline) {
				let page: Awaited<ReturnType<typeof client.listChatEvents>>;
				try {
					page = await client.listChatEvents(cloud.orgId, session.id, { after, limit: 100 });
				} catch {
					await new Promise((resolve) => setTimeout(resolve, 1000));
					continue;
				}
				for (const event of page.events) {
					if (eventPayload(event).queuedTurnId !== turnId) continue;
					if (event.type === "chat.turn_steered") return;
					if (event.type === "chat.turn_steer_failed" && eventPayload(event).deleteRequested === true) return;
					if (event.type === "chat.turn_steer_failed") throw new Error(cloudQueuedSteerErrors([event])[turnId]);
				}
				after = page.nextAfter > after ? page.nextAfter : after;
				await new Promise((resolve) => setTimeout(resolve, 500));
			}
			throw new Error(couldNotSteer("the agent did not answer in time"));
		},
		onSettled: () => void invalidate(),
	});
	const queuedSteerErrors = useMemo(() => cloudQueuedSteerErrors(eventsQuery.data ?? [], steerClock), [eventsQuery.data, steerClock]);
	const steeringQueuedTurnIds = useMemo(() => cloudSteeringQueuedTurns(eventsQuery.data ?? [], steerClock), [eventsQuery.data, steerClock]);
	const planOptions: ChatConfigOption[] | undefined =
		session.provider === "claude-code" && modelsQuery.data?.modes?.includes("plan")
			? [{
				id: "mode", name: "Mode", category: "mode", type: "select",
				currentValue: cloud?.permissionMode === "read-only" || settings.executionMode === "plan" ? "plan" : "agent",
				choices: [
					...(cloud?.permissionMode === "read-only" || !modelsQuery.data.modes.includes("default")
						? [] : [{ value: "agent", name: "Agent" }]),
					{ value: "plan", name: "Plan" },
				],
			}]
			: undefined;
	const deletedError = modelsQuery.error instanceof CloudCpError && modelsQuery.error.code === "SESSION_TERMINATED"
		? modelsQuery.error
		: workerConnected ? undefined : wakeError;
	const modelsError = deletedError
		? deletedError.message
		: workerConnected && modelsQuery.error instanceof Error && !isWorkerUnavailable(modelsQuery.error)
			? `Model choices unavailable: ${modelsQuery.error.message}`
			: undefined;
	const claudeOptions: ChatConfigOption[] = session.provider === "claude-code" && listedModels.length > 0 ? [
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: selectedModel?.id, choices: listedModels.map((model) => ({ value: model.id, name: model.displayName, description: model.description })) },
		...(selectedModel?.efforts?.length
			? [{ id: "effort", name: "Effort", category: "thought_level", type: "select" as const, currentValue: shownSettings.reasoningEffort ?? selectedModel.defaultEffort, choices: selectedModel.efforts.map((value) => ({ value, name: formatEffortLabel(value, appI18n.t) })) }]
			: []),
	] : [];
	const providerOptions = [...(planOptions ?? []), ...claudeOptions];
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const openFileFromChat = useCallback((path: string) => {
		if (!onOpenFile) return;
		const artifact = sessionArtifactPath(path, session.id);
		if (artifact) {
			if (!artifactAtPath(session.artifactFiles, artifact)) {
				void queryClient.invalidateQueries({ queryKey: cloud?.shared ? sharedCloudWorkspacesQueryKey : cloudSessionsQueryKey });
			}
			requestWorkspaceFileOpen(session.id, artifact, undefined, "artifact");
			return;
		}
		if (!path.startsWith("/") || !cloud) {
			onOpenFile(path);
			return;
		}
		void cloudWorkspaceFilePath(path, async (query) =>
			(await client.searchWorkspaceReview(cloud.orgId, session.id, { query, limit: 100 })).results.map((result) => result.path),
		).then((relative) => onOpenFile(relative ?? path));
	}, [client, cloud, onOpenFile, queryClient, requestWorkspaceFileOpen, session.artifactFiles, session.id]);
	const [fileQuery, setFileQuery] = useState<string>();
	const [debouncedFileQuery, setDebouncedFileQuery] = useState<string>();
	useEffect(() => {
		if (fileQuery === undefined) return;
		const timer = window.setTimeout(() => setDebouncedFileQuery(fileQuery.trim()), fileQuery ? 150 : 0);
		return () => window.clearTimeout(timer);
	}, [fileQuery]);
	const filesQuery = useQuery<{ paths: string[]; truncated: boolean }>({
		queryKey: ["cloud-composer-files", cloud?.orgId ?? "", session.id, debouncedFileQuery ?? ""],
		enabled: Boolean(cloud && ready && workerConnected && session.isTerminated !== true && debouncedFileQuery !== undefined),
		staleTime: 30_000,
		retry: false,
		placeholderData: (previous) => previous,
		queryFn: async ({ signal }) => {
			if (!debouncedFileQuery) {
				const tree = await client.getWorkspaceReviewTree(cloud!.orgId, session.id, undefined, { signal });
				return { paths: tree.entries.filter((entry) => entry.type === "file").map((entry) => entry.path), truncated: true };
			}
			const found = await client.searchWorkspaceReview(cloud!.orgId, session.id, { query: debouncedFileQuery, limit: 100 }, { signal });
			return { paths: found.results.filter((result) => !result.binary).map((result) => result.path), truncated: found.truncated || Boolean(found.nextCursor) };
		},
	});
	const agentSwitchAllowed = cloudSwitch.available && !readOnly;
	const agentSwitch = useMemo<AgentSwitchProps>(() => ({
		projectId: session.workspaceId,
		cloudOrgId: cloud?.orgId,
		agents: agentSwitchAllowed ? cloudSwitch.agents : [{ id: session.provider, label: agentLabel(session.provider) }],
		pending: agentSwitchAllowed ? pendingSwitch : undefined,
		onPendingChange: agentSwitchAllowed ? setChosenSwitch : () => undefined,
		disabled: !agentSwitchAllowed || Boolean(activeTurn) || cloudSwitch.switching,
		harnessView: "cloud",
	}), [activeTurn, agentSwitchAllowed, cloud?.orgId, cloudSwitch.agents, cloudSwitch.switching, pendingSwitch, session.provider, session.workspaceId]);
	const { models: choiceModels, configOptions } = useChatModelChoices(session.provider, listedModels, providerOptions.length ? providerOptions : undefined, shownSettings.model, undefined, true, false);
	const models = useMemo(
		() => catalogDefaultUnknown ? choiceModels.map((model) => model.default ? { ...model, default: false } : model) : choiceModels,
		[catalogDefaultUnknown, choiceModels],
	);
	return (
		<CloudAttachmentSourceProvider source={attachmentSource}>
		<AdaptiveStreamPacingContext.Provider value>
		<ChatWorkspace
			snapshot={snapshot}
			historyPending={Boolean(cloud && ready) && eventsQuery.data === undefined && !eventsQuery.isError}
			hasOlder={history?.hasOlder}
			loadingOlder={history?.loadingOlder}
			onLoadOlder={onLoadOlder}
			localEchos={openingEchos}
			openingStartedAt={opening?.createdAt}
			onLinkOpen={openLinkInBrowser}
			onSessionLinkOpen={openSessionLink}
			agentSwitch={agentSwitch}
			models={models}
			configOptions={configOptions}
			onChooseConfigOption={configOptions?.length ? (id, choice) => {
				if (!("value" in choice)) return;
				if (id === "model") updateSettings({ ...settingsRef.current.settings, model: choice.value, reasoningEffort: undefined });
				else if (id === "effort") updateSettings({ ...settingsRef.current.settings, reasoningEffort: choice.value });
				else if (id === "mode" && planOptions?.[0].choices.some((option) => option.value === choice.value)) {
					updateSettings({ ...settingsRef.current.settings, executionMode: choice.value === "plan" ? "plan" : "agent" });
				}
			} : undefined}
			onChooseSettings={(next, picked) => {
				const current = settingsRef.current.settings;
				const approvalMode = picked === "approvalMode" ? next.approvalMode : current.approvalMode;
				const shownOnly = (key: "model" | "reasoningEffort") =>
					current[key] === undefined && next[key] !== undefined && next[key] === pendingSettings?.[key];
				updateSettings({
					...current,
					...next,
					approvalMode,
					...(shownOnly("model") ? { model: undefined } : {}),
					...(shownOnly("reasoningEffort") ? { reasoningEffort: undefined } : {}),
				});
			}}
			showApprovalMode={sessionApprovalModes.length > 0}
			approvalModes={approvalModes}
			approvalModeLimits={approvalModeLimits}
			onRememberPermissions={sessionApprovalModes.length > 0 ? (mode) => {
				try {
					if (mode) localStorage.setItem(projectKey, JSON.stringify({ approvalMode: mode, approvalModeChosen: true }));
					else localStorage.removeItem(projectKey);
				} catch { }
				setRememberedMode({ key: projectKey, mode: mode ?? undefined });
				if (mode) updateSettings({ ...settingsRef.current.settings, approvalMode: mode });
			} : undefined}
			rememberedPermissionMode={rememberedMode.key === projectKey ? rememberedMode.mode : readRememberedApprovalMode(projectKey)}
			onDecide={(requestId, decisionId) => decide.mutate({
				requestId, decisionId, key: settingsKey,
				leavesPlanFor: planExitApprovalMode(snapshot.items.find((item) => item.kind === "activity" && item.requestId === requestId), decisionId),
			})}
			busy={send.isPending}
			controllerTransitioning={controllerTransitioning}
			newWorkDisabled={newWorkDisabled}
			commandError={
				shareSettings.error instanceof Error
					? shareSettings.error.message
					: interrupt.error instanceof Error
					? interrupt.error.message
					: decide.error instanceof Error
						? decide.error.message
						: steer.error instanceof Error
							? steer.error.message
						: cancelQueued.error instanceof AlreadyDeliveredError
							? cancelQueued.error.message
					: eventsQuery.error instanceof Error
							? eventsQuery.error.message
						: send.error instanceof Error
							? send.error.message
							: modelsError
			}
			headerActions={headerActions}
			composerNotice={switchError ? (
				<ComposerErrorNotice
					title={appI18n.t("agentSwitch.couldNotSwitch")}
					message={apiErrorMessage(switchError)}
					onDismiss={() => setSwitchError(undefined)}
				/>
			) : live ? <SharedComposerNotice live={live} lease={lease} userId={me} /> : undefined}
			composerLockedPlaceholder={readOnly
				? appI18n.t(myRole === "viewer" ? "chat.shared.viewOnly" : "chat.shared.checkingAccess")
				: lease.locked ? "" : undefined}
			onComposerInput={lease.noteDraft}
			settingsLocked={settingsLocked}
			onOpenFiles={onOpenFiles}
			onOpenFile={openFileFromChat}
			onOpenArtifact={onOpenArtifact}
			onInterrupt={activeTurn ? () => interrupt.mutate() : undefined}
			onSteer={activeTurn && snapshot.capabilities?.includes("steer") ? (text, attachments, clientMessageId) => {
				if (attachments?.length) return Promise.resolve({ status: "not-accepted" as const, reason: "Cloud steering currently accepts text only." });
				return steer.mutateAsync({ text, clientMessageId });
			} : undefined}
			steerPending={steer.isPending}
			onPromoteQueuedTurn={queuedSteerOffered && activeTurn && snapshot.capabilities?.includes("steer") ? (turnId) => steerQueued.mutateAsync(turnId) : undefined}
			promoteQueuedTurnPendingTurnId={steerQueued.isPending ? steerQueued.variables : undefined}
			queuedSteerErrors={queuedSteerErrors}
			steeringQueuedTurnIds={steeringQueuedTurnIds}
			onEditQueuedTurn={queuedEditOffered ? (turnId, text) => editQueued.mutateAsync({ turnId, text }) : undefined}
			canEditQueuedMessage={canEditQueuedMessage}
			editQueuedTurnPendingTurnId={editQueued.isPending ? editQueued.variables?.turnId : undefined}
			onCancelQueuedTurn={readOnly || session.isTerminated === true ? undefined : (turnId) => cancelQueued.mutateAsync(turnId)}
			canCancelQueuedMessage={canCancelQueuedMessage}
			cancelQueuedTurnPendingTurnId={cancelQueued.isPending ? cancelQueued.variables : undefined}
			showSteerButton
			excerptsEnabled
			fileReferencesEnabled
			filePaths={filesQuery.data?.paths}
			filePathsTruncated={filesQuery.data?.truncated}
			onFileQuery={setFileQuery}
			fileReferenceDelivery="text"
			messageByteLimit={CLOUD_CHAT_MESSAGE_BYTES}
			onStageAttachments={stageAttachments}
			cloudAttachments={stageAttachments ? cloudAttachments : undefined}
			onSend={(text, _attachments, clientMessageId, excerpts, fileReferences, displayText) => {
				const numbered = excerpts?.some(ref => ref.snapshot?.referenceFormat === "numbered-v1") || fileReferences?.some(ref => ref.referenceFormat === "numbered-v1");
				const message = numbered
					? cloudMessageAttachments(displayText ?? text, displayText, [])
					: cloudMessageAttachments(text, displayText, fileReferences);
				const content = cloudAnnotationContent(excerpts, fileReferences);
				const agentText = numbered ? message.text + formatNumberedAnnotations(excerpts ?? [], fileReferences) : excerpts?.length ? message.text + cloudExcerptText(excerpts, snapshot) : message.text;
				const sendMessage = (afterSwitch?: boolean) => send.mutateAsync({
					text: message.displayText === "" ? agentText.replace(/^\s+/, "") : agentText,
					attachmentIds: message.attachmentIds,
					clientMessageId,
					...(content.length ? { display: { text: message.displayText, content } } : {}),
					...(afterSwitch ? { afterSwitch } : {}),
				});
				setSwitchError(undefined);
				if (!pendingSwitch) return sendMessage();
				return cloudSwitch.start(pendingSwitch).then(
					() => {
						setChosenSwitch(undefined);
						if (cloud) {
							const targetKey = `cloud-chat-settings:${cloud.orgId}:${session.id}:${pendingSwitch.harness}`;
							rememberAgentSwitchPick(targetKey, pendingSwitch);
							setSelected((current) => current.key === targetKey ? { key: "", settings: {} } : current);
						}
						return sendMessage(true);
					},
					(error: unknown) => {
						setChosenSwitch(undefined);
						setSwitchError(error);
						throw Object.assign(apiErrorException(error), { code: AGENT_SWITCH_FAILED });
					},
				);
			}}
			session={chatSession}
			settingsReady={!startStopped}
			onResumeAgent={workspaceFailure ? () => retryStart.mutate() : undefined}
			resumingAgent={retryStart.isPending}
			resumeError={retryStart.error instanceof Error ? retryStart.error.message : undefined}
			sessionRole={session.kind}
			sessionTabAction={sessionTabAction}
			sessionTabActionPinned={sessionTabActionPinned}
			sessionTitle={session.title}
			onSessionRenamed={() => queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey })}
			reviewerTerminal={reviewerTerminal}
			onOpenReviewerTerminal={onOpenReviewerTerminal}
			reviewerChat={reviewerChat}
			onOpenReviewerChat={onOpenReviewerChat}
			reviewerChatSelected={reviewerChatSelected}
			promptSuggestion={promptSuggestion}
			annotationNavigationRequest={annotationNavigation.request}
			onAnnotationNavigationHandled={annotationNavigation.handled}
			reviewerTarget={reviewerTarget}
			onSelectChat={onSelectChat}
			shellTerminals={shellTerminals}
			shellTarget={shellTarget}
			onSelectShellTerminal={onSelectShellTerminal}
			onCloseShellTerminal={onCloseShellTerminal}
			onRenameShellTerminal={onRenameShellTerminal}
			daemonReady={daemonReady}
			theme={theme}
			auxiliaryTabOrder={auxiliaryTabOrder}
			onAuxiliaryTabOrderChange={onAuxiliaryTabOrderChange}
			tabStripAction={tabStripAction}
			workspaceTabs={workspaceTabs}
			workspaceActiveTabKey={workspaceActiveTabKey}
		/>
		</AdaptiveStreamPacingContext.Provider>
		</CloudAttachmentSourceProvider>
	);
}
