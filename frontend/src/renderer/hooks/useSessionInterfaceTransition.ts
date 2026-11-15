import {
	type QueryClient,
	useMutation,
	useMutationState,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { interfaceTransitionPollDelay } from "../../../../packages/shared/session/interface-transition-poll";
import { apiErrorCode, apiErrorMessage, hasTrustedApiBaseUrl } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { CloudCpAuthError, type CloudCpInterfaceTransition } from "../lib/cloud-cp";
import { conversationQueryKey } from "./useConversation";
import { useCloudCp } from "./useCloudCp";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";

export type SessionInterfaceTransition = components["schemas"]["SessionInterfaceTransition"];
export type SessionInterfaceTransitionStatus =
	components["schemas"]["SessionInterfaceTransitionStatusResponse"];
export type SessionInterfaceTransitionPolicy = "drain" | "interrupt";
export type SessionInterfaceTransitionHistoryPolicy = "strict" | "provider_history";
export type SessionInterfaceMode = "chat" | "tui";

type StartInterfaceTransitionInput = {
	targetMode: SessionInterfaceMode;
	policy: SessionInterfaceTransitionPolicy;
	historyPolicy?: SessionInterfaceTransitionHistoryPolicy;
	model?: string;
	reasoningEffort?: string;
};

type InterfaceTransitionMutationTarget = {
	targetSessionId: string;
	targetHostId?: string;
	targetCloudOrgId?: string;
};

export type SessionInterfaceContext = string | { orgId: string } | null;

function mutationContext(target: InterfaceTransitionMutationTarget): SessionInterfaceContext {
	return target.targetCloudOrgId ? { orgId: target.targetCloudOrgId } : target.targetHostId ?? null;
}

export type StartInterfaceTransitionRequest = StartInterfaceTransitionInput & { targetHarness?: string };

type StartInterfaceTransitionMutationInput = StartInterfaceTransitionRequest & InterfaceTransitionMutationTarget;

type AcknowledgeInterfaceTransitionNoticeMutationInput =
	InterfaceTransitionMutationTarget & {
		transitionId: string;
	};

const startInterfaceTransitionMutationKey = ["start-session-interface-transition"] as const;
const cancelInterfaceTransitionMutationKey = ["cancel-session-interface-transition"] as const;
const acknowledgeInterfaceTransitionNoticeMutationKey = [
	"acknowledge-session-interface-transition-notice",
] as const;

type InterfaceTransitionMutationState<TInput> = {
	error: unknown;
	input?: TInput;
	status: "error" | "idle" | "pending" | "success";
	submittedAt: number;
};

function toSessionInterfaceTransition(
	transition: CloudCpInterfaceTransition | undefined,
): SessionInterfaceTransition | undefined {
	return transition as SessionInterfaceTransition | undefined;
}

function useInterfaceTransitionMutations<TInput>(mutationKey: readonly unknown[]) {
	return useMutationState<InterfaceTransitionMutationState<TInput>>({
		filters: { mutationKey },
		select: (mutation) => ({
			error: mutation.state.error,
			input: mutation.state.variables as TInput | undefined,
			status: mutation.state.status,
			submittedAt: mutation.state.submittedAt,
		}),
	});
}

function summarizeInterfaceTransitionMutations<
	TInput extends InterfaceTransitionMutationTarget,
>(mutations: InterfaceTransitionMutationState<TInput>[], sessionId: string | undefined, hostId?: string, cloudOrgId?: string) {
	let latest: InterfaceTransitionMutationState<TInput> | undefined;
	let pending: InterfaceTransitionMutationState<TInput> | undefined;
	for (const mutation of mutations) {
		const input = mutation.input;
		if (!input || input.targetSessionId !== sessionId || input.targetHostId !== hostId || input.targetCloudOrgId !== cloudOrgId) continue;
		if (!latest || mutation.submittedAt >= latest.submittedAt) latest = mutation;
		if (
			mutation.status === "pending" &&
			(!pending || mutation.submittedAt >= pending.submittedAt)
		) {
			pending = mutation;
		}
	}
	const errored = !pending && latest?.status === "error" ? latest : undefined;
	return {
		error: errored ? apiErrorMessage(errored.error) : undefined,
		errorAt: errored?.submittedAt,
		isPending: Boolean(pending),
		pendingInput: pending?.input,
	};
}

function clearInterfaceTransitionMutationState(
	queryClient: QueryClient,
	mutationKey: readonly unknown[],
	sessionId: string | undefined,
	hostId?: string,
	cloudOrgId?: string,
) {
	if (!sessionId) return;
	const mutationCache = queryClient.getMutationCache();
	for (const mutation of mutationCache.findAll({ mutationKey })) {
		const input = mutation.state.variables as InterfaceTransitionMutationTarget | undefined;
		if (input?.targetSessionId === sessionId && input.targetHostId === hostId && input.targetCloudOrgId === cloudOrgId && mutation.state.status !== "pending") {
			mutationCache.remove(mutation);
		}
	}
}

const activePhases = new Set<SessionInterfaceTransition["phase"]>([
	"requested",
	"preflighting",
	"draining",
	"source_stopping",
	"source_stopped",
	"target_starting",
	"activating",
]);

const cancellablePhases = new Set<SessionInterfaceTransition["phase"]>([
	"requested",
	"preflighting",
	"draining",
]);

const liveTransitionPoll = 250;

const readinessReasonCodes = new Set([
	"NATIVE_SESSION_MISSING",
	"NATIVE_SESSION_UNVERIFIED",
	"WORKSPACE_UNAVAILABLE",
]);

type InterfaceTransitionPollQuery = {
	state: {
		data?: SessionInterfaceTransitionStatus;
		dataUpdateCount: number;
		dataUpdatedAt: number;
		error: unknown;
		errorUpdateCount: number;
	};
};

const pollBudgets = new WeakMap<
	InterfaceTransitionPollQuery,
	{ dataUpdatedAt: number; failureBase: number; readinessKey: string; readinessBase: number }
>();

function sessionGone(error: unknown): boolean {
	if (apiErrorCode(error) === "SESSION_NOT_FOUND") return true;
	const status = typeof error === "object" && error !== null ? (error as { status?: unknown }).status : undefined;
	return status === 404 || status === 410;
}

export function interfaceTransitionRefetchInterval(query: InterfaceTransitionPollQuery): number | false {
	const { state } = query;
	const status = state.data;
	const readinessKey = `${status?.transition?.phase ?? ""}|${status?.reasonCode ?? ""}`;
	let budget = pollBudgets.get(query);
	if (!budget) {
		budget = { dataUpdatedAt: state.dataUpdatedAt, failureBase: state.dataUpdateCount > 0 ? state.errorUpdateCount : 0, readinessKey, readinessBase: state.dataUpdateCount };
		pollBudgets.set(query, budget);
	}
	if (budget.dataUpdatedAt !== state.dataUpdatedAt) {
		budget.dataUpdatedAt = state.dataUpdatedAt;
		budget.failureBase = state.errorUpdateCount;
	}
	if (budget.readinessKey !== readinessKey) {
		budget.readinessKey = readinessKey;
		budget.readinessBase = state.dataUpdateCount;
	}
	const consecutiveFailures = state.errorUpdateCount - budget.failureBase;
	if (state.error instanceof CloudCpAuthError) return false;
	const delay = interfaceTransitionPollDelay({
		blocked: interfaceTransitionNeedsRestart(status?.transition),
		live: interfaceTransitionIsActive(status?.transition),
		livePollMs: liveTransitionPoll,
		readinessPending: Boolean(status?.reasonCode && readinessReasonCodes.has(status.reasonCode)),
		readinessAttempts: state.dataUpdateCount - budget.readinessBase,
		consecutiveFailures,
		sessionGone: consecutiveFailures > 0 && sessionGone(state.error),
	});
	return delay ?? false;
}

export function interfaceTransitionIsActive(transition?: SessionInterfaceTransition): boolean {
	return Boolean(transition && activePhases.has(transition.phase));
}

export function interfaceTransitionIsCancellable(transition?: SessionInterfaceTransition): boolean {
	return Boolean(transition && cancellablePhases.has(transition.phase));
}

export function interfaceTransitionNeedsRestart(transition?: SessionInterfaceTransition): boolean {
	return Boolean(
		transition &&
			interfaceTransitionIsActive(transition) &&
			transition.errorCode === "TARGET_STOP_UNCONFIRMED",
	);
}

export function interfaceTransitionHasUnacknowledgedNotice(
	transition?: SessionInterfaceTransition,
): boolean {
	return Boolean(
		interfaceTransitionNeedsRestart(transition) ||
			(transition &&
				!transition.noticeAcknowledgedAt &&
				(transition.phase === "failed" || transition.phase === "recovery_required")),
	);
}

export function sessionInterfaceTransitionQueryKey(sessionId: string, context?: SessionInterfaceContext) {
	if (typeof context === "string") return ["session-interface-transition", context, sessionId] as const;
	if (context) return ["session-interface-transition", "cloud", context.orgId, sessionId] as const;
	return ["session-interface-transition", sessionId] as const;
}

function useSessionInterfaceTransitionStatusQuery(
	sessionId: string | undefined,
	cloudCp: ReturnType<typeof useCloudCp>,
	context?: SessionInterfaceContext,
	hasSessionContext = false,
) {
	const queryClient = useQueryClient();
	const hostId = typeof context === "string" ? context : undefined;
	const cloud = context && typeof context === "object" ? context : undefined;
	const isCloud = Boolean(cloud && cloudCp.ready);
	const isLocal = context === null || !hasSessionContext;
	return useQuery({
		queryKey: sessionInterfaceTransitionQueryKey(sessionId ?? "", context),
		enabled: Boolean(sessionId && (hostId || isCloud || (isLocal && hasTrustedApiBaseUrl()))),
		queryFn: async () => {
			if (isCloud && cloud) {
				const [sessionResponse, transitionStatus] = await Promise.all([
					cloudCp.client.getSession(cloud.orgId, sessionId as string),
					cloudCp.client.getInterfaceTransition(cloud.orgId, sessionId as string),
				]);
				const previousTransition = queryClient.getQueryData<SessionInterfaceTransitionStatus>(
					sessionInterfaceTransitionQueryKey(sessionId as string, context),
				)?.transition;
				const completedTransition = !transitionStatus.transition && previousTransition &&
					(interfaceTransitionIsActive(previousTransition) || previousTransition.phase === "completed") &&
					sessionResponse.session.interfaceMode === previousTransition.targetMode
					? { ...previousTransition, phase: "completed" as const }
					: undefined;
				return {
					supported: transitionStatus.supported,
					targetMode: transitionStatus.targetMode,
					reasonCode: transitionStatus.reasonCode,
					reason: transitionStatus.reason,
					transition: toSessionInterfaceTransition(transitionStatus.transition) ?? completedTransition,
					currentMode: sessionResponse.session.interfaceMode,
				} as SessionInterfaceTransitionStatus & { currentMode?: SessionInterfaceMode };
			}
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/interface-transition",
				{ params: { path: { sessionId: sessionId as string } } },
			);
			if (error) throw error;
			return data as SessionInterfaceTransitionStatus;
		},
		refetchInterval: interfaceTransitionRefetchInterval,
		retry: false,
	});
}

export function useSessionInterfaceTransitionStatus(sessionId: string | undefined, context?: SessionInterfaceContext) {
	const query = useSessionInterfaceTransitionStatusQuery(sessionId, useCloudCp(Boolean(context && typeof context === "object")), context, context !== undefined);
	return {
		status: query.data,
		transition: query.data?.transition,
		isLoading: query.isLoading,
		statusError: query.error ? apiErrorMessage(query.error) : undefined,
	};
}

export function useSessionInterfaceTransition(
	sessionId: string | undefined,
	context?: SessionInterfaceContext,
) {
	const queryClient = useQueryClient();
	const hostId = typeof context === "string" ? context : undefined;
	const cloud = context && typeof context === "object" ? context : undefined;
	const cloudOrgId = cloud?.orgId;
	const cloudCp = useCloudCp(Boolean(cloud));
	const hasSessionContext = arguments.length >= 2;
	const isCloud = Boolean(cloud);
	const settledRef = useRef<string>("");
	const refreshAttemptRef = useRef(0);
	const [refreshingTransition, setRefreshingTransition] = useState<{
		attempt: number;
		key: string;
	}>();
	const query = useSessionInterfaceTransitionStatusQuery(
		sessionId,
		cloudCp,
		context,
		hasSessionContext,
	);

	const start = useMutation({
		mutationKey: startInterfaceTransitionMutationKey,
		mutationFn: async ({
			targetSessionId,
			targetHostId,
			targetCloudOrgId,
			targetHarness,
			...input
		}: StartInterfaceTransitionMutationInput) => {
			if (targetCloudOrgId) {
				const response = await cloudCp.client.startInterfaceTransition(
					targetCloudOrgId,
					targetSessionId,
					{
						targetMode: input.targetMode, policy: input.policy,
						...(input.model ? { model: input.model } : {}),
						...(input.reasoningEffort ? { reasoningEffort: input.reasoningEffort } : {}),
						...(targetHarness ? { targetHarness } : {}),
					},
				);
				return {
					transition: toSessionInterfaceTransition(response.transition),
				};
			}
			const { data, error } = await clientForSessionHost(targetHostId).POST(
				"/api/v1/sessions/{sessionId}/interface-transition",
				{
					params: { path: { sessionId: targetSessionId } },
					body: input,
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (response, variables) => {
			if (response?.transition) {
				queryClient.setQueryData<SessionInterfaceTransitionStatus>(
					sessionInterfaceTransitionQueryKey(variables.targetSessionId, mutationContext(variables)),
					{
						supported: true,
						targetMode: response.transition.targetMode,
						transition: response.transition,
					},
				);
			}
			const refreshes = [
				queryClient.invalidateQueries({
					queryKey: sessionInterfaceTransitionQueryKey(variables.targetSessionId, mutationContext(variables)),
				}),
			];
			if (variables.targetCloudOrgId) {
				refreshes.push(
					queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey }),
					queryClient.invalidateQueries({ queryKey: ["cloud-session"] }),
				);
			}
			return Promise.all(refreshes).catch(() => undefined);
		},
	});

	const cancel = useMutation({
		mutationKey: cancelInterfaceTransitionMutationKey,
		mutationFn: async ({ targetSessionId, targetHostId, targetCloudOrgId }: InterfaceTransitionMutationTarget) => {
			if (targetCloudOrgId) {
				return cloudCp.client.cancelInterfaceTransition(targetCloudOrgId, targetSessionId);
			}
			const { error } = await clientForSessionHost(targetHostId).DELETE(
				"/api/v1/sessions/{sessionId}/interface-transition",
				{ params: { path: { sessionId: targetSessionId } } },
			);
			if (error) throw error;
			return undefined;
		},
		onSuccess: (_data, variables) => {
			void queryClient.invalidateQueries({
				queryKey: sessionInterfaceTransitionQueryKey(variables.targetSessionId, mutationContext(variables)),
			});
		},
	});

	const acknowledgeNotice = useMutation({
		mutationKey: acknowledgeInterfaceTransitionNoticeMutationKey,
		mutationFn: async ({
			targetSessionId,
			targetHostId,
			targetCloudOrgId,
			transitionId,
		}: AcknowledgeInterfaceTransitionNoticeMutationInput) => {
			if (targetCloudOrgId) {
				return cloudCp.client.acknowledgeInterfaceTransitionNotice(
					targetCloudOrgId,
					targetSessionId,
					transitionId,
				);
			}
			const { data, error } = await clientForSessionHost(targetHostId).PUT(
				"/api/v1/sessions/{sessionId}/interface-transition/{transitionId}/notice-acknowledgement",
				{
					params: {
						path: { sessionId: targetSessionId, transitionId },
					},
				},
			);
			if (error) throw error;
			return data;
		},
		onSuccess: (response, variables) => {
			if (!response || variables.targetCloudOrgId) return;
			const localResponse = response as unknown as { transition: SessionInterfaceTransition };
			queryClient.setQueryData<SessionInterfaceTransitionStatus>(
				sessionInterfaceTransitionQueryKey(variables.targetSessionId, mutationContext(variables)),
				(current) =>
					current?.transition?.id === localResponse.transition.id
						? { ...current, transition: localResponse.transition }
						: current,
			);
		},
		onSettled: (_data, _error, variables) => {
			void queryClient.invalidateQueries({
				queryKey: sessionInterfaceTransitionQueryKey(variables.targetSessionId, mutationContext(variables)),
			});
		},
	});
	const startState = summarizeInterfaceTransitionMutations(
		useInterfaceTransitionMutations<StartInterfaceTransitionMutationInput>(
			startInterfaceTransitionMutationKey,
		),
		sessionId,
		hostId,
		cloudOrgId,
	);
	const cancelState = summarizeInterfaceTransitionMutations(
		useInterfaceTransitionMutations<InterfaceTransitionMutationTarget>(
			cancelInterfaceTransitionMutationKey,
		),
		sessionId,
		hostId,
		cloudOrgId,
	);
	const acknowledgeNoticeState = summarizeInterfaceTransitionMutations(
		useInterfaceTransitionMutations<AcknowledgeInterfaceTransitionNoticeMutationInput>(
			acknowledgeInterfaceTransitionNoticeMutationKey,
		),
		sessionId,
		hostId,
		cloudOrgId,
	);

	const transition = query.data?.transition;
	const transitionActive = interfaceTransitionIsActive(transition);
	const transitionID = transition?.id;
	const transitionKey =
		sessionId && transitionID ? JSON.stringify([hostId, cloudOrgId, sessionId, transitionID]) : "";
	const settling = Boolean(
		transitionKey &&
			!transitionActive &&
			(settledRef.current !== transitionKey ||
				refreshingTransition?.key === transitionKey),
	);
	useEffect(() => {
		if (!sessionId || !transitionKey || transitionActive) return;
		if (settledRef.current === transitionKey) return;
		settledRef.current = transitionKey;
		const attempt = ++refreshAttemptRef.current;
		setRefreshingTransition({ attempt, key: transitionKey });
		const refreshes = [
			queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
			queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) }),
		];
		if (isCloud) {
			refreshes.push(
				queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["cloud-session"] }),
			);
		}
		void Promise.all(refreshes).finally(() => {
			setRefreshingTransition((refreshing) =>
				refreshing?.key === transitionKey && refreshing.attempt === attempt
					? undefined
					: refreshing,
			);
		});
	}, [isCloud, queryClient, sessionId, hostId, transitionActive, transitionKey]);
	const transitionCreatedAt = transition ? Date.parse(transition.createdAt) : NaN;
	const startErrorSuperseded = Boolean(
		startState.errorAt !== undefined &&
			Number.isFinite(transitionCreatedAt) &&
			transitionCreatedAt > startState.errorAt,
	);
	useEffect(() => {
		if (!startErrorSuperseded) return;
		clearInterfaceTransitionMutationState(
			queryClient,
			startInterfaceTransitionMutationKey,
			sessionId,
			hostId,
			cloudOrgId,
		);
	}, [queryClient, sessionId, hostId, cloudOrgId, startErrorSuperseded]);

	const refreshStatus = useCallback(
		async (): Promise<SessionInterfaceTransitionStatus | undefined> => {
			const refreshed = await query.refetch();
			if (refreshed.error) throw refreshed.error;
			return refreshed.data;
		},
		[query.refetch],
	);

	return {
		status: query.data,
		transition,
		refreshStatus,
		settling,
		isLoading: query.isLoading,
		statusError: query.error ? apiErrorMessage(query.error) : undefined,
		start: (input: StartInterfaceTransitionRequest) => {
			if (!sessionId) return Promise.reject(new Error("No session is selected."));
			return start.mutateAsync({ ...input, targetSessionId: sessionId, targetHostId: hostId, targetCloudOrgId: cloudOrgId });
		},
		starting: startState.isPending,
		startingPolicy: startState.pendingInput?.policy,
		startingTarget: startState.pendingInput?.targetMode,
		startError: startErrorSuperseded ? undefined : startState.error,
		resetStartError: () => {
			clearInterfaceTransitionMutationState(
				queryClient,
				startInterfaceTransitionMutationKey,
				sessionId,
				hostId,
				cloudOrgId,
			);
		},
		cancel: () => {
			if (!sessionId) return Promise.reject(new Error("No session is selected."));
			return cancel.mutateAsync({ targetSessionId: sessionId, targetHostId: hostId, targetCloudOrgId: cloudOrgId });
		},
		cancelling: cancelState.isPending,
		cancelError: cancelState.error,
		acknowledgeNotice: (transitionId: string) => {
			if (!sessionId) return Promise.reject(new Error("No session is selected."));
			return acknowledgeNotice.mutateAsync({ targetSessionId: sessionId, targetHostId: hostId, targetCloudOrgId: cloudOrgId, transitionId });
		},
		acknowledgingNotice: acknowledgeNoticeState.isPending,
		acknowledgeNoticeError: acknowledgeNoticeState.error,
	};
}
