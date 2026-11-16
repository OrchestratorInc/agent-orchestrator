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
	/**
	 * chat.turn_interrupted: why a turn ended before it started (deleted,
	 * interface_switch, steered); chat.turn_aborted: author_cannot_write for a message
	 * the control plane did not run.
	 */
	reason?: unknown;
	/** chat.user_message: the files the message carries. */
	attachments?: unknown;
	/** workspace.setup */
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
	/** chat.turn_steer_requested, chat.turn_steered, chat.turn_steer_failed: the queued turn whose message the steer carries. */
	queuedTurnId?: unknown;
	/** chat.turn_steered, chat.turn_steer_failed: the person deleted the queued message while the agent was taking its steer. */
	deleteRequested?: unknown;
	error?: unknown;
	text?: unknown;
	/**
	 * chat.user_message: the model and effort the message was sent with.
	 * session.model_unavailable: the project's model the account lacks.
	 */
	model?: unknown;
	/** session.model_unavailable: the agent whose account lacks the model. */
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

// The approval mode the composer shows when the user hasn't picked one: what
// the worker runs a turn without one with, from the session's mode (trusted
// skips prompts; standard accepts edits for Claude and auto-reviews for
// Codex). It is only shown: such a turn is sent without an approval mode, so
// the worker can step down when the adapter does not offer this one.
function defaultApprovalMode(harness: string, modes: ApprovalMode[], ceiling?: "read-only" | "standard" | "trusted"): ApprovalMode | undefined {
	const preferred: ApprovalMode | undefined = ceiling === "trusted"
		? "bypass-permissions"
		: ceiling === "standard" ? (harness === "codex" ? "auto" : "accept-edits") : undefined;
	return preferred && modes.includes(preferred) ? preferred : modes[0];
}

// Each approval mode steps down only to the modes after it, which approve less
// on their own, so a substitute is never more permissive than the pick: bypass
// never asks, auto approves routine actions, accept edits approves file edits,
// and the agent's own permissions ask. Codex's default is full access, so it
// sits just below bypass. The worker steps down a Claude turn the same way.
const APPROVAL_STEP_DOWN: ApprovalMode[] = ["bypass-permissions", "auto", "accept-edits", "default"];
const CODEX_APPROVAL_STEP_DOWN: ApprovalMode[] = ["bypass-permissions", "default", "auto", "accept-edits"];

/**
 * The approval mode a turn that asks for `wanted` runs with: `wanted` when
 * `offered` has it, else the closest weaker mode it has, else undefined.
 */
export function effectiveApprovalMode(harness: string, wanted: ApprovalMode, offered: readonly ApprovalMode[]): ApprovalMode | undefined {
	const order = harness === "codex" ? CODEX_APPROVAL_STEP_DOWN : APPROVAL_STEP_DOWN;
	const start = order.indexOf(wanted);
	if (start < 0) return offered.includes(wanted) ? wanted : undefined;
	return order.slice(start).find((mode) => offered.includes(mode));
}

// Claude's session modes, as the approval modes the composer names them.
const CLAUDE_MODE_APPROVAL = new Map<string, ApprovalMode>([
	["default", "default"],
	["acceptEdits", "accept-edits"],
	["auto", "auto"],
	["bypassPermissions", "bypass-permissions"],
]);

/**
 * The approval mode deciding `approval` with `decisionId` leaves Plan mode
 * for, or undefined. Claude asks before it leaves Plan mode (its ExitPlanMode
 * tool, an ACP switch_mode request) with one option per mode it can carry on
 * in, and "plan" to keep planning.
 */
function planExitApprovalMode(approval: ConversationItem | undefined, decisionId: string): ApprovalMode | undefined {
	if (approval?.kind !== "activity" || approval.activityKind !== "approval" || approval.detail?.toolKind !== "switch_mode") return undefined;
	return CLAUDE_MODE_APPROVAL.get(decisionId);
}

/**
 * The session's approval modes a model can run. A worker that does not say
 * which modes a model offers (an older one) leaves every mode the session allows.
 */
function modelApprovalModes(sessionModes: ApprovalMode[], model?: { approvalModes?: ApprovalMode[] }): ApprovalMode[] {
	const offered = model?.approvalModes;
	return offered ? sessionModes.filter((mode) => offered.includes(mode)) : sessionModes;
}

function savedApprovalMode(value: unknown): ApprovalMode | undefined {
	return value === "default" || value === "accept-edits" || value === "auto" || value === "bypass-permissions" ? value : undefined;
}

// A saved approval mode counts only with approvalModeChosen: older builds
// also saved the shown default this way, and sending it as the user's pick
// overrode the session's mode (a trusted Chat asked before every command).
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

// An agent switch starts the new agent on the model and effort picked for it.
// They become the session's choice for that agent, so its composer shows them
// and its next message sends them. Otherwise the agent has nothing chosen, and
// the settings bar fills in its model's default effort as a pick (Claude's
// "high" after a switch made on "low"), which the next message then sends.
function rememberAgentSwitchPick(key: string, pick: PendingAgentSwitch) {
	const reasoningEffort = switchEffort(pick);
	if (!pick.model && !reasoningEffort) return;
	try {
		writeCloudTurnSettings(key, { ...readCloudTurnSettings(key), model: pick.model || undefined, reasoningEffort });
	} catch {
		// Storage unavailable: the composer falls back to the agent's own model.
	}
}

// The model and effort the session's worker last reported as its own, so a
// catalog read that reports the same ones again (a refocus, a remount, a
// reconnect) does not override a model or effort picked since.
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
		// Without it, a pick still holds for this mounted session.
	}
}

// The project's remembered approval mode. Only "Remember for this project"
// ever wrote this key, so a value saved before approvalModeChosen existed is
// still the user's pick.
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

/** The files a chat.user_message event lists. */
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

/** The queued turn whose message a steer event carries, if it carries one. */
function queuedSteerTurnID(event: CloudCpClientEvent): string | undefined {
	if (event.type !== "chat.turn_steer_requested" && event.type !== "chat.turn_steered" && event.type !== "chat.turn_steer_failed") return undefined;
	const queuedTurnID = eventPayload(event).queuedTurnId;
	return typeof queuedTurnID === "string" && queuedTurnID ? queuedTurnID : undefined;
}

/** Who a steer is from: the orchestrator's reads as its message did, anyone else's as the person's. */
function steerOrigin(payload: ReturnType<typeof eventPayload>): { origin: string; senderDisplayName?: string } {
	if (payload.origin === "automation") {
		return { origin: "automation", ...(typeof payload.senderLabel === "string" && payload.senderLabel ? { senderDisplayName: payload.senderLabel } : {}) };
	}
	return { origin: "human" };
}

/**
 * A queued message's steer the control plane has asked of the agent and not
 * settled after this long is read as failed. The worker answers within 45 s or
 * the request expires, and the control plane records the expiry the next time
 * the worker polls; a worker gone silent never does, and the message must not
 * read as steering forever. The margin covers clock skew.
 */
export const QUEUED_STEER_STALE_MS = 90_000;

/** Whether a queued message's steer request is past the time it can be answered in. */
function queuedSteerStale(requested: CloudCpClientEvent, now: number | undefined): boolean {
	return now !== undefined && Date.parse(requested.createdAt) + QUEUED_STEER_STALE_MS <= now;
}

/** The row's error for a queued message whose steer failed: why, that it is back in the queue, and how to retry. */
export function couldNotSteer(reason?: string): string {
	const why = reason?.trim().replace(/\.$/, "");
	return `Couldn't steer${why ? `: ${why}` : ""}. It's back in the queue; select Steer to try again.`;
}

/**
 * Why the agent did not take the steer of each queued message whose last
 * steer failed, by its turn: the message is back in the queue, in its place,
 * and its row says so. A steer asked or taken since clears it. With now, a
 * steer left unanswered past QUEUED_STEER_STALE_MS has failed too.
 */
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

/**
 * The queued messages being steered into the running turn: asked of the agent,
 * not answered, and not past QUEUED_STEER_STALE_MS at now. They stay in the
 * queue, marked steering, until the agent answers.
 */
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

/** When the next unanswered queued-message steer turns stale after `after`, or undefined with none left. */
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

/** A refused or unreachable queued steer, as the row's error. */
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

/**
 * The model and effort a turn sent without them runs: the last a message was
 * sent with. A session's first prompt carries the model it launched with, and
 * the agent keeps a model it was given for the turns after it, across a wake
 * too. Undefined when no message named a model: the agent runs its default.
 */
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

/**
 * The turns of the messages this viewer sent: the ones a queued edit
 * (editQueuedMessage) may change, which the control plane allows only their
 * author. A message an orchestrator or automation sent has no author.
 */
export function ownMessageTurns(events: readonly CloudCpClientEvent[] | undefined, viewerUserId: string | undefined): Set<string> {
	const turns = new Set<string>();
	if (!viewerUserId) return turns;
	for (const event of events ?? []) {
		const turnID = eventTurnID(event);
		if (event.type === "chat.user_message" && turnID && eventPayload(event).authorUserId === viewerUserId) turns.add(turnID);
	}
	return turns;
}

/**
 * What a refused delete of a queued message says (cancelTurn), or undefined
 * when there is nothing left to delete: the agent already ran it, and the
 * dock lets it go with the next read.
 */
/**
 * A delete that came too late: the agent had already taken the queued message
 * as a steer, so it was delivered. Said in the chat as well as on the row,
 * which leaves the queue with the steer.
 */
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
			// deleteQueuedMessage left the turn as it was: the agent took it, or it
			// was stopped or ended otherwise.
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

// How long the stream gathers events before the chat redraws: one frame's
// worth, so a burst of deltas is one snapshot rebuild, not one per token.
const STREAM_FLUSH_MS = 32;
/** How often the chat's events are read while its stream is up: a safety net. */
export const CHAT_EVENTS_SAFETY_POLL_MS = 10_000;
/** How often they are read while the stream is down or still opening. */
export const CHAT_EVENTS_FALLBACK_POLL_MS = 1_000;
/**
 * How long a closed chat keeps its events: reopened within it, the
 * conversation shows at once and only what is newer is read. React Query's
 * default (5 min) made a chat reopened after a short break read its whole
 * history again behind a blank screen.
 */
export const CHAT_EVENTS_CACHE_MS = 60 * 60_000;

/**
 * Feeds a Cloud session's chat events into its cached list as the control
 * plane commits them, over the session's shared event stream, so a reply
 * streams in as it is written instead of a second at a time. The stream
 * starts after the newest event the poll already loaded (it waits for the
 * poll's first page rather than replaying the history) and reconnects with
 * backoff. Returns whether the stream is up, from the moment it opens: an idle
 * chat receives no event to prove it, and the chat polls quickly only while
 * the stream is down.
 */
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
	// Before the poll's first page there is nothing to resume from, and the
	// poll loads the history: the stream waits for it.
	const resumeAfter = useCallback(() => {
		const loaded = queryClient.getQueryData<CloudCpClientEvent[]>(["cloud-chat-events", orgId ?? "", sessionId]);
		return loaded ? loaded.at(-1)?.sequence ?? 0 : "loading" as const;
	}, [orgId, queryClient, sessionId]);
	return useCloudSessionEventStream({ baseUrl, orgId, sessionId, enabled, onEvent, resumeAfter });
}

/** A worker's turn_plan activity as the turn's plan, or undefined when it carries none. */
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

/** A worker's usage activity as the conversation's context use, or undefined when it is malformed. */
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

/**
 * A failed Setup step's command, who and where it ran, and its full log in the
 * sandbox; the timeline row words them.
 */
function projectSetupFailure(payload: EventPayload): SystemEventDetail["setupFailure"] {
	const text = (value: unknown) => (typeof value === "string" && value.trim() !== "" ? value.trim() : undefined);
	const groups = Array.isArray(payload.groups) ? payload.groups.filter((group): group is string => typeof group === "string") : [];
	const failure = {
		command: text(payload.command), user: text(payload.user), groups: groups.length > 0 ? groups : undefined,
		dir: text(payload.dir), logPath: text(payload.logPath),
	};
	return Object.values(failure).some((value) => value !== undefined) ? failure : undefined;
}

/**
 * The outcome of the project's Setup commands (Project settings > Scripts), as
 * a system row: Setup runs before the agent starts, so it belongs to no turn.
 */
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

/**
 * The session started on its agent's default model because the creator's
 * Cloud account cannot run the project's model, as a system row: the control
 * plane says so once, before the session's first turn.
 */
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

/**
 * While an agent switch runs, the transcript ends with its "Switching" line, as
 * a local switch draws one, until the control plane's "Switched" line for it
 * arrives.
 */
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

/** Where the session's agent changed: the "Switched" line a local provider switch draws. */
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

/**
 * One Setup or Cleanup run as a system row, placed where its first step event
 * arrived. Its revision is the run's newest event, so the row redraws live
 * even where only the newest event per step is kept.
 */
function scriptRunActivity(run: ScriptRun, createdAt: string): ConversationActivity {
	return {
		kind: "activity", id: `cloud-script-run-${run.runId}`, sequence: run.firstSequence, revision: run.lastSequence,
		activityKind: "system", status: run.status === "running" ? "running" : run.status === "failed" ? "failed" : "completed",
		summary: appI18n.t("scriptRun.activitySummary", { phase: scriptRunTitle(run, appI18n.t), status: scriptRunStatusText(run, appI18n.t) }),
		detail: { event: "workspace.script.run", scriptRun: run },
		createdAt,
	};
}

/** The session's newest Setup run with step events, if any. */
function latestSetupRun(session: WorkspaceSession, events: readonly CloudCpClientEvent[]): ScriptRun | undefined {
	return groupScriptRuns(events, { sessionEnded: cloudSessionScriptsEnded(session.cloud) })
		.filter((run) => run.phase === "setup").at(-1);
}

/**
 * The startup checklist a Cloud Chat session shows while its project's Setup
 * runs: the worker takes no turn until Setup finishes, so the session's first
 * prompt is waiting on Setup, not being worked on. Undefined once no Setup is
 * running (a worker that sends no step events never shows it). It is the
 * checklist a failed start shows (cloudStartupChecklist), with Setup running.
 */
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

/** The workspace startup failure a Cloud Chat session shows as a failed local start: a failed checkout, Setup or agent start. */
function workspaceStartupFailure(session: WorkspaceSession): Extract<CloudStartupProblem, { phase: "failed" }> | undefined {
	const problem = cloudStartupProblem(session);
	return problem?.phase === "failed" && isWorkspaceStartupError(problem.code) ? problem : undefined;
}

const startupStepOrder = ["worktree", "setup", "agent"] as const;

/**
 * The startup checklist of a Cloud Chat session, the one a local Chat start
 * shows: "Setting up" while the project's Setup runs, and the failed start
 * (the step that failed, the error, Retry) once the checkout, Setup or the
 * agent's start failed. While a retry runs it reads as setting up again, with
 * the failed step running, until the worker reports. Undefined when neither.
 */
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

/** A Setup row's outcome and where its run started, for the Setup history rule. */
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

/**
 * The conversation without the Setup attempts that did not pass, as a local
 * start keeps none: while the start is failed the checklist reports the
 * failure, and once a retry passes only the Setup that passed stays. A failed
 * Setup with no startup failure reporting it, and no later pass, still shows.
 */
export function withoutFailedSetupAttempts(items: ConversationItem[], startFailed: boolean): ConversationItem[] {
	const lastPassed = Math.max(-1, ...items.map(setupAttempt).filter((attempt) => attempt?.passed).map((attempt) => attempt!.order));
	return items.filter((item) => {
		const attempt = setupAttempt(item);
		return !attempt?.failed || (!startFailed && attempt.order > lastPassed);
	});
}

/**
 * The name to show on a prompt in a shared session: another participant's,
 * never the viewer's own, and nothing for a message no person sent.
 */
function promptAuthorName(payload: EventPayload, viewerUserId: string | undefined): string | undefined {
	if (!viewerUserId || typeof payload.authorUserId !== "string" || payload.authorUserId === viewerUserId) return undefined;
	return typeof payload.authorName === "string" && payload.authorName.trim() !== "" ? payload.authorName.trim() : undefined;
}

/** A shared session's owner-set settings, as the composer's turn settings. */
export function fromSessionChatSettings(shared: CloudCpSessionChatSettings | undefined): CloudTurnSettings {
	return {
		...(shared?.model ? { model: shared.model } : {}),
		...(shared?.reasoningEffort ? { reasoningEffort: shared.reasoningEffort } : {}),
		...(shared?.approvalMode ? { approvalMode: shared.approvalMode } : {}),
		...(shared?.mode === "read-only" ? { executionMode: "plan" as const } : {}),
	};
}

/** The composer's turn settings, as the session's owner-set settings. */
export function toSessionChatSettings(settings: CloudTurnSettings): CloudCpSessionChatSettings {
	return {
		...(settings.model ? { model: settings.model } : {}),
		...(settings.reasoningEffort ? { reasoningEffort: settings.reasoningEffort } : {}),
		...(settings.approvalMode ? { approvalMode: settings.approvalMode } : {}),
		...(settings.executionMode === "plan" ? { mode: "read-only" as const } : {}),
	};
}

// Preserve the legacy paired-context format for already-prepared deliveries.
const EXCERPT_PAIRED_TEXT_MAX = 16 * 1024;

function excerptContext(text: string): string {
	return text.length <= EXCERPT_PAIRED_TEXT_MAX ? text : `${text.slice(0, EXCERPT_PAIRED_TEXT_MAX)}\n[truncated]`;
}

/**
 * The quoted messages a Cloud message attaches, inlined for its agent as a
 * local Chat delivers them: the selection, then the turn it came from.
 */
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

/**
 * A Cloud message's text for the agent, with, when it attaches quotes or
 * captures, how the transcript shows it: the message as typed beside its
 * quotes and captures. `text` is the typed message with the quotes and
 * captures inlined, so it contains the typed message; the control plane
 * refuses a display text that `text` does not contain (400), so a message
 * whose display text is not in its text goes as text alone.
 */
export function cloudMessageBody(text: string, display?: { text: string; content: ConversationContentSummary[] }): { text: string; displayText?: string; content?: ConversationContentSummary[] } {
	if (!display?.content.length || (display.text !== "" && !text.includes(display.text))) return { text };
	return { text, ...(display.text ? { displayText: display.text } : {}), content: display.content };
}

/** The quoted messages and file captures a Cloud message attaches, as the content blocks a local message keeps for them. */
export function cloudAnnotationContent(excerpts: readonly ChatDraftExcerptReference[] = [], fileReferences: readonly ChatFileReference[] = []): ConversationContentSummary[] {
	return [
		...excerpts.map((excerpt) => {
			// Only the display copy drops background context; agent text and retries
			// still use the complete frozen snapshot.
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

/** A message event's recorded content blocks: the quotes and captures its transcript shows. */
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

/**
 * The chat of a Cloud session from its events. `partial`: the events are the
 * newest part of the history (older pages load on demand), so the first of
 * them is not the session's first.
 */
export function toSnapshot(session: WorkspaceSession, events: CloudCpClientEvent[], viewerUserId?: string, { partial = false }: { partial?: boolean } = {}): ConversationSnapshot {
	const turns = new Map<string, ConversationTurn>();
	const assistant = new Map<string, ConversationMessage>();
	const approvals = new Map<string, ConversationActivity>();
	const activities = new Map<string, ConversationActivity>();
	const turnPatches = new Map<string, (DiffFile & { patch: string })[]>();
	const steerableTurns = new Set<string>();
	const items: ConversationItem[] = [];
	// A person's message by its turn, for an edit made while it was queued.
	const humanMessages = new Map<string, ConversationMessage>();
	// Which turns' prompts and starts are loaded, to tell a turn that ended
	// before the agent started on it from one that ran.
	const promptedTurns = new Set<string>();
	const startedTurns = new Set<string>();
	// Queued messages the person deleted (turn_interrupted of reason deleted).
	const withdrawnTurns = new Set<string>();
	let usage: ConversationUsage | undefined;
	// Live Setup and Cleanup logs. A run with step events replaces the older
	// one-line workspace.setup summary the worker still sends after it; workers
	// that predate step events keep that summary row.
	const scriptRuns = new Map(groupScriptRuns(events, { sessionEnded: cloudSessionScriptsEnded(session.cloud) }).map((run) => [run.runId, run]));
	const placedRuns = new Set<string>();
	const unclaimedSetupRuns = [...scriptRuns.values()].filter((run) => run.phase === "setup");
	// The session's first prompt is recorded before its Setup runs, so the turn
	// it opens is anchored ahead of Setup and Setup would read after the reply.
	// A Setup that ran before any turn started sits just above that prompt.
	// Without the session's start loaded, the first prompt here is not its first.
	const firstTurnStart = events.find((event) => event.type === "chat.turn_started")?.sequence ?? Infinity;
	// A turn whose prompt is above the loaded events began before every prompt
	// here (a prompt queued while it ran sits inside it, ahead of its later
	// events): it sorts before them, in the order of its first loaded event.
	const windowStart = events[0]?.sequence ?? 0;
	const windowSpan = (events.at(-1)?.sequence ?? 0) - windowStart + 2;
	const firstPrompt = partial ? undefined : events.find((event) => event.type === "chat.user_message")?.sequence;
	const runSequence = (run: ScriptRun) =>
		run.phase === "setup" && firstPrompt !== undefined && firstPrompt < run.firstSequence && run.firstSequence < firstTurnStart
			? firstPrompt - 0.5 : run.firstSequence;
	for (const event of events) {
		if (event.type === "chat.user_message_edited") {
			// The edit of a message still queued: it reads as edited, in place,
			// and starts nothing of its own. One whose message is not loaded has
			// it rewritten already when that page loads.
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
			// A turn starts with its prompt. One whose first event here is any
			// other began before the loaded events (a page starts inside the
			// turn a later prompt queued in, or inside a very long one): it had
			// started.
			const startedEarlier = partial && event.type !== "chat.user_message" && event.type !== "chat.turn_started";
			const promptAbove = partial && event.type !== "chat.user_message";
			turns.set(turnID, {
				// Keyed by the turn alone: as older pages load, a turn first seen
				// partway gains its prompt, and the reader's view stays on it.
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
			// No agent started on it: its prompt is loaded with no start after it
			// (a turn starts after its prompt), or it ended at attempt 0, never
			// claimed, which holds on a page without its prompt too.
			// The control plane marks a message it did not run (reason
			// author_cannot_write), which holds on a page holding only its end.
			const notRun = event.type === "chat.turn_aborted" && eventPayload(event).reason === "author_cannot_write";
			const neverStarted = notRun || (!startedTurns.has(turnID) && (promptedTurns.has(turnID) || eventPayload(event).attempt === 0));
			if (event.type === "chat.turn_interrupted" && (eventPayload(event).reason === "deleted" || eventPayload(event).reason === "steered")) {
				// Deleted: the person took the queued message back. Steered: it
				// went into the running turn instead, where its chat.turn_steered
				// row shows it.
				// The person took the queued message back: it leaves the
				// conversation, as a local chat drops a queued message deleted from
				// its dock. Any other ending (a switch to the terminal, an older
				// event, a reason this desktop does not know) keeps the person's
				// text, shown interrupted as local shows a queue its Stop ended.
				turn.state = "cancelled";
				delete turn.startedAt;
				withdrawnTurns.add(turnID);
			} else if (neverStarted) {
				// Ended before it ran: it did no work to time (a page without its
				// prompt took its first event for its start).
				delete turn.startedAt;
				if (event.type === "chat.turn_aborted") {
					// Refused when its turn came: not run, with the reason, rather than
					// a run that failed.
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
			// The worker re-sends the whole plan as it changes; the turn keeps the newest.
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
			// The worker's one turn notice: the approval mode it ran in place of
			// one the turn's model does not offer. Only its words are kept, so a
			// worker row can never name a page or artifact this desktop would open.
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
					// The provider's own item id, which a subagent's steps name as their parent.
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
				// Moved above the first prompt: insert it there too, so it leads a
				// prompt with no turn id (both then share one turn-less timeline
				// group, kept in item order) as well as one with a turn id.
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
			// A message sent while the switch ran was held for the new agent, which
			// answers it: the line goes above it, where a local switch (done before
			// the message is sent) draws it.
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
			// The newest setup run that began before this summary is the run it summarizes.
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
		// A message of only files has no text.
		const attachments = event.type === "chat.user_message" ? eventAttachments(event) : [];
		if (!text && attachments.length === 0) continue;
		if (event.type === "chat.user_message") {
			const payload = eventPayload(event);
			const automation = payload.origin === "automation";
			// A person's message with quotes or file captures reads as they typed it,
			// with the captures beside it; the agent got them inlined.
			const content = automation ? undefined : eventContent(payload);
			const senderSessionId = automation && typeof payload.senderSessionId === "string" ? payload.senderSessionId : undefined;
			const senderProjectId = automation && typeof payload.senderProjectId === "string" ? payload.senderProjectId : undefined;
			const senderDisplayName = automation && typeof payload.senderDisplayName === "string" ? payload.senderDisplayName : undefined;
			const message: ConversationMessage = {
				kind: "message", id: `cloud-event-${event.sequence}`, sequence: event.sequence, revision: 1,
				turnId: turnID, role: "user", origin: automation ? "automation" : "human",
				authorName: automation ? undefined : promptAuthorName(payload, viewerUserId),
				// An empty typed text is not recorded, so a message with captures and
				// no display text was only the captures, and one with neither only its files.
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
		// A queued message being steered stays in the queue, marked steering
		// (cloudSteeringQueuedTurns); its failure is the row's reason.
		if (event.type === "chat.turn_steer_requested" || event.type === "chat.turn_steer_failed") continue;
		if (event.type === "chat.turn_steered") {
			const payload = eventPayload(event);
			const clientMessageID = payload.clientMessageId;
			items.push({
				kind: "activity", id: `cloud-steer-${event.sequence}`, turnId: turnID, sequence: event.sequence,
				revision: 1, activityKind: "system", status: "completed", summary: `Steered: ${text}`,
				detail: {
					event: "steer", text, ...steerOrigin(payload), clientMessageId: typeof clientMessageID === "string" ? clientMessageID : undefined,
					// Deleted while the agent was taking it: it got the message anyway.
					...(payload.deleteRequested === true ? { deliveredBeforeDelete: true } : {}),
				},
				createdAt: event.createdAt,
			});
			continue;
		}
		if (event.type !== "chat.assistant_delta") continue;
		// Older Cloud workers persisted this Codex CLI status as assistant text.
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
			// A step the user stopped reads as stopped, as it does locally; one whose
			// turn ended any other way has no known outcome.
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
	// A withdrawn message shows nowhere: not its bubble, its files or its edits.
	const shownItems = withdrawnTurns.size > 0 ? items.filter((item) => !item.turnId || !withdrawnTurns.has(item.turnId)) : items;
	const orderedTurns = [...turns.values()];
	// The first durable send may wait briefly for the worker to claim it. It is
	// the active request; only later messages should appear in the queue. While
	// the project's Setup runs, or after the checkout, Setup or the agent's start
	// failed, it is not: nothing works on it until the workspace is ready, and
	// the startup checklist (cloudStartupChecklist) shows that instead.
	if (!orderedTurns.some((turn) => turn.state === "running") && !cloudSetupStartupSteps(session, events) && !workspaceStartupFailure(session)) {
		const next = orderedTurns.find((turn) => turn.state === "queued");
		if (next) {
			next.state = "running";
			// Its working time starts once a Setup it waited on ended, not when
			// it was sent: Setup time is not agent time.
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
		// File captures reach a Cloud agent inlined in the message text.
		supportsFileReferences: true,
	};
}

// Where a Freestyle (and Docker) sandbox checks the repository out.
const SANDBOX_CHECKOUT_ROOT = "/workspace/repository/";

/**
 * The workspace-relative path a file link in a Cloud agent's reply names. An
 * agent links files by their path in its sandbox, which the worker does not
 * serve: a path in the usual checkout loses that root, and any other absolute
 * one (a Coder workspace's own directory) is matched to a workspace file it
 * ends with, via the worker's search. Undefined when none matches.
 */
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

// The control plane reports a worker that did not answer in time as
// TRANSPORT_TIMEOUT (the queued request expired) or WORKER_TIMEOUT (its own
// wait for the answer ran out): both are a slow worker, not a failure.
function isTransportTimeout(error: unknown): boolean {
	return error instanceof CloudCpError && (error.code === "TRANSPORT_TIMEOUT" || error.code === "WORKER_TIMEOUT");
}

// A worker that has just connected can fail its first catalog reads while it
// still prepares the agent (a Coder workspace finishing its tool installs,
// Claude Code upgrading). The control plane reports those as
// WORKER_OPERATION_FAILED; they are asked again for about a minute, with a
// growing wait, before the error shows.
function isWorkerOperationFailed(error: unknown): boolean {
	return error instanceof CloudCpError && error.code === "WORKER_OPERATION_FAILED";
}
const MODELS_FAILED_RETRIES = 6;
// The worker says it is still setting up the coding agent or its Chat adapter
// (a template installing its tools in the background, or the worker's own
// install of a pinned release). That clears within a minute or two, so it is
// asked again quietly, every few seconds, for longer.
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
	/** Opens a web link in this session's Browser tab on this computer. */
	onOpenLinkInBrowser?: (uri: string) => Promise<void>;
	/** The session's reviewer, when it runs as a chat: a tab beside the chat, as in a local session. */
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	reviewerChatSelected?: boolean;
	/** The tab strip's own action, such as a new shell. */
	tabStripAction?: ReactNode;
	/** The files open in the center pane, as tabs beside the chat, as a local chat has them. */
	workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void; onClose?: () => void }>;
	workspaceActiveTabKey?: string;
	/** A file in front hides the chat. */
	workspaceFileActive?: boolean;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	/** Keep the session tab's action showing at rest, while an interface switch is in flight. */
	sessionTabActionPinned?: boolean;
	/** The Cloud reviewer terminal, shown as its own tab beside the chat as in local sessions. */
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	/** The selected reviewer pane, if any. */
	reviewerTarget?: Extract<TerminalTarget, { kind: "reviewer" }>;
	onSelectChat?: () => void;
	/** This session's sandbox shells, shown as tabs beside the chat as in local sessions. */
	shellTerminals?: ShellTerminal[];
	/** The selected shell pane, if any. */
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
	/** Opens one of the session's artifacts in the inspector's Artifacts tab, by its artifact path. */
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
	// A shared session's live state: who else has it open, who holds its
	// composer (the lock on talking to the agent), and the model and settings
	// its owner chose.
	const live = useCloudSessionLive(cloud?.orgId, session.id, Boolean(cloud && ready && session.isTerminated !== true));
	// Who this person is, as the server sees them on the live stream; the
	// remembered account only until the stream says.
	const me = live?.you || userId;
	// The owner sets a shared session's model and settings; everyone else sees
	// them read-only and their turns run with them.
	const settingsLocked = Boolean(live && !live.canConfigure);
	const queryClient = useQueryClient();
	// A failed checkout or Setup reads the way a failed local Chat start does: the
	// setup checklist under the opening message, with Retry. Retry asks the running
	// worker to prepare the workspace again; until it reports, the step runs again.
	const workspaceFailure = workspaceStartupFailure(session);
	const [retriedFailureAt, setRetriedFailureAt] = useState<string>();
	const rerunning = retriedFailureAt !== undefined && retriedFailureAt === workspaceFailure?.at;
	// Ends a retry still waiting for an idle-paused workspace's worker when Chat goes away.
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
	// The approval modes the session allows. A pick among them is kept even
	// while the selected model cannot run it (approvalModes below), so it
	// returns once a model that can is picked again.
	const sessionApprovalModes = useMemo(() => allowedApprovalModes(session.provider, cloud?.permissionMode), [session.provider, cloud?.permissionMode]);
	// settings.approvalMode is only the user's pick; the composer shows this
	// default when there is none.
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
	// The selection version when the worker's catalog was last read. A choice
	// made after it (while the worker was still starting, or during the
	// request) wins over the worker's native model when the catalog arrives.
	const catalogSyncRef = useRef({ key: settingsKey, version: 0 });
	if (catalogSyncRef.current.key !== settingsKey) catalogSyncRef.current = { key: settingsKey, version: 0 };
	// The owner's choice becomes the session's, so everyone in it sees it.
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
			// The choice still applies for this mounted session when storage is unavailable.
		}
		if (live?.canConfigure) shareSettings.mutate(next);
	};
	// The model catalog comes from the session's worker, so it is asked for only
	// while a worker is connected. Until then (a sandbox still starting, an
	// idle-paused one waking, a restore) there is nothing to report: the picker
	// keeps the saved choice, and the query runs by itself once the session
	// list reports the worker connected, however long that takes. The catalog is
	// always stale, so a reconnect the session list sees refreshes it too.
	const workerConnected = session.runtimeConnected === true;
	const liveRef = useRef({ connected: workerConnected, terminated: session.isTerminated === true });
	liveRef.current = { connected: workerConnected, terminated: session.isTerminated === true };
	// The owner opening a shared session whose settings were never set makes
	// their current choice the session's, once.
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
		// A deleted session has no worker and cannot be resumed (only restored),
		// so it is not asked. Keying on the worker generation reloads the
		// catalog for each new worker, including the one a restore starts,
		// instead of keeping an answer from a worker that is gone. An agent
		// switch keeps the worker generation, so the agent keys it too.
		queryKey: ["cloud-chat-models", cloud?.orgId ?? "", session.id, session.provider, session.terminalGeneration ?? ""],
		enabled: Boolean(cloud && ready && workerConnected && session.isTerminated !== true &&
			(session.provider === "codex" || session.provider === "claude-code")),
		// The TUI may change its native model while Chat is unmounted.
		staleTime: 0,
		refetchOnMount: "always",
		// The session list can still call a worker connected for a few seconds
		// after it left (a pause), or after a quick pause and resume it never saw.
		// Keep asking only while it does; once it reports the worker gone, the
		// query waits for the next connection instead. A worker's first catalog
		// read can outlast one request (it prepares the agent's tools first), so
		// a timed-out request is asked again a few times before it shows.
		retry: (failures, error) => liveRef.current.connected && !liveRef.current.terminated &&
			(isWorkerUnavailable(error) || (isTransportTimeout(error) && failures < 3) ||
				(isWorkerOperationFailed(error) && failures < MODELS_FAILED_RETRIES) ||
				(isAgentSettingUp(error) && failures < AGENT_SETUP_RETRIES)),
		retryDelay: modelsRetryDelay,
		queryFn: async ({ signal }) => {
			const selectionVersionAtFetch = catalogSyncRef.current.version;
			const catalog = await client.listChatModels(cloud!.orgId, session.id, { signal });
			// The worker listed the account's own models, which the control plane
			// keeps as the account's Cloud list: every New Task and settings picker
			// for this agent reads it again now rather than on its next refresh.
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
		// The event stream delivers events as they are committed; the poll only
		// catches what a dropped or not-yet-open stream missed. An open stream
		// counts from the moment it opens, so an idle chat slows down at once.
		refetchInterval: streaming ? CHAT_EVENTS_SAFETY_POLL_MS : CHAT_EVENTS_FALLBACK_POLL_MS,
		// The newest page first, then only what came after the newest event held.
		queryFn: async () => cloud ? loadCloudChatHistory(queryClient, client, cloud.orgId, session.id) : [] as CloudCpClientEvent[],
	});
	// What the list holds of the history; older pages load on demand. Kept as
	// long as the list is, so a kept list never reads as the whole history.
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
	// The half hour before the first page follows it at once.
	const recentPending = Boolean(history?.recentPending && eventsQuery.data);
	useEffect(() => {
		if (recentPending) loadOlder(true);
	}, [loadOlder, recentPending]);
	const onLoadOlder = useCallback(() => loadOlder(), [loadOlder]);
	// Until the worker lists its models (it starts the agent to ask, after a
	// sandbox waking from a pause has come back), the picker shows the control
	// plane's catalog for the agent: what a worker on the same CLI and
	// credential last reported, or what the pinned CLI release runs. It needs
	// no worker, so a waking session shows its models at once.
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
	// A turn sent without a pick runs the session's own model: the last one its
	// messages were sent with, else the agent's default (lastChosenChatSettings).
	// While the catalog stands in for the worker's list, the composer names that
	// model, as the worker does once it answers. Nothing is named until the
	// session's messages have loaded, so the label does not switch from a
	// catalog's default to the session's model: until then no default is marked
	// (neither the control plane's nor the device's own, below), and nothing
	// falls back to one. It is only shown: it is not saved as a pick, and a
	// turn still carries no model unless one is picked.
	const lastChosen = useMemo(() => lastChosenChatSettings(eventsQuery.data), [eventsQuery.data]);
	// With no model named, the default is the control plane's catalog's, once it
	// has answered (or failed): the device's own may differ.
	const catalogDefaultUnknown = !modelsQuery.data &&
		(!eventsQuery.data || (!lastChosen && catalogQuery.isPending && catalogQuery.fetchStatus !== "idle"));
	// Named from the session's own list, as New Task names Cloud's catalog: Claude's
	// "haiku" is the model its description names, which this worker's CLI runs.
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
	// The session's approval modes the selected model can run: Claude offers
	// Auto-approve only for models that support it (not Haiku).
	const selectedModelApprovalModes = selectedModel?.approvalModes;
	const approvalModes = useMemo(
		() => modelApprovalModes(sessionApprovalModes, selectedModelApprovalModes ? { approvalModes: selectedModelApprovalModes } : undefined),
		[sessionApprovalModes, selectedModelApprovalModes],
	);
	// Opening Chat wakes an idle-paused session once, the way opening its
	// terminal does. It is not repeated while Chat stays open, so an open tab
	// does not keep an idle session from pausing.
	const [wakeError, setWakeError] = useState<CloudCpError>();
	useEffect(() => {
		setWakeError(undefined);
		if (!cloud || !ready || liveRef.current.connected || liveRef.current.terminated) return;
		let active = true;
		// Shared with the session view's own resume, so opening sends one request.
		resumeCloudSession(client, cloud.orgId, session.id).catch((error: unknown) => {
			// A session deleted under this view says so; any other refusal leaves
			// the session's own lifecycle state to explain itself.
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
		// A model picked from the local list before the catalog arrived is kept
		// only if this worker can run it; otherwise the worker's own model applies.
		const catalog = native.models ?? [];
		const runnable = !current.model || catalog.length === 0 || catalog.some((model) => model.id === current.model);
		// The worker's own model applies where nothing is picked, and when it
		// changed since it was last seen (the TUI's /model); a refetch that
		// reports the same one again leaves a pick made since then alone.
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
		// The owner's window shares the worker's model with the session, so the
		// others see, and their turns run with, the model the owner sees.
		if (live?.canConfigure) shareSettings.mutate(next);
		// The native model and effort are saved; the approval mode stays the one
		// the user picked in this session, if any.
		try { writeCloudTurnSettings(settingsKey, { ...next, approvalMode: readCloudTurnSettings(settingsKey).approvalMode }); } catch { /* keep the mounted choice */ }
	}, [modelsQuery.dataUpdatedAt, settingsKey]);
	// A waking session's worker connects in these events, polled every second,
	// before the sessions list's slower poll says so: refresh the session then,
	// so Chat stops waiting for a worker that is already back.
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
	// A guest's grant is per session, so their chat box opens only once the
	// server has said which role this session gives them.
	const readOnly = myRole === "viewer" || (myRole === undefined && cloud?.shared === true);
	const lease = useComposerLease({ client, orgId: cloud?.orgId, sessionId: session.id, userId: me, live, canWrite: !readOnly });
	const leaseRef = useRef(lease);
	leaseRef.current = lease;
	const attachmentSource = useMemo(
		() => cloud && ready && baseUrl ? { baseUrl, orgId: cloud.orgId, sessionId: session.id } : undefined,
		[baseUrl, cloud, ready, session.id],
	);
	// Files are offered only while the control plane takes them (attachments in
	// the session organization's offering), up to its per-file limit.
	const offering = useCloudOffering(Boolean(cloud), cloud?.orgId);
	const attachmentMaxBytes = offering.maxAttachmentBytes;
	// No more than the main process carries in one request, either.
	const cloudAttachments = useMemo(() => attachmentMaxBytes === undefined
		? undefined : { maxBytes: Math.min(attachmentMaxBytes, MAX_CLOUD_CP_BINARY_BYTES) }, [attachmentMaxBytes]);
	// A file attached in the composer is uploaded to the session right away;
	// the composer lists it by reference until the message is sent.
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
			// A message sent with an agent switch goes to the new agent, which
			// starts on the model and effort the switch named: the old agent's
			// picks are not its.
			const selectedSettings: CloudTurnSettings = !afterSwitch && settingsRef.current.key === settingsKey ? settingsRef.current.settings : {};
			// Without the user's pick the turn carries no approval mode: the
			// worker derives it from the session's mode. A pick the turn's model
			// cannot run is sent as the mode the composer shows in its place.
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
				// A control plane from before message displays refuses the fields,
				// and one that finds the display text missing from the text refuses
				// it (both 400). The text already carries the quotes and captures,
				// so it goes as text alone, with the same key.
				if (!(error instanceof CloudCpError) || error.status !== 400) throw error;
				return client.sendSessionMessage(cloud.orgId, session.id, request, { idempotencyKey: clientMessageId });
			}
		},
		onSuccess: () => {
			// The send released the lease to whoever is next in line.
			leaseRef.current.sent();
			void invalidate();
		},
	});
	// A queued message is edited in place while the agent has not started on
	// it. The composer hands over the message with its kept and added files
	// listed by reference, as it sends one; the files go as attachment ids.
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
				// The agent took the message, it was stopped, or its session ended
				// meanwhile: the queue editor lets go of it, keeping the edit's text,
				// as it does for a local turn that is no longer queued.
				if (error instanceof CloudCpError && (error.code === "TURN_NOT_QUEUED" || error.code === "SESSION_TERMINATED")) {
					const reason = error.code === "SESSION_TERMINATED"
						? "This session has ended, so this message can no longer be edited." : error.message;
					throw Object.assign(new Error(reason), { code: "CHAT_TURN_NOT_QUEUED" });
				}
				// Any other refusal (a text too long, files the worker cannot take)
				// left the message as it was, so the editor gets the edit back with
				// the reason. A server error, a rate limit or no answer stays
				// pending for the editor to retry.
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
	// Only the author's own message, and only one whose text alone is what the
	// agent receives. The control plane takes an edit of a message with quotes
	// or captures but drops them, and they are inlined in the text the agent
	// gets, which the composer cannot rebuild, so such a message offers no Edit.
	const canEditQueuedMessage = useCallback(
		(message: ConversationMessage) => Boolean(message.turnId && ownTurns.has(message.turnId) && !message.content?.length),
		[ownTurns],
	);
	// A queued message is taken back before the agent starts on it. Where the
	// control plane offers queued-delete, that never stops a turn the agent
	// took meanwhile (deleteQueuedMessage, 409 TURN_NOT_QUEUED), as a local
	// chat's Delete does; an older one only stops turns (cancelTurn). It lets
	// whoever may direct the turn do it: the message's author, whoever
	// started the session, the organization's owners and admins, and the
	// composer lease holder.
	const queuedDeleteOffered = offering.features.includes(QUEUED_DELETE_FEATURE);
	const cancelQueued = useMutation({
		mutationFn: async (turnId: string) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			// The newest event the chat holds: a steer's answer comes after it.
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
			// The agent was taking the message as a steer: its answer decides.
			// Deleted if it did not take it; delivered, and said so, if it did.
			// Read from the events the chat already holds on, not from the
			// session's first event.
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
	// A pick the session no longer allows (its mode dropped while Chat was
	// open) is neither sent nor shown. One the selected model cannot run shows
	// as the mode that runs instead, the one the worker would step down to.
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
	// The chat is the tab in front: not a file, terminal or reviewer tab.
	const chatInFront = !workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget;
	const startStopped = startFailed && !rerunning;
	// The clock a queued message's unanswered steer is read against, moved on
	// when the oldest one would turn stale, so it then shows as failed.
	const [steerClock, setSteerClock] = useState(() => Date.now());
	const steerDeadline = useMemo(() => nextQueuedSteerDeadline(eventsQuery.data ?? [], steerClock), [eventsQuery.data, steerClock]);
	useEffect(() => {
		if (steerDeadline === undefined) return;
		const timer = window.setTimeout(() => setSteerClock(Date.now()), steerDeadline - Date.now() + 250);
		return () => window.clearTimeout(timer);
	}, [steerClock, steerDeadline]);
	// Choosing another agent only records the choice; the switch runs with the next message.
	const cloudSwitch = useCloudAgentSwitch(session);
	const [chosenSwitch, setChosenSwitch] = useState<PendingAgentSwitch>();
	const pendingSwitch = chosenSwitch?.harness !== session.provider ? chosenSwitch : undefined;
	// A pick whose agent the session now runs is done; one is dropped when another tab
	// comes forward, so a message typed on coming back cannot go to an agent no longer meant.
	useEffect(() => {
		if (chosenSwitch && (chosenSwitch.harness === session.provider || !chatInFront)) setChosenSwitch(undefined);
	}, [chatInFront, chosenSwitch, session.provider]);
	const [switchError, setSwitchError] = useState<unknown>();
	const runningSwitch = cloudSwitch.running;
	const snapshot = useMemo(() => {
		const projected = toSnapshot(session, eventsQuery.data ?? [], me, { partial: history?.hasOlder });
		// A failed Setup is reported by the checklist while the start is failed, as a
		// failed local start does; once a retry passes, only the Setup that passed stays.
		const failedSetupDropped = withoutFailedSetupAttempts(projected.items, startFailed);
		const items = withRunningAgentSwitch(failedSetupDropped, runningSwitch);
		// No agent runs until a retry prepares the workspace, as a failed local start reports.
		const controller = startStopped ? { ...projected.controller, state: "stopped" as const } : projected.controller;
		return { ...projected, items, controller, settings: { ...shownSettings, approvalMode: shownApprovalMode } };
	}, [eventsQuery.data, history?.hasOlder, me, runningSwitch, session, shownSettings, shownApprovalMode, startFailed, startStopped]);
	// The brief this device started the session with: shown from the first frame,
	// until the control plane's chat holds the session's first human message, and
	// timed from when it was sent, so the start reads as one conversation.
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
	// While Setup runs, or once the checkout, Setup or the agent's start failed,
	// the session reads as a local start does: the startup checklist in place of a
	// working timer, and messages that send in order once the agent starts.
	const chatSession = useMemo<WorkspaceSession>(() => {
		const startup = cloudStartupChecklist(session, eventsQuery.data ?? [], workspaceFailure, rerunning);
		return startup ? { ...session, ...startup } : session;
	}, [eventsQuery.data, rerunning, session, workspaceFailure]);
	const activeTurn = snapshot.turns.find((turn) => turn.state === "running");
	// A palette chat hit opens here scrolled to its message. A reply hit names
	// the reply by its first event, which is the reply's own sequence.
	const snapshotRef = useRef(snapshot);
	snapshotRef.current = snapshot;
	// This surface stays mounted from session to session, so the target is kept
	// for the session it names and dropped once the chat applied it.
	const annotationNavigation = useChatNavigationRequest<{ id: string; text: string; messageId?: string }>(session.id);
	const navigateToMessage = annotationNavigation.navigate;
	const focusMessage = useCallback((target: ChatMessageFocusTarget) => {
		const replySequence = /^cloud-search-.+-(\d+)$/.exec(target.messageId)?.[1];
		const reply = replySequence === undefined ? undefined
			: snapshotRef.current.items.find((item) => item.kind === "message" && item.role === "assistant" && item.sequence === Number(replySequence));
		navigateToMessage({ id: target.id, text: target.text, messageId: reply?.id ?? target.messageId });
	}, [navigateToMessage]);
	useChatMessageFocus(sessionUiKey(session.id), undefined, eventsQuery.isSuccess, focusMessage);
	// A next-message suggestion once a turn settles, as a local chat offers, only
	// while this person can send and the chat is in front.
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
		// An approved plan ends Plan mode, as it does in local Chat: the agent
		// carries on in the mode it was approved with, and so do later turns.
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
	// A queued message steered into the running turn (the queue's Steer). The
	// control plane records the request and its outcome on the queued turn's
	// events, so the chat reads them back after a remount: the message shows as
	// steering until the agent takes it, and back in the queue, with the reason,
	// when it does not. Without queued-steer the queue offers no Steer: a
	// control plane that cannot record it would leave the message queued.
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
					// The steer is recorded; its outcome reaches the chat's events
					// once the connection is back. Ask again.
					await new Promise((resolve) => setTimeout(resolve, 1000));
					continue;
				}
				for (const event of page.events) {
					if (eventPayload(event).queuedTurnId !== turnId) continue;
					if (event.type === "chat.turn_steered") return;
					// Deleted while it was being steered: the delete says how it ended.
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
	// A session deleted under this view says so in its own words, which say how
	// to recover. A worker that is not connected is not an error: the catalog
	// loads once one is.
	const deletedError = modelsQuery.error instanceof CloudCpError && modelsQuery.error.code === "SESSION_TERMINATED"
		? modelsQuery.error
		: workerConnected ? undefined : wakeError;
	const modelsError = deletedError
		? deletedError.message
		// An error kept from an earlier worker is not this one's.
		: workerConnected && modelsQuery.error instanceof Error && !isWorkerUnavailable(modelsQuery.error)
			? `Model choices unavailable: ${modelsQuery.error.message}`
			: undefined;
	// Claude's model and effort as provider options, the shape local Claude Chat gets from its agent.
	// Effort levels are named as the local chat and New Task name them ("Extra high" for xhigh).
	const claudeOptions: ChatConfigOption[] = session.provider === "claude-code" && listedModels.length > 0 ? [
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: selectedModel?.id, choices: listedModels.map((model) => ({ value: model.id, name: model.displayName, description: model.description })) },
		...(selectedModel?.efforts?.length
			? [{ id: "effort", name: "Effort", category: "thought_level", type: "select" as const, currentValue: shownSettings.reasoningEffort ?? selectedModel.defaultEffort, choices: selectedModel.efforts.map((value) => ({ value, name: formatEffortLabel(value, appI18n.t) })) }]
			: []),
	] : [];
	const providerOptions = [...(planOptions ?? []), ...claudeOptions];
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	// A file link in the transcript opens the file it names in the workspace, as
	// the same file from the Files tab does (one tab for both). A file the agent
	// wrote to this session's artifact directory opens in Artifacts instead, since
	// the file view cannot read outside the checkout (HTTP 422). Artifacts shows
	// it once the session lists it, so one written moments ago opens too; the list
	// is read again now rather than at its next poll.
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
	// "@" offers the workspace's files as a local chat does. A Cloud workspace is
	// searched rather than listed, so what follows "@" is asked of the worker
	// (shortly after typing stops), and an empty query lists its top level.
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
	// The composer's agent and model picker, with the agent's mark, as a local
	// chat has. Where the control plane can switch the session's agent, choosing
	// another one records the choice and the next message switches to it, as a
	// local chat does; otherwise the picker offers only the session's models.
	// Its models are Cloud's for the account (cloudOrgId), as every Cloud picker's are.
	const agentSwitchAllowed = cloudSwitch.available && !readOnly;
	const agentSwitch = useMemo<AgentSwitchProps>(() => ({
		projectId: session.workspaceId,
		cloudOrgId: cloud?.orgId,
		agents: agentSwitchAllowed ? cloudSwitch.agents : [{ id: session.provider, label: agentLabel(session.provider) }],
		pending: agentSwitchAllowed ? pendingSwitch : undefined,
		onPendingChange: agentSwitchAllowed ? setChosenSwitch : () => undefined,
		// Switching is refused while the agent is working or a switch runs.
		disabled: !agentSwitchAllowed || Boolean(activeTurn) || cloudSwitch.switching,
		harnessView: "cloud",
	}), [activeTurn, agentSwitchAllowed, cloud?.orgId, cloudSwitch.agents, cloudSwitch.switching, pendingSwitch, session.provider, session.workspaceId]);
	// The worker runs its own Claude Code, so this computer's CLI version gates nothing here.
	const { models: choiceModels, configOptions } = useChatModelChoices(session.provider, listedModels, providerOptions.length ? providerOptions : undefined, shownSettings.model, undefined, true, false);
	// With no list of the session's own yet, Claude's choices come from the device's
	// own catalog, which marks its own default: not the session's either until its
	// messages have loaded.
	const models = useMemo(
		() => catalogDefaultUnknown ? choiceModels.map((model) => model.default ? { ...model, default: false } : model) : choiceModels,
		[catalogDefaultUnknown, choiceModels],
	);
	return (
		<CloudAttachmentSourceProvider source={attachmentSource}>
		{/* Replies pace their reveal to how the stream arrives (a poll at a time while it is down). */}
		<AdaptiveStreamPacingContext.Provider value>
		<ChatWorkspace
			snapshot={snapshot}
			// Until the first read the chat's history is unknown, not empty: a Cloud
			// session says nothing of its messages, so it opens as one that has some.
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
				// The settings bar sends the shown settings back with every change,
				// so only a pick from the approval menu changes the approval mode,
				// even when it is the shown default.
				const approvalMode = picked === "approvalMode" ? next.approvalMode : current.approvalMode;
				// The model and effort shown while the worker's list loads come back
				// too; they are the session's, not a pick, so they are not saved.
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
				} catch { /* keep this session's choice */ }
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
						// A delete the agent beat: the row left with the steer, so the chat says it.
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
				// The attached notice already names the writer and the place in line.
				// An empty placeholder keeps the input locked without repeating it.
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
				// Staged cloud files travel as the message's attachment ids, and the
				// worker writes the real paths: no reference reaches the agent or the
				// transcript. New attachments share one numbered reference section;
				// prepared legacy payloads retain their original formatting.
				const numbered = excerpts?.some(ref => ref.snapshot?.referenceFormat === "numbered-v1") || fileReferences?.some(ref => ref.referenceFormat === "numbered-v1");
				const message = numbered
					? cloudMessageAttachments(displayText ?? text, displayText, [])
					: cloudMessageAttachments(text, displayText, fileReferences);
				const content = cloudAnnotationContent(excerpts, fileReferences);
				const agentText = numbered ? message.text + formatNumberedAnnotations(excerpts ?? [], fileReferences) : excerpts?.length ? message.text + cloudExcerptText(excerpts, snapshot) : message.text;
				const sendMessage = (afterSwitch?: boolean) => send.mutateAsync({
					// A message of only files and captures or quotes starts with them.
					text: message.displayText === "" ? agentText.replace(/^\s+/, "") : agentText,
					attachmentIds: message.attachmentIds,
					clientMessageId,
					...(content.length ? { display: { text: message.displayText, content } } : {}),
					...(afterSwitch ? { afterSwitch } : {}),
				});
				// A new message replaces an earlier switch's failure.
				setSwitchError(undefined);
				if (!pendingSwitch) return sendMessage();
				// The switch starts first; the message, sent while it runs, waits for
				// the new agent. Nothing was sent if the switch is refused, so the
				// draft stays and the pick is dropped.
				return cloudSwitch.start(pendingSwitch).then(
					() => {
						setChosenSwitch(undefined);
						if (cloud) {
							const targetKey = `cloud-chat-settings:${cloud.orgId}:${session.id}:${pendingSwitch.harness}`;
							rememberAgentSwitchPick(targetKey, pendingSwitch);
							// Settings held from an earlier spell on that agent (a switch back) are
							// stale now: drop them, so the agent's saved choice is read once it runs.
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
			// The chat tab renames the session as a local one does; the rename
			// updates the cached session lists itself, and this catches up the rest.
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
