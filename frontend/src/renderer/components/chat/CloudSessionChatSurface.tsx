import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useChatModelChoices } from "../../hooks/useChatModelChoices";
import { useCloudCp } from "../../hooks/useCloudCp";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { useCloudSessionLive } from "../../hooks/useCloudSessionLive";
import { useComposerLease } from "../../hooks/useComposerLease";
import { cloudSessionsQueryKey } from "../../hooks/useWorkspaceQuery";
import { appI18n } from "../../i18n";
import type { CloudCpClient, CloudCpClientEvent, CloudCpSessionChatSettings } from "../../lib/cloud-cp";
import { cloudStartupProblem, isWorkspaceStartupError, type CloudStartupProblem } from "../../lib/cloud-lifecycle";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { cloudSessionScriptsEnded, groupScriptRuns, parseScriptStepPayload, SCRIPT_STEP_EVENT, type ScriptRun } from "../../lib/cloud-script-runs";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { ApprovalMode, ChatConfigOption, ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot, ConversationTurn, DiffFile, FileChangeFile, SystemEventDetail, TurnSettings } from "../../types/conversation";
import type { TerminalTarget } from "../../types/terminal";
import type { SessionProvisionStep, WorkspaceSession } from "../../types/workspace";
import { requestWorkspaceRetry } from "../CloudSessionStartupError";
import { scriptRunStatusText, scriptRunTitle } from "../ScriptRunPanel";
import { ChatWorkspace } from "./ChatWorkspace";
import type { ApprovalModeLimits } from "./TurnSettingsBar";
import { SharedComposerNotice } from "./SharedComposerNotice";

type EventPayload = {
	attempt?: unknown;
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
	error?: unknown;
	text?: unknown;
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

function eventText(event: CloudCpClientEvent): string | undefined {
	const text = eventPayload(event).text;
	return typeof text === "string" && text.trim() !== "" ? text : undefined;
}

function eventTurnID(event: CloudCpClientEvent): string | undefined {
	const turnID = eventPayload(event).turnId;
	return typeof turnID === "string" && turnID !== "" ? turnID : undefined;
}

export function appendCloudEvents(existing: CloudCpClientEvent[], incoming: CloudCpClientEvent[]): CloudCpClientEvent[] {
	const lastSequence = existing.at(-1)?.sequence ?? 0;
	return [...existing, ...incoming.filter((event) => event.sequence > lastSequence)];
}

export async function loadCloudChatEvents(
	client: Pick<CloudCpClient, "listChatEvents">,
	orgId: string,
	sessionId: string,
	existing: CloudCpClientEvent[],
): Promise<CloudCpClientEvent[]> {
	let events = existing;
	let after = existing.at(-1)?.sequence ?? 0;
	for (;;) {
		const page = await client.listChatEvents(orgId, sessionId, { after, limit: 500 });
		events = appendCloudEvents(events, page.events);
		if (!page.hasMore) return events;
		if (page.nextAfter <= after) throw new Error("Cloud event cursor did not advance.");
		after = page.nextAfter;
	}
}

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

export function toSnapshot(session: WorkspaceSession, events: CloudCpClientEvent[], viewerUserId?: string): ConversationSnapshot {
	const turns = new Map<string, ConversationTurn>();
	const assistant = new Map<string, ConversationMessage>();
	const approvals = new Map<string, ConversationActivity>();
	const activities = new Map<string, ConversationActivity>();
	const turnPatches = new Map<string, (DiffFile & { patch: string })[]>();
	const steerableTurns = new Set<string>();
	const items: ConversationItem[] = [];
	// Live Setup and Cleanup logs. A run with step events replaces the older
	// one-line workspace.setup summary the worker still sends after it; workers
	// that predate step events keep that summary row.
	const scriptRuns = new Map(groupScriptRuns(events, { sessionEnded: cloudSessionScriptsEnded(session.cloud) }).map((run) => [run.runId, run]));
	const placedRuns = new Set<string>();
	const unclaimedSetupRuns = [...scriptRuns.values()].filter((run) => run.phase === "setup");
	// The session's first prompt is recorded before its Setup runs, so the turn
	// it opens is anchored ahead of Setup and Setup would read after the reply.
	// A Setup that ran before any turn started sits just above that prompt.
	const firstTurnStart = events.find((event) => event.type === "chat.turn_started")?.sequence ?? Infinity;
	const firstPrompt = events.find((event) => event.type === "chat.user_message")?.sequence;
	const runSequence = (run: ScriptRun) =>
		run.phase === "setup" && firstPrompt !== undefined && firstPrompt < run.firstSequence && run.firstSequence < firstTurnStart
			? firstPrompt - 0.5 : run.firstSequence;
	for (const event of events) {
		const turnID = eventTurnID(event);
		if (turnID && !turns.has(turnID)) {
			turns.set(turnID, { id: turnID, state: "queued", requestedAt: event.createdAt });
		}
		if (turnID && event.type === "chat.turn_started") {
			const turn = turns.get(turnID)!;
			turn.state = "running";
			turn.startedAt = event.createdAt;
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
					decisions, detail: { method: "ACP session/request_permission", toolKind: typeof payload.toolKind === "string" ? payload.toolKind : undefined },
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
		if (!text) continue;
		if (event.type === "chat.user_message") {
			const payload = eventPayload(event);
			const automation = payload.origin === "automation";
			const senderSessionId = automation && typeof payload.senderSessionId === "string" ? payload.senderSessionId : undefined;
			const senderProjectId = automation && typeof payload.senderProjectId === "string" ? payload.senderProjectId : undefined;
			const senderDisplayName = automation && typeof payload.senderDisplayName === "string" ? payload.senderDisplayName : undefined;
			items.push({
				kind: "message", id: `cloud-event-${event.sequence}`, sequence: event.sequence, revision: 1,
				turnId: turnID, role: "user", origin: automation ? "automation" : "human",
				authorName: automation ? undefined : promptAuthorName(payload, viewerUserId),
				text: automation && typeof payload.displayText === "string" ? payload.displayText : text,
				senderLabel: automation && typeof payload.senderLabel === "string" ? payload.senderLabel : undefined,
				senderSessionId, senderProjectId, senderDisplayName,
				streaming: false, delivery: "accepted", createdAt: event.createdAt,
			});
			continue;
		}
		if (event.type === "chat.turn_steered") {
			const clientMessageID = eventPayload(event).clientMessageId;
			items.push({
				kind: "activity", id: `cloud-steer-${event.sequence}`, turnId: turnID, sequence: event.sequence,
				revision: 1, activityKind: "system", status: "completed", summary: `Steered: ${text}`,
				detail: { event: "steer", text, origin: "human", clientMessageId: typeof clientMessageID === "string" ? clientMessageID : undefined },
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
		if (activity.status === "running" && activity.turnId && turns.get(activity.turnId)?.state !== "running") {
			activity.status = "recovered";
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
		controller: { state: hasRunningTurn ? "busy" : "ready" }, turns: orderedTurns, items,
		latestSequence: events.at(-1)?.sequence ?? 0, oldestSequence: events[0]?.sequence ?? 1,
		hasMoreBefore: false, settings: {}, capabilities: activeTurn && steerableTurns.has(activeTurn.id) ? ["steer"] : [],
	};
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

export function CloudSessionChatSurface({
	session,
	headerActions,
	sessionTabAction,
	onOpenFiles,
	onOpenFile,
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
}: {
	session: WorkspaceSession;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
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
	const { client, ready, userId } = useCloudCp();
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
		// instead of keeping an answer from a worker that is gone.
		queryKey: ["cloud-chat-models", cloud?.orgId ?? "", session.id, session.terminalGeneration ?? ""],
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
			(isWorkerUnavailable(error) || (isTransportTimeout(error) && failures < 3)),
		queryFn: async ({ signal }) => {
			const selectionVersionAtFetch = catalogSyncRef.current.version;
			const catalog = await client.listChatModels(cloud!.orgId, session.id, { signal });
			return { ...catalog, selectionVersionAtFetch };
		},
	});
	const workerModels = modelsQuery.data?.models ?? [];
	const selectedModel = workerModels.find((model) => model.id === settings.model) ?? workerModels.find((model) => model.default);
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
		const abort = new AbortController();
		client.resumeSession(cloud.orgId, session.id, { signal: abort.signal }).catch((error: unknown) => {
			// A session deleted under this view says so; any other refusal leaves
			// the session's own lifecycle state to explain itself.
			if (!abort.signal.aborted && error instanceof CloudCpError && error.code === "SESSION_TERMINATED") setWakeError(error);
		});
		return () => abort.abort();
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
		if (runnable && (chosenSince || (!native.model && !native.reasoningEffort))) return;
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
	const eventsQuery = useQuery({
		queryKey: ["cloud-chat-events", cloud?.orgId ?? "", session.id],
		enabled: Boolean(cloud && ready),
		refetchInterval: 1_000,
		queryFn: async () => {
			if (!cloud) return [] as CloudCpClientEvent[];
			const previous = queryClient.getQueryData<CloudCpClientEvent[]>(["cloud-chat-events", cloud.orgId, session.id]) ?? [];
			return loadCloudChatEvents(client, cloud.orgId, session.id, previous);
		},
	});
	const invalidate = () =>
		queryClient.invalidateQueries({ queryKey: ["cloud-chat-events", cloud?.orgId ?? "", session.id] });
	const myRole = live?.viewers.find((viewer) => viewer.userId === me)?.role;
	// A guest's grant is per session, so their chat box opens only once the
	// server has said which role this session gives them.
	const readOnly = myRole === "viewer" || (myRole === undefined && cloud?.shared === true);
	const lease = useComposerLease({ client, orgId: cloud?.orgId, sessionId: session.id, userId: me, live, canWrite: !readOnly });
	const leaseRef = useRef(lease);
	leaseRef.current = lease;
	const send = useMutation({
		mutationFn: async ({ text, clientMessageId }: { text: string; clientMessageId?: string }) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			const selectedSettings: CloudTurnSettings = settingsRef.current.key === settingsKey ? settingsRef.current.settings : {};
			// Without the user's pick the turn carries no approval mode: the
			// worker derives it from the session's mode. A pick the turn's model
			// cannot run is sent as the mode the composer shows in its place.
			const picked = selectedSettings.approvalMode && sessionApprovalModes.includes(selectedSettings.approvalMode)
				? selectedSettings.approvalMode : undefined;
			const turnModel = workerModels.find((model) => model.id === selectedSettings.model) ?? workerModels.find((model) => model.default);
			const approvalMode = picked && (effectiveApprovalMode(session.provider, picked, modelApprovalModes(sessionApprovalModes, turnModel)) ?? picked);
			const mode = selectedSettings.executionMode === "plan" && modelsQuery.data?.modes?.includes("plan")
				? "read-only" : cloud.permissionMode;
			const composerEpoch = leaseRef.current.epoch;
			return client.sendSessionMessage(cloud.orgId, session.id, {
				text,
				...(selectedSettings.model ? { model: selectedSettings.model } : {}),
				...(selectedSettings.reasoningEffort ? { reasoningEffort: selectedSettings.reasoningEffort } : {}),
				...(mode ? { mode } : {}),
				...(approvalMode && mode !== "read-only" ? { approvalMode } : {}),
				...(composerEpoch !== undefined ? { composerEpoch } : {}),
			}, { idempotencyKey: clientMessageId });
		},
		onSuccess: () => {
			// The send released the lease to whoever is next in line.
			leaseRef.current.sent();
			void invalidate();
		},
	});
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
	const startStopped = startFailed && !rerunning;
	const snapshot = useMemo(() => {
		const projected = toSnapshot(session, eventsQuery.data ?? [], me);
		// A failed Setup is reported by the checklist while the start is failed, as a
		// failed local start does; once a retry passes, only the Setup that passed stays.
		const items = withoutFailedSetupAttempts(projected.items, startFailed);
		// No agent runs until a retry prepares the workspace, as a failed local start reports.
		const controller = startStopped ? { ...projected.controller, state: "stopped" as const } : projected.controller;
		return { ...projected, items, controller, settings: { ...settings, approvalMode: shownApprovalMode } };
	}, [eventsQuery.data, me, session, settings, shownApprovalMode, startFailed, startStopped]);
	// While Setup runs, or once the checkout, Setup or the agent's start failed,
	// the session reads as a local start does: the startup checklist in place of a
	// working timer, and messages that send in order once the agent starts.
	const chatSession = useMemo<WorkspaceSession>(() => {
		const startup = cloudStartupChecklist(session, eventsQuery.data ?? [], workspaceFailure, rerunning);
		return startup ? { ...session, ...startup } : session;
	}, [eventsQuery.data, rerunning, session, workspaceFailure]);
	const activeTurn = snapshot.turns.find((turn) => turn.state === "running");
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
		mutationFn: async ({ requestId, decisionId }: { requestId: string; decisionId: string }) => {
			if (!cloud) throw new Error("Cloud session context is unavailable.");
			await client.decideChatApproval(cloud.orgId, session.id, requestId, decisionId);
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
	const claudeOptions: ChatConfigOption[] = session.provider === "claude-code" && workerModels.length > 0 ? [
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: selectedModel?.id, choices: workerModels.map((model) => ({ value: model.id, name: model.displayName, description: model.description })) },
		...(selectedModel?.efforts?.length
			? [{ id: "effort", name: "Effort", category: "thought_level", type: "select" as const, currentValue: settings.reasoningEffort ?? selectedModel.defaultEffort, choices: selectedModel.efforts.map((value) => ({ value, name: "" })) }]
			: []),
	] : [];
	const providerOptions = [...(planOptions ?? []), ...claudeOptions];
	const { models, configOptions } = useChatModelChoices(session.provider, workerModels, providerOptions.length ? providerOptions : undefined, settings.model);
	return (
		<ChatWorkspace
			snapshot={snapshot}
			onSessionLinkOpen={openSessionLink}
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
				updateSettings({ ...current, ...next, approvalMode });
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
			onDecide={(requestId, decisionId) => decide.mutate({ requestId, decisionId })}
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
					: eventsQuery.error instanceof Error
							? eventsQuery.error.message
						: send.error instanceof Error
							? send.error.message
							: modelsError
			}
			headerActions={headerActions}
			composerNotice={live ? <SharedComposerNotice live={live} lease={lease} userId={me} /> : undefined}
			composerLockedPlaceholder={readOnly
				? appI18n.t(myRole === "viewer" ? "chat.shared.viewOnly" : "chat.shared.checkingAccess")
				// The attached notice already names the writer and the place in line.
				// An empty placeholder keeps the input locked without repeating it.
				: lease.locked ? "" : undefined}
			onComposerInput={lease.noteDraft}
			settingsLocked={settingsLocked}
			onOpenFiles={onOpenFiles}
			onOpenFile={onOpenFile}
			onInterrupt={activeTurn ? () => interrupt.mutate() : undefined}
			onSteer={activeTurn && snapshot.capabilities?.includes("steer") ? (text, attachments, clientMessageId) => {
				if (attachments?.length) return Promise.resolve({ status: "not-accepted" as const, reason: "Cloud steering currently accepts text only." });
				return steer.mutateAsync({ text, clientMessageId });
			} : undefined}
			steerPending={steer.isPending}
			showSteerButton
			onSend={(text, _attachments, clientMessageId) => send.mutateAsync({ text, clientMessageId })}
			session={chatSession}
			settingsReady={!startStopped}
			onResumeAgent={workspaceFailure ? () => retryStart.mutate() : undefined}
			resumingAgent={retryStart.isPending}
			resumeError={retryStart.error instanceof Error ? retryStart.error.message : undefined}
			sessionRole={session.kind}
			sessionTabAction={sessionTabAction}
			sessionTitle={session.title}
			reviewerTerminal={reviewerTerminal}
			onOpenReviewerTerminal={onOpenReviewerTerminal}
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
		/>
	);
}
