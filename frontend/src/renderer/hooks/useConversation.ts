import { apiErrorException } from "../lib/api-error-exception";
import type { ChatFileReference } from "../../shared/chat-file-reference";

import {
	type InfiniteData,
	type QueryClient,
	infiniteQueryOptions,
	useInfiniteQuery,
	useIsMutating,
	useMutation,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { baseUrlForHost, clientForSessionHost } from "../lib/host-clients";
import { HOST_UNREACHABLE_CODES, sessionUiKey } from "../lib/hosts";
import { subscribeWorkspaceFileChanges } from "../lib/workspace-file-events";
import { isMissingWorkspaceError, knownMissingWorkspace, recordWorkspaceResult, workspaceRecoveryEpoch } from "./useSessionWorkspaceFiles";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { resumeAgentError, resumeOpenElsewhere } from "./useCanResumeAgent";
import { useRestoreSession } from "./useRestoreSession";
import { recordDirectWorkerInteraction } from "../lib/session-management-telemetry";
import type { ChatDraftExcerptReference } from "../lib/chat-drafts";
import type {
	ActivityKind,
	ApprovalMode,
	ActivityStatus,
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ControllerState,
	DecisionOption,
	DiffStatus,
	McpServer,
	MessageOrigin,
	MessageRole,
	ChatConfigOption,
	ChatConfigOptionValue,
	ChatModel,
	ChatSkill,
	ChatEditOutcome,
	ChatSteerOutcome,
	PlanStep,
	PlanStepStatus,
	QueuedMessageEditOptions,
	SessionMode,
	ThreadStatus,
	TurnSettings,
	TurnState,
} from "../types/conversation";

type WireSnapshot = components["schemas"]["ConversationSnapshotResponse"];
type WireMessage = components["schemas"]["ConversationMessageResponse"];
type WireActivity = components["schemas"]["ConversationActivityResponse"];
type WireImageContent = components["schemas"]["ConversationImageContentRequest"];
type WireResourceContent = components["schemas"]["ConversationResourceContentRequest"];
type WireExcerptReference = components["schemas"]["ConversationExcerptReferenceRequest"];

export interface ConversationSendInput {
	text: string;
	attachments?: WireImageContent[];
	resources?: WireResourceContent[];
	excerpts?: WireExcerptReference[];
	fileReferences?: ChatFileReference[];
	clientMessageId?: string;
	continuation?: boolean;
	providerSwitch?: Omit<components["schemas"]["ProviderSwitchRequest"], "idempotencyKey" | "message">;
	switchDisplay?: ConversationSwitchDisplay;
}

export interface ConversationSwitchDisplay {
	sourceHarness: string;
	sourceModel?: string;
	targetModel?: string;
	targetEffort?: string;
}

export class ProviderSwitchFailed extends Error {
	constructor(readonly reason: unknown) {
		super(apiErrorMessage(reason));
	}
}

const switchOutcomeUnknown = (status: number, error: unknown) =>
	(status === 502 || status === 503 || status === 504) &&
	!String((error as { code?: unknown } | undefined)?.code ?? "").startsWith("PROVIDER_SWITCH_");

interface ConversationSendMutationInput {
	targetSessionId: string;
	clientMessageId: string;
	input: ConversationSendInput;
}

interface ConversationSessionMutationInput {
	targetSessionId: string;
}

interface ConversationRetryMutationInput extends ConversationSessionMutationInput {
	requestId: string;
	sourceTurnId: string;
}

interface ConversationEditMutationInput extends ConversationRetryMutationInput {
	text: string;
}

export const conversationQueryRoot = ["conversation"] as const;

export function conversationQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation", hostId, sessionId] as const) : ([...conversationQueryRoot, sessionId] as const);
}

export function conversationModelsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-models", hostId, sessionId] as const) : (["conversation-models", sessionId] as const);
}

export function conversationConfigOptionsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-config-options", hostId, sessionId] as const) : (["conversation-config-options", sessionId] as const);
}

const conversationDispatchTrackingQueryKey = ["conversation-dispatch-tracking"] as const;
const conversationLocalEchosQueryKey = ["conversation-local-echos"] as const;
type ConversationDispatchOperation = "edit" | "retry" | "send";
interface ConversationDispatchDescriptor {
	operation: ConversationDispatchOperation;
	requestId: string;
	sourceTurnId?: string;
}
type ConversationDispatchTracking =
	| (ConversationDispatchDescriptor & { state: "pending" })
	| (ConversationDispatchDescriptor & { state: "accepted"; turnId: string });
type ConversationDispatchTrackingBySession = Record<string, ConversationDispatchTracking>;

export type ConversationLocalEcho = {
	clientMessageId: string;
	text: string;
	continuation?: boolean;
	createdAt: string;
	backgroundWake?: boolean;
	queued?: boolean;
	fileReferences?: ChatFileReference[];
	excerpts?: (Pick<ChatDraftExcerptReference, "text" | "comment" | "messageId" | "revision" | "order" | "snapshot"> & { id?: string; conversationId?: string })[];
	turnId?: string;
	providerSwitch?: ConversationSwitchDisplay & {
		targetHarness: string;
		afterSequence: number;
	};
	opening?: boolean;
};
type ConversationLocalEchosBySession = Record<string, ConversationLocalEcho[]>;

function newestCachedSequence(queryClient: QueryClient, sessionId: string, hostId?: string): number {
	const cached = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(sessionId, hostId));
	return Math.max(0, ...(cached?.pages.map((page) => page.latestSequence) ?? []));
}

function switchEcho(
	display: ConversationSwitchDisplay | undefined,
	targetHarness: string | undefined,
	afterSequence: number,
): ConversationLocalEcho["providerSwitch"] {
	return display && targetHarness ? { ...display, targetHarness, afterSequence } : undefined;
}

function addConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	echo: ConversationLocalEcho,
): void {
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => ({
		...current,
		[targetSessionId]: [
			...(current[targetSessionId] ?? []).filter((existing) => existing.clientMessageId !== echo.clientMessageId),
			echo,
		],
	}));
}

export function seedOpeningLocalEcho(
	queryClient: QueryClient,
	sessionId: string,
	hostId: string | undefined,
	echo: Pick<ConversationLocalEcho, "clientMessageId" | "text" | "createdAt">,
): void {
	addConversationLocalEcho(queryClient, sessionUiKey(sessionId, hostId), { ...echo, opening: true });
}

function acceptConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	clientMessageId: string,
	turnId: string,
): void {
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => {
		const echoes = current[targetSessionId];
		if (!echoes) return current;
		let changed = false;
		const nextEchoes = echoes.map((echo) => {
			if (echo.clientMessageId !== clientMessageId || echo.turnId === turnId) return echo;
			changed = true;
			return { ...echo, turnId };
		});
		return changed ? { ...current, [targetSessionId]: nextEchoes } : current;
	});
}

function releaseConversationLocalEcho(
	queryClient: QueryClient,
	targetSessionId: string,
	clientMessageId?: string,
	turnId?: string,
): void {
	queryClient.setQueryData<ConversationLocalEchosBySession>(conversationLocalEchosQueryKey, (current = {}) => {
		const echoes = current[targetSessionId];
		if (!echoes) return current;
		const nextEchoes = echoes.filter(
			(echo) =>
				(clientMessageId === undefined || echo.clientMessageId !== clientMessageId) &&
				(turnId === undefined || echo.turnId !== turnId),
		);
		if (nextEchoes.length === echoes.length) return current;
		const next = { ...current };
		if (nextEchoes.length === 0) delete next[targetSessionId];
		else next[targetSessionId] = nextEchoes;
		return next;
	});
}

function claimConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
	operation: ConversationDispatchOperation,
	sourceTurnId?: string,
): boolean {
	const current =
		queryClient.getQueryData<ConversationDispatchTrackingBySession>(
			conversationDispatchTrackingQueryKey,
		) ?? {};
	if (current[targetSessionId]?.state === "pending") return false;
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		{
			...current,
			[targetSessionId]: { operation, requestId, sourceTurnId, state: "pending" },
		},
	);
	return true;
}

function acceptConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
	turnId: string,
): void {
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		(current = {}) => {
			const tracked = current[targetSessionId];
			if (tracked?.requestId !== requestId) return current;
			return {
				...current,
				[targetSessionId]: { ...tracked, state: "accepted", turnId },
			};
		},
	);
}

function releaseConversationDispatch(
	queryClient: QueryClient,
	targetSessionId: string,
	requestId: string,
): void {
	queryClient.setQueryData<ConversationDispatchTrackingBySession>(
		conversationDispatchTrackingQueryKey,
		(current = {}) => {
			if (current[targetSessionId]?.requestId !== requestId) return current;
			const next = { ...current };
			delete next[targetSessionId];
			return next;
		},
	);
}

export function conversationSkillsQueryKey(sessionId: string, hostId?: string) {
	return hostId ? (["remote-conversation-skills", hostId, sessionId] as const) : (["conversation-skills", sessionId] as const);
}

const conversationProviderCatalogEpochs = new WeakMap<QueryClient, Map<string, number>>();

function conversationProviderCatalogEpoch(queryClient: QueryClient, sessionId: string): number {
	return conversationProviderCatalogEpochs.get(queryClient)?.get(sessionId) ?? 0;
}

function advanceConversationProviderCatalogEpoch(queryClient: QueryClient, sessionId: string) {
	let epochs = conversationProviderCatalogEpochs.get(queryClient);
	if (!epochs) {
		epochs = new Map();
		conversationProviderCatalogEpochs.set(queryClient, epochs);
	}
	epochs.set(sessionId, conversationProviderCatalogEpoch(queryClient, sessionId) + 1);
}

export function clearConversationProviderCatalogs(queryClient: QueryClient, sessionId: string, hostId?: string) {
	advanceConversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId, hostId));
	queryClient.removeQueries({ queryKey: conversationModelsQueryKey(sessionId, hostId) });
	queryClient.removeQueries({ queryKey: conversationConfigOptionsQueryKey(sessionId, hostId) });
	queryClient.removeQueries({ queryKey: conversationSkillsQueryKey(sessionId, hostId) });
}

export function invalidateConversationProviderCatalogs(queryClient: QueryClient, sessionId: string, hostId?: string) {
	void queryClient.invalidateQueries({ queryKey: conversationModelsQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: conversationConfigOptionsQueryKey(sessionId, hostId) });
	void queryClient.invalidateQueries({ queryKey: conversationSkillsQueryKey(sessionId, hostId) });
}

const CONVERSATION_PAGE_SIZE = 200;
const REMOTE_LIVE_PAGE_SIZE = 30;
const REMOTE_OLDER_PAGE_SIZE = 100;
const REMOTE_OUTPUT_LIMIT = 512;
const remoteConversationRefreshErrorKey = (sessionId: string, hostId: string) => ["remote-conversation-refresh-error", hostId, sessionId] as const;
const CONFIG_OPTIONS_POLL_INTERVAL_MS = 5_000;
const CONFIG_OPTION_WRITE_TIMEOUT_MS = 60_000;
const SEND_ANSWER_TIMEOUT_MS = 60_000;
const SKILLS_POLL_INTERVAL_MS = 60_000;

const PERMANENT_CODES = new Set([
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_AUTH_REQUIRED",
]);

export interface ConversationQueryResult {
	snapshot?: ConversationSnapshot;
	isLoading: boolean;
	unavailable?: { code: string; message: string };
	error?: string;
	refreshError?: string;
	hasOlder: boolean;
	isLoadingOlder: boolean;
	loadOlder: () => void;
}

type ConversationPageQuery = { beforeSequence?: number; afterSequence?: number; limit: number; outputLimit?: number };

async function fetchConversationWire(sessionId: string, hostId: string | undefined, query: ConversationPageQuery): Promise<WireSnapshot> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/conversation", {
		params: { path: { sessionId }, query },
	});
	if (error) throw error;
	return data as WireSnapshot;
}

function conversationPageQuery(hostId: string | undefined, beforeSequence?: number): ConversationPageQuery {
	if (!hostId) return { beforeSequence, limit: CONVERSATION_PAGE_SIZE };
	return { beforeSequence, limit: beforeSequence ? REMOTE_OLDER_PAGE_SIZE : REMOTE_LIVE_PAGE_SIZE, outputLimit: REMOTE_OUTPUT_LIMIT };
}

async function fetchConversationPage(sessionId: string, hostId?: string, beforeSequence?: number): Promise<ConversationSnapshot> {
	return toSnapshot(await fetchConversationWire(sessionId, hostId, conversationPageQuery(hostId, beforeSequence)));
}

export async function fetchActivityOutput(sessionId: string, hostId: string | undefined, sequence: number, activityId: string): Promise<string | undefined> {
	const wire = await fetchConversationWire(sessionId, hostId, { beforeSequence: sequence + 1, afterSequence: sequence > 1 ? sequence - 1 : undefined, limit: 1 });
	const activity = wire.activities?.find((candidate) => candidate.id === activityId);
	return (activity?.detail as { output?: unknown } | undefined)?.output as string | undefined;
}

export function remoteConversationFloor(page: ConversationSnapshot): number {
	const live = new Set(page.turns.filter((turn) => turn.state === "queued" || turn.state === "running").map((turn) => turn.id));
	const newest = page.items.findLast((item) => item.turnId)?.turnId;
	if (newest) live.add(newest);
	let floor = page.latestSequence;
	for (const item of page.items) {
		const unsettled = item.kind === "activity" && (item.status === "running" || item.status === "pending");
		if (unsettled || (item.turnId && live.has(item.turnId))) floor = Math.min(floor, item.sequence - 1);
	}
	return floor;
}

export function mergeRemoteConversationTail(prior: ConversationSnapshot, tail: ConversationSnapshot, floor: number): ConversationSnapshot | undefined {
	if (tail.conversationId !== prior.conversationId || tail.activeBranchId !== prior.activeBranchId || tail.hasMoreBefore) return undefined;
	if (tail.compactedAt !== prior.compactedAt || tail.nativeForkAvailableAfterSequence !== prior.nativeForkAvailableAfterSequence) return undefined;
	const priorTurns = new Map(prior.turns.map((turn) => [turn.id, turn]));
	const turnsUnchanged = tail.turns.every((turn) => {
		const before = priorTurns.get(turn.id);
		return before?.state === turn.state && Boolean(before.rolledBack) === Boolean(turn.rolledBack);
	});
	if (!turnsUnchanged) return undefined;
	const priorIds = new Set(prior.items.map((item) => item.id));
	if (tail.items.some((item) => item.kind === "activity" && item.activityKind === "plan" && item.detail?.spansTurns && !priorIds.has(item.id))) return undefined;
	const kept = prior.items.filter((item) => item.sequence <= floor);
	const keptTurnIds = new Set(kept.map((item) => item.turnId));
	return mergeConversationPages([tail, { ...prior, items: kept, turns: prior.turns.filter((turn) => keptTurnIds.has(turn.id)) }]);
}

export async function refreshRemoteConversation(queryClient: QueryClient, sessionId: string, hostId: string): Promise<void> {
	const key = conversationQueryKey(sessionId, hostId);
	let latest: ConversationSnapshot;
	try {
		const prior = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(key)?.pages[0];
		const floor = prior ? remoteConversationFloor(prior) : 0;
		if (prior && floor >= 1) {
			const wire = await fetchConversationWire(sessionId, hostId, { afterSequence: floor, limit: REMOTE_LIVE_PAGE_SIZE, outputLimit: REMOTE_OUTPUT_LIMIT });
			const tail = toSnapshot(wire);
			const current = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(key);
			const merged = wire.afterSequence === floor && current?.pages.length ? mergeRemoteConversationTail(current.pages[0], tail, floor) : undefined;
			if (merged && current) {
				queryClient.setQueryData(remoteConversationRefreshErrorKey(sessionId, hostId), false);
				queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(key, { ...current, pages: [merged, ...current.pages.slice(1)] });
				return;
			}
			latest = wire.afterSequence ? await fetchConversationPage(sessionId, hostId) : tail;
		} else {
			latest = await fetchConversationPage(sessionId, hostId);
		}
	} catch (error) {
		if (baseUrlForHost(hostId) && !HOST_UNREACHABLE_CODES.has(apiErrorCode(error))) {
			queryClient.setQueryData(remoteConversationRefreshErrorKey(sessionId, hostId), true);
		}
		throw error;
	}
	queryClient.setQueryData(remoteConversationRefreshErrorKey(sessionId, hostId), false);
	queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(key, (previous) => {
		if (previous?.pages.length && previous.pages[0].conversationId === latest.conversationId && previous.pages[0].activeBranchId === latest.activeBranchId && latest.latestSequence < previous.pages[0].latestSequence) return previous;
		if (!previous?.pages.length || !latest.hasMoreBefore || previous.pages[0].conversationId !== latest.conversationId || previous.pages[0].activeBranchId !== latest.activeBranchId) return { pages: [latest], pageParams: [undefined] };
		const priorLive = previous.pages[0];
		if (latest.oldestSequence > priorLive.latestSequence + 1) return { pages: [latest], pageParams: [undefined] };
		const slidOut = latest.oldestSequence > priorLive.oldestSequence
			? priorLive.items.filter((item) => item.sequence < latest.oldestSequence)
			: [];
		const slidTurnIds = new Set(slidOut.map((item) => item.turnId));
		const live = slidOut.length
			? mergeConversationPages([latest, { ...priorLive, items: slidOut, turns: priorLive.turns.filter((turn) => slidTurnIds.has(turn.id)) }]) ?? latest
			: latest;
		return { ...previous, pages: [live, ...previous.pages.slice(1)] };
	});
}

const remoteConversationRefreshes = new WeakMap<QueryClient, Map<string, { dirty: boolean }>>();

export function refreshRemoteConversationOnce(queryClient: QueryClient, sessionId: string, hostId: string): void {
	let running = remoteConversationRefreshes.get(queryClient);
	if (!running) remoteConversationRefreshes.set(queryClient, (running = new Map()));
	const key = `${hostId}\0${sessionId}`;
	const inFlight = running.get(key);
	if (inFlight) {
		inFlight.dirty = true;
		return;
	}
	const state = { dirty: false };
	running.set(key, state);
	void (async () => {
		do {
			state.dirty = false;
			try { await refreshRemoteConversation(queryClient, sessionId, hostId); }
			catch { }
		} while (state.dirty);
		running.delete(key);
	})();
}

export function conversationSettled(queryClient: QueryClient, sessionId: string, hostId?: string): boolean {
	const state = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(sessionId, hostId))?.pages[0]?.controller.state;
	return state === "ready" || state === "busy";
}

export function conversationQueryOptions(sessionId: string, hostId?: string) {
	return infiniteQueryOptions({
		queryKey: conversationQueryKey(sessionId, hostId),
		initialPageParam: undefined as number | undefined,
		queryFn: ({ pageParam }) => fetchConversationPage(sessionId, hostId, pageParam),
		getNextPageParam: (page) => (page.hasMoreBefore ? page.oldestSequence : undefined),
		staleTime: Infinity,
		...(hostId ? { refetchOnMount: false } : {}),
		gcTime: 30 * 60_000,
		retry: (attempt, error) => {
			const code = apiErrorCode(error);
			if (code && PERMANENT_CODES.has(code)) return false;
			return attempt < 2;
		},
	});
}

function selectConversationPages(data: InfiniteData<ConversationSnapshot>) {
	return mergeConversationPages(data.pages);
}

export function useConversation(sessionId: string | undefined, hostId?: string): ConversationQueryResult {
	const queryClient = useQueryClient();
	const refreshError = useQuery({
		queryKey: remoteConversationRefreshErrorKey(sessionId ?? "", hostId ?? ""),
		queryFn: async () => false,
		initialData: false,
		enabled: false,
	}).data;
	const query = useInfiniteQuery({
		...conversationQueryOptions(sessionId ?? "", hostId),
		enabled: Boolean(sessionId),
		select: selectConversationPages,
	});
	useEffect(() => {
		if (hostId && sessionId && queryClient.getQueryData(conversationQueryKey(sessionId, hostId))) {
			refreshRemoteConversationOnce(queryClient, sessionId, hostId);
		}
	}, [hostId, queryClient, sessionId]);

	if (query.error && (!hostId || !query.data)) {
		const code = apiErrorCode(query.error);
		if (code === "SESSION_MODE_MISMATCH" || code === "CHAT_CONTROLLER_NOT_READY") {
			return {
				isLoading: false,
				unavailable: { code, message: apiErrorMessage(query.error) },
				hasOlder: false,
				isLoadingOlder: false,
				loadOlder: () => {},
			};
		}
		return {
			isLoading: false,
			error: apiErrorMessage(query.error),
			hasOlder: false,
			isLoadingOlder: false,
			loadOlder: () => {},
		};
	}

	return {
		snapshot: query.data,
		refreshError: hostId && (refreshError || query.error) ? "Could not load this remote conversation." : undefined,
		isLoading: query.isLoading,
		hasOlder: query.hasNextPage,
		isLoadingOlder: query.isFetchingNextPage,
		loadOlder: () => {
			void query.fetchNextPage({ cancelRefetch: false });
		},
	};
}

async function refreshAfterProviderSwitch(queryClient: QueryClient, sessionId: string, hostId?: string) {
	clearConversationProviderCatalogs(queryClient, sessionId, hostId);
	await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
	await queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
	invalidateConversationProviderCatalogs(queryClient, sessionId, hostId);
}

export function useProviderSwitchCommand(sessionId: string, hostId?: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (request: Omit<components["schemas"]["ProviderSwitchRequest"], "idempotencyKey">) => {
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/provider-switch", {
				params: { path: { sessionId } },
				body: { ...request, idempotencyKey: crypto.randomUUID() },
			});
			if (error) throw switchOutcomeUnknown(response.status, error) ? error : new ProviderSwitchFailed(error);
			return data;
		},
		onSuccess: () => refreshAfterProviderSwitch(queryClient, sessionId, hostId),
		onError: (error) => {
			if (!(error instanceof ProviderSwitchFailed)) void refreshAfterProviderSwitch(queryClient, sessionId, hostId).catch(() => {});
		},
	});
}

export function useConversationCommands(sessionId: string | undefined, hostId?: string, terminated = false) {
	const queryClient = useQueryClient();
	const restoreSession = useRestoreSession();
	const stateSessionId = sessionId ? sessionUiKey(sessionId, hostId) : undefined;
	const stateKey = (targetSessionId: string) => sessionUiKey(targetSessionId, hostId);
	const trackedDispatches = useQuery({
		queryKey: conversationDispatchTrackingQueryKey,
		queryFn: async (): Promise<ConversationDispatchTrackingBySession> => ({}),
		initialData: {} as ConversationDispatchTrackingBySession,
		enabled: false,
		gcTime: Number.POSITIVE_INFINITY,
		staleTime: Number.POSITIVE_INFINITY,
	}).data;
	const localEchosBySession = useQuery({
		queryKey: conversationLocalEchosQueryKey,
		queryFn: async (): Promise<ConversationLocalEchosBySession> => ({}),
		initialData: {} as ConversationLocalEchosBySession,
		enabled: false,
		gcTime: Number.POSITIVE_INFINITY,
		staleTime: Number.POSITIVE_INFINITY,
	}).data;
	const trackedDispatch = stateSessionId ? trackedDispatches[stateSessionId] : undefined;
	const invalidateSession = useCallback(
		async (targetSessionId: string) => {
			if (hostId) await refreshRemoteConversation(queryClient, targetSessionId, hostId);
			else await queryClient.invalidateQueries({ queryKey: conversationQueryKey(targetSessionId) });
		},
		[hostId, queryClient],
	);
	const invalidate = useCallback(() => {
		if (sessionId) {
			void invalidateSession(sessionId).catch(() => {});
		}
	}, [invalidateSession, sessionId]);
	const refreshSessionInBackground = useCallback(
		(targetSessionId: string) => {
			void invalidateSession(targetSessionId).catch(() => {});
		},
		[invalidateSession],
	);

	const send = useMutation({
		onMutate: (variables: ConversationSendMutationInput) => {
			const current = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(
				conversationQueryKey(variables.targetSessionId, hostId),
			);
			const backgroundWake = current?.pages.some((page) => page.controller.state === "hibernated") ?? false;
			const turnActive = current?.pages.some((page) =>
				page.turns.some((turn) => turn.state === "running" || turn.state === "queued"),
			) ?? false;
			addConversationLocalEcho(queryClient, stateKey(variables.targetSessionId), {
				clientMessageId: variables.clientMessageId,
				text: variables.input.text,
				continuation: variables.input.continuation,
				fileReferences: variables.input.fileReferences,
			excerpts: variables.input.excerpts?.map((excerpt) => ({
				id: excerpt.id, order: excerpt.order, snapshot: excerpt.snapshot,
					conversationId: excerpt.conversationId,
					text: excerpt.text,
					comment: excerpt.comment,
					messageId: excerpt.messageId,
					revision: excerpt.revision,
				})),
				createdAt: new Date().toISOString(),
				backgroundWake,
				queued: turnActive && !backgroundWake,
				providerSwitch: switchEcho(variables.input.switchDisplay, variables.input.providerSwitch?.targetHarness, newestCachedSequence(queryClient, variables.targetSessionId, hostId)),
			});
			queryClient.setQueryData<ConversationDispatchTrackingBySession>(
				conversationDispatchTrackingQueryKey,
				(current = {}) => {
					const tracked = current[stateKey(variables.targetSessionId)];
					if (tracked && tracked.requestId !== variables.clientMessageId) return current;
					return {
						...current,
						[stateKey(variables.targetSessionId)]: {
							operation: "send",
							requestId: variables.clientMessageId,
							state: "pending",
						},
					};
				},
			);
		},
		mutationFn: async ({
			targetSessionId,
			clientMessageId,
			input,
		}: ConversationSendMutationInput) => {
			const { providerSwitch, switchDisplay: _switchDisplay, ...message } = input;
			if (providerSwitch) {
				const refresh = () => void refreshAfterProviderSwitch(queryClient, targetSessionId, hostId).catch(() => {});
				const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/provider-switch", {
					params: { path: { sessionId: targetSessionId } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { ...providerSwitch, idempotencyKey: crypto.randomUUID(), message: { ...message, clientMessageId } },
				}).catch((failure: unknown) => {
					refresh();
					throw failure;
				});
				const switched = !error || response.headers.get("X-AO-Provider-Switched") === "1";
				const refused = Boolean(error) && !switched && !switchOutcomeUnknown(response.status, error);
				if (!refused) refresh();
				if (error) throw refused ? new ProviderSwitchFailed(error) : error;
				const accepted: components["schemas"]["SendConversationMessageResponse"] = data.message ?? { duplicate: true };
				return accepted;
			}
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/messages",
				{
					params: { path: { sessionId: targetSessionId } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { ...message, clientMessageId },
					signal: input.attachments?.length ? undefined : AbortSignal.timeout(SEND_ANSWER_TIMEOUT_MS),
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			const acceptedTurnId = data?.turnId;
			if (acceptedTurnId) {
				acceptConversationLocalEcho(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
					acceptedTurnId,
				);
				if (data.state === "queued") {
					releaseConversationDispatch(
						queryClient,
						stateKey(variables.targetSessionId),
						variables.clientMessageId,
					);
				} else {
					acceptConversationDispatch(
						queryClient,
						stateKey(variables.targetSessionId),
						variables.clientMessageId,
						acceptedTurnId,
					);
				}
			} else {
				releaseConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
				);
				releaseConversationLocalEcho(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.clientMessageId,
				);
			}
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(
				queryClient,
				stateKey(variables.targetSessionId),
				variables.clientMessageId,
			);
			releaseConversationLocalEcho(
				queryClient,
				stateKey(variables.targetSessionId),
				variables.clientMessageId,
			);
		},
	});

	const resolve = useMutation({
		mutationFn: async (input: { requestId: string; decisionId: string }) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve",
				{
					params: {
						path: {
							sessionId: sessionId as string,
							requestId: input.requestId,
						},
					},
					body: { decisionId: input.decisionId },
				},
			);
			if (error && !(hostId && apiErrorCode(error) === "CHAT_REQUEST_NOT_PENDING")) throw error;
		},
		onSettled: hostId ? invalidate : undefined,
		onSuccess: hostId ? undefined : invalidate,
	});

	const resolveInput = useMutation({
		mutationFn: async (input: {
			requestId: string;
			action: "accept" | "decline" | "cancel";
			content?: Record<string, unknown>;
		}) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve",
				{
					params: {
						path: {
							sessionId: sessionId as string,
							requestId: input.requestId,
						},
					},
					body: { action: input.action, content: input.content },
				},
			);
			if (error && !(hostId && apiErrorCode(error) === "CHAT_REQUEST_NOT_PENDING")) throw error;
		},
		onSettled: hostId ? invalidate : undefined,
		onSuccess: hostId ? undefined : invalidate,
	});

	const interrupt = useMutation({
		mutationFn: async ({ targetSessionId }: ConversationSessionMutationInput) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/interrupt",
				{ params: { path: { sessionId: targetSessionId } } },
			);
			if (error) throw error;
		},
		onSuccess: (_data, variables) => refreshSessionInBackground(variables.targetSessionId),
		onError: (_error, variables) => refreshSessionInBackground(variables.targetSessionId),
	});

	const resumingAgent = useIsMutating({ mutationKey: ["resume-agent", hostId ?? "local", sessionId] }) > 0;
	const resume = useMutation({
		mutationKey: ["resume-agent", hostId ?? "local", sessionId],
		mutationFn: async () => {
			if (terminated) {
				const restored = await restoreSession(sessionId as string, hostId);
				if (restored.status !== "success") throw new Error(restored.message);
				await invalidateSession(sessionId as string).catch(() => {});
				return undefined;
			}
			const { data, error, response } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/resume-agent",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error)
				throw resumeAgentError(error, response.status);
			return data;
		},
		onSuccess: () => {
			invalidate();
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
		onError: () => {
			invalidate();
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
	});

	const compact = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/compact",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const settingsWrites = useRef({ queue: Promise.resolve() as Promise<unknown>, latest: 0 });
	const setSnapshotSettings = (targetSessionId: string, settings: TurnSettings) =>
		queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(targetSessionId, hostId), (current) =>
			current ? {
				...current,
				pages: current.pages.map((page, index) => index === 0 ? { ...page, settings } : page),
			} : current,
		);
	const chooseSettings = useMutation({
		onMutate: async ({ targetSessionId, settings }: { targetSessionId: string; settings: TurnSettings }) => {
			await queryClient.cancelQueries({ queryKey: conversationQueryKey(targetSessionId, hostId) });
			setSnapshotSettings(targetSessionId, settings);
			return { write: ++settingsWrites.current.latest };
		},
		mutationFn: async ({ targetSessionId, settings }: { targetSessionId: string; settings: TurnSettings }) => {
			const write = settingsWrites.current.queue.then(async () => {
				const { data, error } = await clientForSessionHost(hostId).PATCH(
					"/api/v1/sessions/{sessionId}/conversation/settings",
					{
						params: { path: { sessionId: targetSessionId } },
						body: settings,
						signal: AbortSignal.timeout(CONFIG_OPTION_WRITE_TIMEOUT_MS),
					},
				);
				if (error) throw error;
				return data;
			});
			settingsWrites.current.queue = write.catch(() => undefined);
			return write;
		},
		onError: (_error, { targetSessionId }, context) => {
			if (context?.write === settingsWrites.current.latest) refreshSessionInBackground(targetSessionId);
		},
		onSuccess: async (settings, { targetSessionId }, context) => {
			if (context?.write !== settingsWrites.current.latest) return;
			const queryKey = conversationQueryKey(targetSessionId, hostId);
			await queryClient.cancelQueries({ queryKey });
			if (settings) setSnapshotSettings(targetSessionId, settings as TurnSettings);
			refreshSessionInBackground(targetSessionId);
		},
	});

	const steer = useMutation({
		mutationFn: async (input: { text: string; attachments?: WireImageContent[]; clientMessageId?: string; recoverOnly?: boolean }) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/steer",
				{
					params: { path: { sessionId: sessionId as string } },
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { ...input, clientMessageId: input.clientMessageId ?? crypto.randomUUID() },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const promoteQueuedTurn = useMutation({
		mutationFn: async (turnId: string) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/steer",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const cancelQueuedTurn = useMutation({
		mutationFn: async (turnId: string) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/cancel",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
				},
			);
			if (error) throw error;
		},
		onSuccess: invalidate,
	});

	const editQueuedTurn = useMutation({
		mutationFn: async ({ turnId, text, ...options }: { turnId: string; text: string } & QueuedMessageEditOptions) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
					headers: options.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: { text, ...options },
				},
			);
			if (error) throw error;
		},
		onSuccess: invalidate,
	});

	const reorderQueuedTurns = useMutation({
		mutationFn: async (turnIds: string[]) => {
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/queue/reorder",
				{
					params: {
						path: { sessionId: sessionId as string },
					},
					body: { turnIds },
				},
			);
			if (error) throw apiErrorException(error, "Could not reorder queued messages");
		},
		onMutate: async (turnIds) => {
			if (!sessionId) return;
			const queryKey = conversationQueryKey(sessionId, hostId);
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(queryKey);
			if (previous) {
				queryClient.setQueryData<InfiniteData<ConversationSnapshot>>(
					queryKey,
					applyQueuedTurnOrderToPages(previous, turnIds),
				);
			}
			return { previous };
		},
		onError: (_error, _turnIds, context) => {
			if (!sessionId || !context?.previous) return;
			queryClient.setQueryData(conversationQueryKey(sessionId, hostId), context.previous);
		},
		onSettled: () => {
			invalidate();
		},
	});

	const rollback = useMutation({
		mutationFn: async ({ turnId, filePlanId }: { turnId: string; filePlanId?: string }) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback",
				{
					params: {
						path: { sessionId: sessionId as string, turnId },
					},
					body: filePlanId ? { restoreFiles: true, planId: filePlanId } : { restoreFiles: false },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: invalidate,
	});

	const retryTurn = useMutation({
		mutationFn: async ({ targetSessionId, sourceTurnId }: ConversationRetryMutationInput) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/retry",
				{
					params: {
						path: { sessionId: targetSessionId, turnId: sourceTurnId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			if (data?.turnId) {
				acceptConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.requestId,
					data.turnId,
				);
			} else {
				releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
			}
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
		},
	});

	const autoResume = useMutation({
		mutationFn: async ({ targetSessionId, sourceTurnId, enabled }: ConversationSessionMutationInput & { sourceTurnId: string; enabled: boolean }) => {
			const { error } = await clientForSessionHost(hostId).PATCH(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume",
				{
					params: { path: { sessionId: targetSessionId, turnId: sourceTurnId } },
					body: { enabled },
				},
			);
			if (error) throw error;
		},
		onSuccess: (_data, variables) => refreshSessionInBackground(variables.targetSessionId),
	});

	const editMessage = useMutation({
		mutationFn: async ({
			targetSessionId,
			sourceTurnId,
			requestId,
			text,
		}: ConversationEditMutationInput) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/edit",
				{
					params: {
						path: { sessionId: targetSessionId, turnId: sourceTurnId },
					},
					body: { text, clientMessageId: requestId },
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (data, variables) => {
			if (data?.turnId) {
				acceptConversationDispatch(
					queryClient,
					stateKey(variables.targetSessionId),
					variables.requestId,
					data.turnId,
				);
			} else {
				releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
			}
			void refreshSessionInBackground(variables.targetSessionId);
		},
		onError: (_error, variables) => {
			releaseConversationDispatch(queryClient, stateKey(variables.targetSessionId), variables.requestId);
		},
	});

	const activateBranch = useMutation({
		mutationFn: async (branchId: string) => {
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/branches/{branchId}/activate",
				{
					params: {
						path: { sessionId: sessionId as string, branchId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSettled: invalidate,
	});
	const acknowledgeAcceptedTurn = useCallback(
		(turnId: string) => {
			if (!sessionId) return;
			queryClient.setQueryData<ConversationDispatchTrackingBySession>(
				conversationDispatchTrackingQueryKey,
				(current = {}) => {
					const tracked = current[stateSessionId as string];
					if (tracked?.state !== "accepted" || tracked.turnId !== turnId) return current;
					const next = { ...current };
					delete next[stateSessionId as string];
					return next;
				},
			);
		},
		[queryClient, sessionId, stateSessionId],
	);
	const acknowledgeLocalEcho = useCallback(
		(turnId: string | undefined, clientMessageId?: string) => {
			if (!sessionId || (!turnId && !clientMessageId)) return;
			releaseConversationLocalEcho(queryClient, stateSessionId as string, turnId ? undefined : clientMessageId, turnId);
		},
		[queryClient, sessionId, stateSessionId],
	);
	const sendTargetsCurrentSession = send.variables?.targetSessionId === sessionId;
	const sendCode = apiErrorCode(send.error);
	const sendError = sendTargetsCurrentSession && !(send.error instanceof ProviderSwitchFailed) && sendCode !== undefined && !HOST_UNREACHABLE_CODES.has(sendCode) ? send.error : undefined;
	const interruptTargetsCurrentSession = interrupt.variables?.targetSessionId === sessionId;
	const retryTargetsCurrentSession = retryTurn.variables?.targetSessionId === sessionId;
	const autoResumeTargetsCurrentSession = autoResume.variables?.targetSessionId === sessionId;
	const editTargetsCurrentSession = editMessage.variables?.targetSessionId === sessionId;

	return {
		send: (input: string | ConversationSendInput) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			const clientMessageId = (typeof input === "string" ? undefined : input.clientMessageId) ?? crypto.randomUUID();
			const normalizedInput: ConversationSendInput = typeof input === "string"
				? { text: input }
				: {
					...input,
					text: input.text.trim() || (input.excerpts?.length
						? `Use the attached ${input.excerpts.length} chat excerpt(s) as context`
						: input.text),
				};
			if (!claimConversationDispatch(queryClient, stateSessionId as string, clientMessageId, "send")) {
				return Promise.reject(new Error("Conversation work is already being sent for this session."));
			}
			recordDirectWorkerInteraction(sessionId, "chat", "worker", hostId);
			return send.mutateAsync({
				targetSessionId: sessionId,
				clientMessageId,
				input: normalizedInput,
			});
		},
		pendingAcceptedTurnId:
			trackedDispatch?.state === "accepted" ? trackedDispatch.turnId : undefined,
		acknowledgeAcceptedTurn,
		localEchos: stateSessionId ? localEchosBySession[stateSessionId] ?? [] : [],
		acknowledgeLocalEcho,
		echoBeforeSend: (
			echo: Pick<ConversationLocalEcho, "clientMessageId" | "text" | "excerpts">,
			providerSwitch?: { targetHarness: string; display: ConversationSwitchDisplay },
		) => {
			if (!stateSessionId || !sessionId) return;
			addConversationLocalEcho(queryClient, stateSessionId, {
				...echo,
				createdAt: new Date().toISOString(),
				providerSwitch: switchEcho(providerSwitch?.display, providerSwitch?.targetHarness, newestCachedSequence(queryClient, sessionId, hostId)),
			});
		},
		dropEcho: (clientMessageId: string) => {
			if (stateSessionId) releaseConversationLocalEcho(queryClient, stateSessionId, clientMessageId);
		},
		resolve: (requestId: string, decisionId: string) => resolve.mutate({ requestId, decisionId }),
		resolveInput: (
			requestId: string,
			action: "accept" | "decline" | "cancel",
			content?: Record<string, unknown>,
		) => resolveInput.mutateAsync({ requestId, action, content }),
		interrupt: () => interrupt.mutate({ targetSessionId: sessionId as string }),
		resumeAgent: () => resume.mutateAsync(),
		resumingAgent,
		resumeError: resume.error ? apiErrorMessage(resume.error) : undefined,
		resumeOpenElsewhere: resumeOpenElsewhere(resume.error),
		compact: () => compact.mutateAsync(),
		choosingSettings: chooseSettings.isPending && chooseSettings.variables?.targetSessionId === sessionId,
		chooseSettings: (settings: TurnSettings) => chooseSettings.mutate({ targetSessionId: sessionId as string, settings }),
		chooseStartSettings: async (settings: TurnSettings) => {
			await chooseSettings.mutateAsync({ targetSessionId: sessionId as string, settings });
			const { error } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/exit-agent",
				{ params: { path: { sessionId: sessionId as string } } },
			);
			if (error) throw apiErrorException(error);
			await resume.mutateAsync();
		},
		compacting: compact.isPending,
		compactUnavailable:
			apiErrorCode(compact.error) === "CHAT_COMPACTION_UNSUPPORTED"
				? "This agent cannot compact its history"
				: apiErrorCode(compact.error) === "CHAT_COMPACTION_BUSY"
					? "Stop the current turn before compacting"
					: undefined,
		rollback: (turnId: string, filePlanId?: string) => rollback.mutateAsync({ turnId, filePlanId }),
		previewRollback: async (turnId: string) => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback",
				{
					params: { path: { sessionId: sessionId as string, turnId } },
					signal: AbortSignal.timeout(10_000),
				},
			);
			if (error) throw error;
			return data;
		},
		rollbackPending: rollback.isPending,
		rollbackError:
			apiErrorCode(rollback.error) === "CHAT_ROLLBACK_FILES_CHANGED"
				? "Files changed while this was open. Check the updated list, then roll back again."
				: rollback.error
					? apiErrorMessage(rollback.error)
					: undefined,
		retryControl: {
			setAutoResume: (turnId: string, enabled: boolean) => {
				if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
				return autoResume.mutateAsync({ targetSessionId: sessionId, sourceTurnId: turnId, enabled });
			},
			autoResumePending: autoResume.isPending && autoResumeTargetsCurrentSession,
			autoResumeError: autoResumeTargetsCurrentSession && autoResume.error
				? apiErrorMessage(autoResume.error) : undefined,
			autoResumeTurnId: autoResumeTargetsCurrentSession ? autoResume.variables?.sourceTurnId : undefined,
			retry: (turnId: string) => {
				if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
				const requestId = crypto.randomUUID();
				if (!claimConversationDispatch(queryClient, stateSessionId as string, requestId, "retry", turnId)) {
					return Promise.reject(new Error("Conversation work is already being sent for this session."));
				}
				return retryTurn.mutateAsync({
					requestId,
					sourceTurnId: turnId,
					targetSessionId: sessionId,
				});
			},
			pending:
				trackedDispatch?.operation === "retry" ||
				(retryTurn.isPending && retryTargetsCurrentSession),
			error:
				retryTargetsCurrentSession && retryTurn.error
					? apiErrorMessage(retryTurn.error)
					: undefined,
			turnId:
				trackedDispatch?.operation === "retry"
					? trackedDispatch.sourceTurnId
					: retryTargetsCurrentSession
						? retryTurn.variables?.sourceTurnId
						: undefined,
		},
		editMessage: async (turnId: string, text: string, clientMessageId?: string): Promise<ChatEditOutcome> => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			const requestId = clientMessageId ?? crypto.randomUUID();
			if (!claimConversationDispatch(queryClient, stateSessionId as string, requestId, "edit", turnId)) {
				return Promise.reject(new Error("Conversation work is already being sent for this session."));
			}
			try {
			await editMessage.mutateAsync({
				requestId,
				sourceTurnId: turnId,
				targetSessionId: sessionId,
				text,
			});
			return { status: "accepted" };
			} catch (error) {
				const outcome = editNonAcceptance(error);
				if (outcome) return outcome;
				throw error;
			}
		},
		editMessagePending:
			trackedDispatch?.operation === "edit" ||
			(editMessage.isPending && editTargetsCurrentSession),
		editMessageError:
			editTargetsCurrentSession && editMessage.error
				? apiErrorMessage(editMessage.error)
				: undefined,
		activateBranch: (branchId: string) => activateBranch.mutateAsync(branchId),
		activateBranchPending: activateBranch.isPending,
		activateBranchError: activateBranch.error ? apiErrorMessage(activateBranch.error) : undefined,
		steer: async (text: string, attachments?: WireImageContent[], clientMessageId?: string, recoverOnly?: boolean): Promise<ChatSteerOutcome> => {
			try {
				if (sessionId) recordDirectWorkerInteraction(sessionId, "chat", "worker", hostId);
				await steer.mutateAsync({ text, attachments, clientMessageId, recoverOnly });
				return { status: "accepted" };
			} catch (error) {
				const outcome = steerNonAcceptance(error);
				if (outcome) return outcome;
				throw error;
			}
		},
		promoteQueuedTurn: (turnId: string) => promoteQueuedTurn.mutateAsync(turnId),
		cancelQueuedTurn: (turnId: string) => cancelQueuedTurn.mutateAsync(turnId),
		editQueuedTurn: (turnId: string, text: string, options?: QueuedMessageEditOptions) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			return editQueuedTurn.mutateAsync({ turnId, text, ...options });
		},
		reorderQueuedTurns: (turnIds: string[]) => {
			if (!sessionId) return Promise.reject(new Error("No conversation session is selected."));
			return reorderQueuedTurns.mutateAsync(turnIds);
		},
		promoteQueuedTurnPendingTurnId: promoteQueuedTurn.isPending
			? promoteQueuedTurn.variables
			: undefined,
		cancelQueuedTurnPendingTurnId: cancelQueuedTurn.isPending ? cancelQueuedTurn.variables : undefined,
		editQueuedTurnPendingTurnId: editQueuedTurn.isPending ? editQueuedTurn.variables?.turnId : undefined,
		sendPending: send.isPending && sendTargetsCurrentSession,
		steerPending: steer.isPending,
		steerRefusal: steerRefusal(steer.error),
		steerUnsupported: apiErrorCode(steer.error) === "CHAT_STEER_UNSUPPORTED",
		busy:
			trackedDispatch?.state === "pending" ||
			(send.isPending && sendTargetsCurrentSession) ||
			resolve.isPending ||
			resolveInput.isPending ||
			(interrupt.isPending && interruptTargetsCurrentSession),
		error:
			sendError ||
			resolve.error ||
			(interruptTargetsCurrentSession && interrupt.error) ||
			chooseSettings.error
				? apiErrorMessage(
							sendError ??
							resolve.error ??
							(interruptTargetsCurrentSession ? interrupt.error : undefined) ??
							chooseSettings.error,
					)
				: undefined,
	};
}

function steerRefusal(error: unknown): string | undefined {
	const code = apiErrorCode(error);
	if (!code) return undefined;
	switch (code) {
		case "CHAT_NO_ACTIVE_TURN":
			return "The turn finished before this landed. Send it as a message instead.";
		case "CHAT_INTERFACE_TRANSITION":
			return "The session is switching interfaces. This guidance was not delivered; send it after the switch finishes.";
		case "CHAT_TURN_NOT_STEERABLE": {
			const message = apiErrorMessage(error).trim();
			return `${/[.!?]$/.test(message) ? message : `${message}.`} Try again once it finishes.`;
		}
		case "CHAT_STEER_UNSUPPORTED":
			return undefined;
		case "CHAT_STEER_TEXT_REQUIRED":
			return "Type something to steer with.";
		default:
			return apiErrorMessage(error);
	}
}

const DEFINITIVE_STEER_NON_ACCEPTANCE_CODES = new Set([
	"CHAT_NO_ACTIVE_TURN",
	"CHAT_INTERFACE_TRANSITION",
	"CHAT_TURN_NOT_STEERABLE",
	"CHAT_STEER_UNSUPPORTED",
	"CHAT_STEER_TEXT_REQUIRED",
	"CHAT_UNSUPPORTED_STEER_CONTENT",
	"CHAT_PROVIDER_REFUSED",
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_AUTH_REQUIRED",
]);

function steerNonAcceptance(error: unknown): ChatSteerOutcome | undefined {
	const code = apiErrorCode(error);
	if (!code || !DEFINITIVE_STEER_NON_ACCEPTANCE_CODES.has(code)) return undefined;
	return {
		status: "not-accepted",
		reason: steerRefusal(error) ?? apiErrorMessage(error),
	};
}

const DEFINITIVE_EDIT_NON_ACCEPTANCE_CODES = new Set([
	"CHAT_EDIT_REJECTED",
	"CHAT_EDIT_UNSUPPORTED",
	"CHAT_EDIT_BUSY",
	"CHAT_EDIT_TURN_INVALID",
	"CHAT_PROVIDER_REFUSED",
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_AUTH_REQUIRED",
]);

function editNonAcceptance(error: unknown): ChatEditOutcome | undefined {
	const code = apiErrorCode(error);
	if (!code || !DEFINITIVE_EDIT_NON_ACCEPTANCE_CODES.has(code)) return undefined;
	return { status: "not-accepted", reason: apiErrorMessage(error) };
}

export function useConversationModels(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const query = useQuery({
		queryKey: conversationModelsQueryKey(sessionId ?? "", hostId),
		enabled: Boolean(sessionId) && enabled,
		staleTime: 5 * 60 * 1000,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/models",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return (data?.models ?? []) as ChatModel[];
		},
	});
	return {
		models: query.data ?? [],
		isLoading: query.isLoading,
		loaded: query.isFetched,
	};
}

function offersValue(options: ChatConfigOption[], optionId: string, value: ChatConfigOptionValue): boolean {
	const option = options.find((candidate) => candidate.id === optionId);
	if (!option) return false;
	return !("value" in value) || option.type !== "select" || option.choices.some((choice) => choice.value === value.value);
}

export function useConversationConfigOptions(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const queryClient = useQueryClient();
	const queryKey = conversationConfigOptionsQueryKey(sessionId ?? "", hostId);
	const [writing, setWriting] = useState(false);
	const writes = useRef({
		queue: Promise.resolve() as Promise<unknown>,
		latest: 0,
		open: 0,
		pendingByOption: new Map<string, number>(),
		confirmed: undefined as ChatConfigOption[] | undefined,
	});
	const snapshotRefreshed = useRef(false);
	const readEnabled = Boolean(sessionId) && enabled;
	useEffect(() => {
		snapshotRefreshed.current = false;
	}, [readEnabled]);
	const query = useQuery({
		queryKey,
		enabled: readEnabled,
		retry: false,
		refetchInterval: writing ? false : CONFIG_OPTIONS_POLL_INTERVAL_MS,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/config-options",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) {
				if (apiErrorCode(error) === "CHAT_CONTROLLER_NOT_READY" && !snapshotRefreshed.current) {
					snapshotRefreshed.current = true;
					if (hostId) refreshRemoteConversationOnce(queryClient, sessionId as string, hostId);
					else void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId as string) });
				}
				throw error;
			}
			snapshotRefreshed.current = false;
			return (data?.options ?? []) as ChatConfigOption[];
		},
	});
	const mutation = useMutation({
		onMutate: async ({ optionId, value }) => {
			writes.current.open += 1;
			writes.current.pendingByOption.set(optionId, (writes.current.pendingByOption.get(optionId) ?? 0) + 1);
			setWriting(true);
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<ChatConfigOption[]>(queryKey);
			queryClient.setQueryData<ChatConfigOption[]>(queryKey, previous?.map((option) => option.id !== optionId ? option
				: "value" in value ? { ...option, currentValue: value.value } : { ...option, currentBoolean: value.enabled }));
			return { previous, write: ++writes.current.latest, controllerEpoch: conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId)) };
		},
		onError: (_error, { optionId }, context) => {
			if (!context?.previous || context.controllerEpoch !== conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId))) return;
			if (context.write === writes.current.latest) {
				queryClient.setQueryData(queryKey, writes.current.confirmed ?? context.previous);
				return;
			}
			if ((writes.current.pendingByOption.get(optionId) ?? 0) > 1) return;
			const before = context.previous.find((option) => option.id === optionId);
			if (before) queryClient.setQueryData<ChatConfigOption[]>(queryKey, (current) => current?.map((option) => option.id === optionId ? before : option));
		},
		onSettled: (_data, _error, { optionId }) => {
			writes.current.open -= 1;
			const left = (writes.current.pendingByOption.get(optionId) ?? 1) - 1;
			if (left > 0) writes.current.pendingByOption.set(optionId, left);
			else writes.current.pendingByOption.delete(optionId);
			if (writes.current.open === 0) {
				writes.current.confirmed = undefined;
				setWriting(false);
			}
		},
		mutationFn: async ({
			optionId,
			value,
		}: {
			optionId: string;
			value: ChatConfigOptionValue;
		}) => {
			const write = writes.current.queue.then(() => writeConfigOption(optionId, value));
			writes.current.queue = write.catch(() => undefined);
			return write;
		},
		onSuccess: ({ controllerEpoch, options }, _variables, context) => {
			if (context?.write !== writes.current.latest) return;
			if (controllerEpoch !== conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId))) {
				return;
			}
			queryClient.setQueryData(queryKey, options);
		},
	});

	async function writeConfigOption(optionId: string, value: ChatConfigOptionValue) {
		const controllerEpoch = conversationProviderCatalogEpoch(queryClient, sessionUiKey(sessionId as string, hostId));
		const confirmed = writes.current.confirmed;
		if (confirmed && !offersValue(confirmed, optionId, value)) return { controllerEpoch, options: confirmed };
		await queryClient.cancelQueries({ queryKey });
		const { data, error } = await clientForSessionHost(hostId).PATCH(
			"/api/v1/sessions/{sessionId}/conversation/config-options/{configId}",
			{
				params: {
					path: {
						sessionId: sessionId as string,
						configId: optionId,
					},
				},
				body: value,
				signal: AbortSignal.timeout(CONFIG_OPTION_WRITE_TIMEOUT_MS),
			},
		);
		if (error) throw error;
		const options = (data?.options ?? []) as ChatConfigOption[];
		writes.current.confirmed = options;
		return { controllerEpoch, options };
	}

	const readError = query.error && !query.data && apiErrorCode(query.error) !== "CHAT_CONTROLLER_NOT_READY"
		? query.error
		: undefined;
	return {
		options: query.data ?? [],
		loaded: query.data !== undefined,
		setOption: async (optionId: string, value: ChatConfigOptionValue) =>
			(await mutation.mutateAsync({ optionId, value })).options,
		pending: mutation.isPending,
		error: mutation.error || readError ? apiErrorMessage(mutation.error ?? readError) : undefined,
	};
}

export function useConversationSkills(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const query = useQuery({
		queryKey: conversationSkillsQueryKey(sessionId ?? "", hostId),
		enabled: Boolean(sessionId) && enabled,
		staleTime: SKILLS_POLL_INTERVAL_MS,
		refetchInterval: (query) =>
			apiErrorCode(query.state.error) === "CHAT_CONTROLLER_NOT_READY"
				? false
				: SKILLS_POLL_INTERVAL_MS,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/skills",
				{
					params: { path: { sessionId: sessionId as string } },
				},
			);
			if (error) throw error;
			return (data?.skills ?? []) as ChatSkill[];
		},
	});
	return { skills: query.data ?? [], isLoading: query.isLoading };
}

export function useWorkspaceFilePaths(sessionId: string | undefined, enabled: boolean, hostId?: string) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: hostId ? ["remote-workspace-file-paths", hostId, sessionId ?? ""] : ["workspace-file-paths", sessionId ?? ""],
		enabled: Boolean(sessionId) && enabled,
		staleTime: 30 * 1000,
		refetchInterval: (query) => hostId && !isMissingWorkspaceError(query.state.error) ? 30 * 1000 : false,
		retry: false,
		queryFn: async () => {
			const missing = knownMissingWorkspace(sessionId as string, hostId);
			if (missing) throw missing;
			const epoch = workspaceRecoveryEpoch(sessionId as string, hostId);
			const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/workspace/files", {
				params: { path: { sessionId: sessionId as string } },
			});
			if (error) throw recordWorkspaceResult(sessionId as string, hostId, error, response, "Unable to load workspace files", epoch);
			return {
				paths: (data?.files ?? [])
					.filter((file) => file.status !== "deleted")
					.map((file) => file.path),
				truncated: Boolean(data?.truncated),
			};
		},
	});
	useEffect(() => {
		if (!sessionId || !enabled || hostId) return;
		return subscribeWorkspaceFileChanges(sessionId, queryClient);
	}, [enabled, hostId, queryClient, sessionId]);
	return {
		paths: query.data?.paths ?? [],
		truncated: query.data?.truncated ?? false,
		isLoading: query.isLoading,
	};
}

export function useStageAttachments(sessionId: string | undefined, hostId?: string) {
	return useCallback(
		async (attachments: { mimeType: string; data: string; name?: string }[]): Promise<string[]> => {
			if (!sessionId || attachments.length === 0) return [];
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/attachments", {
				params: { path: { sessionId } },
				body: { attachments: attachments.map(({ mimeType, data }) => ({ mimeType, data })) },
			});
			if (error) throw error;
			return data?.paths ?? [];
		},
		[hostId, sessionId],
	);
}

export function toSnapshot(wire: WireSnapshot): ConversationSnapshot {
	const items: ConversationItem[] = [
		...(wire.messages ?? []).map(toMessage),
		...(wire.activities ?? []).map(toActivity),
	].sort((a, b) => a.sequence - b.sequence);

	return {
		continueTurnId: wire.continueTurnId,
		conversationId: wire.conversationId,
		supportsFileReferences: wire.supportsFileReferences,
		sessionId: wire.sessionId,
		harness: wire.harness ?? "",
		mode: wire.mode as SessionMode,
		controller: { state: wire.controller as ControllerState },
		latestSequence: wire.latestSequence,
		oldestSequence: wire.oldestSequence ?? wire.latestSequence + 1,
		hasMoreBefore: wire.hasMoreBefore ?? false,
		nativeForkAvailableAfterSequence: wire.nativeForkAvailableAfterSequence ?? 0,
		settings: {
			model: wire.settings?.model || undefined,
			reasoningEffort: wire.settings?.reasoningEffort || undefined,
			approvalMode: (wire.settings?.approvalMode as ApprovalMode | undefined) || undefined,
			fastMode: wire.settings?.fastMode || undefined,
			planMode: wire.settings?.planMode || undefined,
			autoCompactWindow: wire.settings?.autoCompactWindow || undefined,
		},
		usage: wire.usage ? { ...wire.usage } : undefined,
		rateLimits: wire.rateLimits ? { ...wire.rateLimits } : undefined,
		compactedAt: wire.compactedAt ?? undefined,
		title: wire.title || undefined,
		modelReroute: wire.modelReroute
			? {
					fromModel: wire.modelReroute.fromModel || undefined,
					toModel: wire.modelReroute.toModel,
					reason: wire.modelReroute.reason || undefined,
					providerTurnId: wire.modelReroute.providerTurnId || undefined,
					at: wire.modelReroute.at,
				}
			: undefined,
		account: wire.account
			? {
					authMode: wire.account.authMode || undefined,
					planLabel: wire.account.planLabel || undefined,
					authenticationState: wire.account.authenticationState,
						authVerifiedAt: wire.account.authVerifiedAt ?? undefined,
						lastAuthFailureAt: wire.account.lastAuthFailureAt ?? undefined,
						lastAuthFailureReason: wire.account.lastAuthFailureReason || undefined,
						authFailureId: wire.account.authFailureId || undefined,
						reauthRequiredAt: wire.account.reauthRequiredAt ?? undefined,
					reauthReason: wire.account.reauthReason || undefined,
				}
			: undefined,
		threadState: wire.threadState
			? {
					status: (wire.threadState.status as ThreadStatus | undefined) || undefined,
					waitingOn: wire.threadState.waitingOn?.length ? wire.threadState.waitingOn : undefined,
					archivedAt: wire.threadState.archivedAt ?? undefined,
					closedAt: wire.threadState.closedAt ?? undefined,
				}
			: undefined,
		mcpServers: wire.mcpServers?.length
			? wire.mcpServers.map(
					(server): McpServer => ({
						name: server.name,
						status: server.status as McpServer["status"],
						error: server.error || undefined,
						failureReason: server.failureReason || undefined,
					}),
				)
			: undefined,
		capabilities: wire.capabilities?.length ? wire.capabilities : undefined,
		activeBranchId: wire.activeBranchId || undefined,
		branchedFromEarlierMessage: wire.branchedFromEarlierMessage ?? undefined,
		batchedDeliveries: wire.batchedDeliveries?.length
			? wire.batchedDeliveries.map((delivery) => ({
					turnId: delivery.turnId,
					deliveredWithTurnId: delivery.deliveredWithTurnId,
					steerActivityId: delivery.steerActivityId || undefined,
				}))
			: undefined,
		branchMaterialization: wire.branchMaterialization
			? {
					strategy: wire.branchMaterialization.strategy,
					replayTruncated: wire.branchMaterialization.replayTruncated,
				}
			: undefined,
		branchPoints: (wire.branchPoints ?? []).map((point) => ({
			turnId: point.turnId,
			position: point.position,
			total: point.total,
			previousBranchId: point.previousBranchId || undefined,
			nextBranchId: point.nextBranchId || undefined,
		})),
		turns: (wire.turns ?? []).map((turn) => ({
			id: turn.id,
			state: turn.state as TurnState,
			providerTurnId: turn.providerTurnId,
			retryOfTurnId: turn.retryOfTurnId,
			hasRetryAttempt: turn.hasRetryAttempt,
			hasHumanPrompt: turn.hasHumanPrompt,
			errorMessage: turn.errorMessage,
			failureCode: turn.failureCode,
			usageLimit: turn.usageLimit ? {
				resetsAt: turn.usageLimit.resetsAt ?? undefined,
				autoResume: turn.usageLimit.autoResume,
				resumeError: turn.usageLimit.resumeError,
			} : undefined,
			requestedAt: turn.requestedAt,
			startedAt: turn.startedAt ?? undefined,
			completedAt: turn.completedAt ?? undefined,
			diff: turn.diff
				? {
						files: (turn.diff.files ?? []).map((file) => ({
							path: file.path,
							additions: file.additions,
							deletions: file.deletions,
							status: file.status as DiffStatus,
							oldPath: file.oldPath || undefined,
						})),
						truncated: turn.diff.truncated,
					}
				: undefined,
			artifacts: turn.artifacts?.files?.length
				? turn.artifacts.files.map((file) => ({ path: file.path, status: file.status }))
				: undefined,
			plan: turn.plan
				? {
						explanation: turn.plan.explanation || undefined,
						steps: (turn.plan.steps ?? []).map(
							(step): PlanStep => ({
								text: step.text,
								status: step.status as PlanStepStatus,
							}),
						),
						spansTurns: turn.plan.spansTurns || undefined,
						superseded: turn.plan.superseded || undefined,
					}
				: undefined,
			rolledBack: turn.rolledBack ?? undefined,
		})),
		items,
	};
}

function applyQueuedTurnOrder(
	snapshot: ConversationSnapshot,
	fifoTurnIds: readonly string[],
): ConversationSnapshot {
	const queuedTurns = snapshot.turns.filter((turn) => turn.state === "queued");
	if (queuedTurns.length !== fifoTurnIds.length) return snapshot;

	const queuedById = new Map(queuedTurns.map((turn) => [turn.id, turn]));
	const requestedAts = [...queuedTurns]
		.sort((left, right) => left.requestedAt.localeCompare(right.requestedAt))
		.map((turn) => turn.requestedAt);
	const reorderedQueued = fifoTurnIds.flatMap((turnId, index) => {
		const turn = queuedById.get(turnId);
		return turn
			? [{ ...turn, requestedAt: requestedAts[index] ?? turn.requestedAt }]
			: [];
	});
	if (reorderedQueued.length !== queuedTurns.length) return snapshot;

	const queuedIds = new Set(fifoTurnIds);
	const otherTurns = snapshot.turns.filter((turn) => !queuedIds.has(turn.id));
	return {
		...snapshot,
		turns: [...otherTurns, ...reorderedQueued].sort((left, right) =>
			left.requestedAt.localeCompare(right.requestedAt),
		),
	};
}

function applyQueuedTurnOrderToPages(
	data: InfiniteData<ConversationSnapshot>,
	fifoTurnIds: readonly string[],
): InfiniteData<ConversationSnapshot> {
	if (data.pages.length === 0) return data;
	return {
		...data,
		pages: data.pages.map((page, index) =>
			index === 0 ? applyQueuedTurnOrder(page, fifoTurnIds) : page,
		),
	};
}

export function mergeConversationPages(pages: ConversationSnapshot[]): ConversationSnapshot | undefined {
	const live = pages[0];
	if (!live) return undefined;
	if (pages.length === 1) return live;

	const items = new Map<string, ConversationItem>();
	const turns = new Map<string, ConversationSnapshot["turns"][number]>();
	const deliveries = new Map<string, NonNullable<ConversationSnapshot["batchedDeliveries"]>[number]>();
	for (const page of [...pages].reverse()) {
		for (const item of page.items) items.set(`${item.kind}:${item.id}`, item);
		for (const turn of page.turns) turns.set(turn.id, turn);
		for (const delivery of page.batchedDeliveries ?? []) deliveries.set(delivery.turnId, delivery);
	}

	const oldest = pages[pages.length - 1] ?? live;
	return {
		...live,
		oldestSequence: oldest.oldestSequence,
		hasMoreBefore: oldest.hasMoreBefore,
		items: [...items.values()].sort((a, b) => a.sequence - b.sequence),
		turns: [...turns.values()].sort((a, b) => a.requestedAt.localeCompare(b.requestedAt)),
		batchedDeliveries: deliveries.size > 0 ? [...deliveries.values()] : undefined,
	};
}

function toMessage(wire: WireMessage): ConversationMessage {
	return {
		kind: "message",
		id: wire.id,
		turnId: wire.turnId,
		sequence: wire.sequence,
		revision: wire.revision,
		role: wire.role as MessageRole,
		origin: wire.origin as MessageOrigin,
		text: wire.text,
		content: (wire.content ?? []).map((item) => ({
			type: item.type,
			mimeType: item.mimeType || undefined,
			uri: item.uri || undefined,
			name: item.name || undefined,
			text: item.text || undefined,
			comment: item.comment || undefined,
			fileReference: item.fileReference as ChatFileReference | undefined, annotationId: item.annotationId, annotationOrder: item.annotationOrder,
			sourceConversationId: item.sourceConversationId || undefined,
			sourceMessageId: item.sourceMessageId || undefined,
			sourceRevision: item.sourceRevision ?? undefined, snapshot: item.snapshot,
		})),
		editAvailable: wire.editAvailable ?? undefined,
		continuation: wire.continuation || undefined,
		authoredByUser: wire.authoredByUser || undefined,
		streaming: wire.streaming,
		senderSessionId: wire.senderSessionId,
		senderProjectId: wire.senderProjectId,
		senderDisplayName: wire.senderDisplayName,
		clientMessageId: wire.clientMessageId,
		createdAt: wire.createdAt,
	};
}

function toActivity(wire: WireActivity): ConversationActivity {
	const detail = (wire.detail ?? {}) as Record<string, unknown>;
	return {
		kind: "activity",
		id: wire.id,
		turnId: wire.turnId,
		sequence: wire.sequence,
		revision: wire.revision,
		activityKind: wire.activityKind as ActivityKind,
		status: wire.status as ActivityStatus,
		summary: wire.summary,
		requestId: wire.requestId,
		providerItemId: wire.providerItemId,
		decisions: readDecisions(detail),
		inputResponse: wire.activityKind === "user_input" ? readInputResponse(detail) : undefined,
		detail: detail as ConversationActivity["detail"],
		createdAt: wire.createdAt,
	};
}

function readInputResponse(detail: Record<string, unknown>): ConversationActivity["inputResponse"] {
	const action = detail.action;
	if (action !== "accept" && action !== "decline" && action !== "cancel") return undefined;
	const content = detail.content;
	return {
		action,
		content: isInputContent(content) ? content : undefined,
	};
}

function isInputContent(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readDecisions(detail: Record<string, unknown>): DecisionOption[] | undefined {
	const raw = detail.decisions;
	if (!Array.isArray(raw)) return undefined;
	const options: DecisionOption[] = [];
	for (const entry of raw) {
		if (entry && typeof entry === "object" && "id" in entry) {
			const option = entry as {
				id?: unknown;
				label?: unknown;
				kind?: unknown;
			};
			if (typeof option.id === "string" && option.id !== "") {
				const kind = isDecisionKind(option.kind) ? option.kind : undefined;
				options.push({
					id: option.id,
					label: typeof option.label === "string" && option.label ? option.label : option.id,
					kind,
				});
			}
		}
	}
	return options.length > 0 ? options : undefined;
}

function isDecisionKind(value: unknown): value is NonNullable<DecisionOption["kind"]> {
	return (
		value === "allow_once" ||
		value === "allow_always" ||
		value === "reject_once" ||
		value === "reject_always"
	);
}
