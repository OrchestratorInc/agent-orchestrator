import type { ChatExcerptSnapshot } from "../../shared/chat-excerpt";
import type { ChatFileReference } from "../../shared/chat-file-reference";

import type { ScriptRun } from "../lib/cloud-script-runs";
import type { CloudAttachment } from "../lib/cloud-attachments";

export type SessionMode = "chat" | "tui";

export type TurnState = "queued" | "running" | "completed" | "recovered" | "interrupted" | "failed" | "cancelled";

export type MessageRole = "user" | "assistant";

export type MessageOrigin = "human" | "automation" | "daemon" | "provider";

export type ActivityKind =
	| "command"
	| "file_change"
	| "plan"
	| "reasoning"
	| "approval"
	| "usage"
	| "error"
	| "system"
	| "mcp_tool"
	| "auto_review"
	| "user_input"
	| "ao_action";

export type ActivityStatus =
	| "running"
	| "completed"
	| "recovered"
	| "failed"
	| "cancelled"
	| "pending"
	| "resolved";

export type DeliveryState = "queued" | "sending" | "accepted" | "uncertain" | "failed";

export type ChatSteerOutcome =
	| { status: "accepted" }
	| { status: "not-accepted"; reason: string };

export type ChatEditOutcome = ChatSteerOutcome;

export type PlanStepStatus = "pending" | "in_progress" | "completed";

export interface PlanStep {
	text: string;
	status: PlanStepStatus;
}

export interface ConversationPlan {
	explanation?: string;
	steps: PlanStep[];
	spansTurns?: boolean;
	superseded?: boolean;
}

export interface ConversationUsageLimit {
	resetsAt?: string;
	autoResume: boolean;
	resumeError?: string;
}

export interface ConversationTurn {
	id: string;
	state: TurnState;
	rolledBack?: boolean;
	providerTurnId?: string;
	retryOfTurnId?: string;
	hasRetryAttempt?: boolean;
	hasHumanPrompt?: boolean;
	anchorSequence?: number;
	groupKey?: string;
	errorMessage?: string;
	failureCode?: "provider_key_missing" | "auth_required";
	notRun?: boolean;
	usageLimit?: ConversationUsageLimit;
	requestedAt: string;
	startedAt?: string;
	completedAt?: string;
	diff?: TurnDiff;
	artifacts?: TurnArtifactChange[];
	plan?: ConversationPlan;
}

export type DiffStatus = "added" | "modified" | "deleted" | "renamed";

export interface DiffFile {
	path: string;
	additions: number;
	deletions: number;
	status: DiffStatus;
	oldPath?: string;
}

export interface TurnArtifactChange {
	path: string;
	status: "added" | "modified";
}

export interface TurnDiff {
	files: DiffFile[];
	truncated?: boolean;
}

export interface ConversationContentSummary {
 snapshot?: ChatExcerptSnapshot;
	fileReference?: ChatFileReference;
	annotationId?: string;
	annotationOrder?: number;
	type: string;
	mimeType?: string;
	uri?: string;
	name?: string;
	text?: string;
	comment?: string;
	sourceConversationId?: string;
 sourceMessageId?: string;
	sourceRevision?: number;
}

export interface QueuedMessageEditOptions {
	clientMessageId?: string;
	attachments?: { mimeType: string; data: string }[];
	retainedContent?: number[];
	expectedRevision?: number;
}

export interface ConversationMessage {
	kind: "message";
	id: string;
	turnId?: string;
	sequence: number;
	revision: number;
	role: MessageRole;
	origin: MessageOrigin;
	text: string;
	content?: ConversationContentSummary[];
	editAvailable?: boolean;
	continuation?: boolean;
	authoredByUser?: boolean;
	streaming: boolean;
	delivery?: DeliveryState;
	authorName?: string;
	senderLabel?: string;
	senderSessionId?: string;
	senderProjectId?: string;
	senderDisplayName?: string;
	clientMessageId?: string;
	cloudAttachments?: CloudAttachment[];
	createdAt: string;
}

export interface ConversationBranchPoint {
	turnId: string;
	position: number;
	total: number;
	previousBranchId?: string;
	nextBranchId?: string;
}

export interface DecisionOption {
	id: string;
	label: string;
	kind?: "allow_once" | "allow_always" | "reject_once" | "reject_always";
}

export interface ApprovalDetail {
	method?: string;
	decision?: string;
	resolvedBy?: string;
	subjectKind?: ActivityKind;
	toolKind?: string;
}

export interface ProviderErrorDetail {
	actionUrl?: string;
}

export interface CommandDetail {
	text?: string;
	textTruncated?: boolean;
	command?: string;
	rawCommand?: string;
	cwd?: string;
	output?: string;
	outputMayBePartial?: boolean;
	outputSource?: "stream" | "aggregate";
	outputTruncated?: boolean;
	outputBytes?: number;
	exitCode?: number;
	durationMs?: number;
	reason?: string;
	terminalInput?: string;
	terminalInputTruncated?: boolean;
	processId?: number;
	parentProviderItemId?: string;
	nestedAgent?: boolean;
	providerToolName?: string;
	providerTitle?: string;
	subagentType?: string;
	subagentRetry?: unknown;
	terminalId?: string;
	signal?: string;
}

export interface UserInputDetail {
	inputMode?: "form" | "url";
	message?: string;
	schema?: {
		title?: string;
		description?: string;
		type?: "object";
		required?: string[];
		properties?: Record<string, Record<string, unknown>>;
	};
	url?: string;
	elicitationId?: string;
}

export interface FileChangeFile {
	path: string;
	oldPath?: string;
	status?: DiffStatus;
	additions: number;
	deletions: number;
	patch?: string;
	oldText?: string;
	newText?: string;
	patchTruncated?: boolean;
}

export interface FileChangeDetail {
	files?: FileChangeFile[] | string[];
	patchOutput?: string;
	patchOutputTruncated?: boolean;
}

export interface McpToolDetail {
	server?: string;
	toolName?: string;
	namespace?: string;
	arguments?: unknown;
	input?: unknown;
	result?: unknown;
	content?: unknown;
	error?: string;
	success?: boolean;
	progress?: string;
	progressTruncated?: boolean;
}

export type ReviewRiskLevel = "low" | "medium" | "high" | "critical";

export interface AutoReviewDetail {
	reviewId?: string;
	targetItemId?: string;
	actionType?: string;
	riskLevel?: string;
	rationale?: string;
	decisionSource?: string;
	status?: string;
	userAuthorization?: string;
	host?: string;
	commandSource?: string;
}

export interface RenderRef {
	id: string;
	title: string;
	height: number;
	path: string;
	heights?: Array<[number, number]>;
}

export interface ArtifactRef {
	path: string;
	name: string;
	url: string;
}

export interface AOActionDetail {
	action?: string;
	operationId?: string;
	sourceSessionId?: string;
	targetSessionId?: string;
	displayName?: string;
	previousDisplayName?: string;
	harness?: string;
	previousHarness?: string;
	href?: string;
	prNumber?: number;
	prTitle?: string;
	error?: string;
}

export interface SystemEventDetail {
	event?:
		| "compaction"
		| "model.rerouted"
		| "auth.reauth_required"
		| "provider.failure"
		| "steer"
		| "plan"
		| "context.reset"
		| "context.boundary"
		| "provider_switch"
		| "render"
		| "artifact"
		| "workspace.setup"
		| "workspace.script.run"
		| "account.switched"
		| "account.limit_handoff"
		| "approval_mode.substituted"
		| "session.model_unavailable";
	scriptRun?: ScriptRun;
	accountId?: string;
	retry?: "sent" | "skipped";
	setupStatus?: "succeeded" | "failed" | "timed_out";
	setupStep?: number;
	sourceHarness?: string;
	targetHarness?: string;
	sourceModel?: string;
	targetModel?: string;
	targetEffort?: string;
	targetAutoCompactWindow?: string;
	permissionMode?: ApprovalMode;
	chatPermissionMode?: ApprovalMode;
	switching?: boolean;
	setupFailure?: { command?: string; user?: string; groups?: string[]; dir?: string; logPath?: string };
	setupHint?: string;
	render?: RenderRef;
	artifact?: ArtifactRef;
	fromModel?: string;
	toModel?: string;
	category?: string;
	severity?: "warning" | "error" | (string & {});
	revision?: number;
	origin?: string;
	senderSessionId?: string;
	senderProjectId?: string;
	senderDisplayName?: string;
	clientMessageId?: string;
	deliveredBeforeDelete?: boolean;
	providerInputSourceTurnIds?: string[];
	content?: Array<{
		type: string;
		data?: string;
		mimeType?: string;
		uri?: string;
		name?: string;
		text?: string;
		excerpt?: {
			reference?: { id?: string; order?: number; conversationId?: string; messageId?: string; revision?: number; text?: string; comment?: string; snapshot?: ChatExcerptSnapshot };
			selectedText?: string;
		};
		fileReference?: ChatFileReference;
	}>;
}

export interface PlanDetail {
	explanation?: string;
	steps?: PlanStep[];
	spansTurns?: boolean;
	superseded?: boolean;
}

export interface UsageDetail {
	inputTokens?: number;
	outputTokens?: number;
	totalTokens?: number;
}

export interface CompactionDetail {
	tokensBefore?: number;
	tokensAfter?: number;
	tokensReclaimed?: number;
	contextWindow?: number;
}

export function isCompaction(item: ConversationItem): boolean {
	return item.kind === "activity" && item.detail?.event === "compaction";
}

export function isSteer(item: ConversationItem): boolean {
	return item.kind === "activity" && item.detail?.event === "steer";
}

export function fileChangeFiles(activity: ConversationActivity): FileChangeFile[] {
	const files = activity.detail?.files;
	if (!Array.isArray(files)) return [];
	return files.filter((file): file is FileChangeFile => typeof file === "object" && file !== null);
}

export function reviewedPaths(activity: ConversationActivity): string[] {
	const files = activity.detail?.files;
	if (!Array.isArray(files)) return [];
	return files.filter((file): file is string => typeof file === "string");
}

export function activityPlan(activity: ConversationActivity): ConversationPlan | undefined {
	const steps = activity.detail?.steps;
	if (!Array.isArray(steps) || steps.length === 0) return undefined;
	return { explanation: activity.detail?.explanation, steps };
}

export interface ConversationActivity {
	kind: "activity";
	id: string;
	turnId?: string;
	sequence: number;
	revision: number;
	activityKind: ActivityKind;
	status: ActivityStatus;
	summary: string;
	detail?: CommandDetail &
		ApprovalDetail &
		ProviderErrorDetail &
		FileChangeDetail &
		UsageDetail &
		CompactionDetail &
		McpToolDetail &
		AutoReviewDetail &
		UserInputDetail &
		SystemEventDetail &
		PlanDetail &
		AOActionDetail;
	requestId?: string;
	inputResponse?: {
		action: "accept" | "decline" | "cancel";
		content?: Record<string, unknown>;
	};
	providerItemId?: string;
	decisions?: DecisionOption[];
	createdAt: string;
}

export type ConversationItem = ConversationMessage | ConversationActivity;

export type ApprovalMode = "default" | "accept-edits" | "auto" | "bypass-permissions";

export interface TurnSettings {
	model?: string;
	reasoningEffort?: string;
	approvalMode?: ApprovalMode;
	fastMode?: boolean;
	planMode?: boolean;
	autoCompactWindow?: "200k" | "1m";
}

export interface ChatModel {
	id: string;
	displayName: string;
	description?: string;
	default: boolean;
	efforts?: string[];
	defaultEffort?: string;
	supportsFast?: boolean;
	unavailableReason?: string;
	hint?: string;
}

export interface ChatConfigOption {
	id: string;
	name: string;
	description?: string;
	category?: string;
	type: "select" | "boolean";
	currentValue?: string;
	currentBoolean?: boolean;
	choices: ChatConfigChoice[];
}

export interface ChatConfigChoice {
	permissionMode?: ApprovalMode;
	value: string;
	name: string;
	description?: string;
	group?: string;
	groupName?: string;
	unavailableReason?: string;
	hint?: string;
}

export type ChatConfigOptionValue = { value: string } | { enabled: boolean };

export interface ChatSkill {
	name: string;
	displayName: string;
	description?: string;
	inputHint?: string;
	source?: string;
}

export type ControllerState = "connecting" | "ready" | "busy" | "recovering" | "stopped" | "hibernated";

export interface ConversationUsage {
	contextUsed: number;
	contextWindow: number;
	inputTokens: number;
	outputTokens: number;
	cachedTokens: number;
	totalTokens: number;
	cost?: number | null;
	currency?: string;
}

export interface ConversationRateLimits {
	primaryUsedPercent: number;
	secondaryUsedPercent: number;
	primaryResetsInSeconds?: number;
	secondaryResetsInSeconds?: number;
	planLabel?: string;
}

export interface ModelReroute {
	fromModel?: string;
	toModel: string;
	reason?: string;
	providerTurnId?: string;
	at: string;
}

export interface ConversationAccount {
	authenticationState?: "unknown" | "required" | "authenticated";
	authVerifiedAt?: string;
	lastAuthFailureAt?: string;
	lastAuthFailureReason?: string;
	authFailureId?: string;
	authMode?: string;
	planLabel?: string;
	reauthRequiredAt?: string;
	reauthReason?: string;
}

export type ThreadStatus = "active" | "idle" | "not_loaded" | "system_error" | "closed";

export interface ConversationThreadState {
	status?: ThreadStatus;
	waitingOn?: string[];
	archivedAt?: string;
	closedAt?: string;
}

export interface McpServer {
	name: string;
	status: "starting" | "ready" | "failed" | "cancelled" | (string & {});
	error?: string;
	failureReason?: string;
}

export interface ConversationSnapshot {
	supportsFileReferences?: boolean;
	continueTurnId?: string;
	conversationId: string;
	sessionId: string;
	harness: string;
	mode: SessionMode;
	controller: { state: ControllerState; error?: string };
	turns: ConversationTurn[];
	items: ConversationItem[];
	latestSequence: number;
	oldestSequence: number;
	hasMoreBefore: boolean;
	nativeForkAvailableAfterSequence?: number;
	activeBranchId?: string;
	branchedFromEarlierMessage?: boolean;
	batchedDeliveries?: BatchedDelivery[];
	branchPoints?: ConversationBranchPoint[];
	branchMaterialization?: ConversationBranchMaterialization;
	settings: TurnSettings;
	usage?: ConversationUsage;
	rateLimits?: ConversationRateLimits;
	compactedAt?: string;
	title?: string;
	modelReroute?: ModelReroute;
	account?: ConversationAccount;
	threadState?: ConversationThreadState;
	mcpServers?: McpServer[];
	capabilities?: string[];
}

export interface ConversationBranchMaterialization {
	strategy: "native" | "approximate_context" | (string & {});
	replayTruncated: boolean;
}

export function can(snapshot: ConversationSnapshot, capability: string): boolean {
	return (snapshot.capabilities ?? []).includes(capability);
}

export function answeringModel(snapshot: ConversationSnapshot): string | undefined {
	return snapshot.modelReroute?.toModel ?? snapshot.settings.model;
}

export function brokenMcpServers(snapshot: ConversationSnapshot): McpServer[] {
	return (snapshot.mcpServers ?? []).filter(
		(server) => server.status === "failed" || server.status === "cancelled",
	);
}

export function needsReauth(snapshot: ConversationSnapshot): boolean {
	return snapshot.account?.authenticationState === "required" ||
		(snapshot.account?.authenticationState === undefined && Boolean(snapshot.account?.reauthRequiredAt));
}

export function activeTurn(
	snapshot: ConversationSnapshot,
	{ controllerStopped = false }: { controllerStopped?: boolean } = {},
): ConversationTurn | undefined {
	return (
		(controllerStopped ? undefined : snapshot.turns.find((turn) => turn.state === "running")) ??
		snapshot.turns.find((turn) => turn.state === "queued")
	);
}

export function isUserQueuePrompt(item: ConversationItem): item is ConversationMessage {
	return item.kind === "message" && item.role === "user" &&
		(item.origin === "human" || (item.origin === "automation" && Boolean(item.authoredByUser)));
}

export function hiddenTimelineTurnIds(snapshot: ConversationSnapshot): Set<string> {
	const humanPromptTurnIds = new Set<string>();
	for (const item of snapshot.items) {
		if (isUserQueuePrompt(item) && item.turnId) {
			humanPromptTurnIds.add(item.turnId);
		}
	}
	const resent = new Set(snapshot.turns.flatMap((turn) => turn.retryOfTurnId && !turn.rolledBack ? [turn.retryOfTurnId] : []));
	let worked: Set<string> | undefined;
	return new Set(
		snapshot.turns
			.filter(
				(turn) =>
					((turn.state === "queued" || turn.state === "cancelled") && humanPromptTurnIds.has(turn.id)) ||
					(turn.state === "failed" && turn.failureCode === "auth_required" && resent.has(turn.id) &&
						!(worked ??= turnsThatDidWork(snapshot)).has(turn.id)),
			)
			.map((turn) => turn.id),
	);
}

const TURN_WORK_ACTIVITY_KINDS: ReadonlySet<ActivityKind> = new Set(["command", "file_change", "mcp_tool", "ao_action"]);

export function turnsThatDidWork(snapshot: ConversationSnapshot): Set<string> {
	const turnIds = new Set<string>();
	for (const turn of snapshot.turns) if (turn.diff?.files.length || turn.artifacts?.length) turnIds.add(turn.id);
	for (const item of snapshot.items) {
		if (item.turnId && item.kind === "activity" && TURN_WORK_ACTIVITY_KINDS.has(item.activityKind)) turnIds.add(item.turnId);
	}
	return turnIds;
}

export function fullQueuedTurnOrder(snapshot: ConversationSnapshot, dockOrder: readonly string[]): string[] {
	const queue = snapshot.turns
		.map((turn, index) => ({ turn, index }))
		.filter(({ turn }) => turn.state === "queued")
		.sort((a, b) => a.turn.requestedAt.localeCompare(b.turn.requestedAt) || a.index - b.index)
		.map(({ turn }) => turn.id);
	const docked = new Set(dockOrder);
	if (!dockOrder.every((turnId) => queue.includes(turnId))) return [...dockOrder];
	let next = 0;
	return queue.map((turnId) => (docked.has(turnId) ? dockOrder[next++]! : turnId));
}

export function queuedTurnIds(snapshot: ConversationSnapshot): Set<string> {
	return new Set(snapshot.turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
}

export function pendingApproval(snapshot: ConversationSnapshot): ConversationActivity | undefined {
	return snapshot.items.find(
		(item): item is ConversationActivity =>
			item.kind === "activity" && item.activityKind === "approval" && item.status === "pending",
	);
}

export function pendingUserInput(snapshot: ConversationSnapshot): ConversationActivity | undefined {
	return snapshot.items.find(
		(item): item is ConversationActivity =>
			item.kind === "activity" && item.activityKind === "user_input" && item.status === "pending",
	);
}

export interface BatchedDelivery {
	turnId: string;
	deliveredWithTurnId: string;
	steerActivityId?: string;
}
