import { type QueryClient, useMutation, useMutationState, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import type { WorkspaceSession } from "../types/workspace";
import { agentSwitchesQueryKey, type AgentSwitch } from "./useAgentSwitches";
import {
	clearConversationProviderCatalogs,
	conversationQueryKey,
	releaseSwitchLocalEcho,
	seedSwitchLocalEcho,
} from "./useConversation";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { recordDirectWorkerInteraction } from "../lib/session-management-telemetry";

export type SwitchAgentHarness = components["schemas"]["SwitchAgentRequest"]["targetHarness"];
export type SwitchAgentMessage = components["schemas"]["SendConversationMessageRequest"] & { clientMessageId: string };

export type SwitchAgentInput = {
	session: WorkspaceSession;
	targetHarness: SwitchAgentHarness;
	model: string;
	/** Reasoning level the new agent starts with; empty keeps the project's. */
	effort?: string;
	idempotencyKey: string;
	/** Sent from the composer: the new agent answers this first. */
	message?: SwitchAgentMessage;
};

/** The daemon refused the switch, so the message it carried was never delivered. */
export const AGENT_SWITCH_REJECTED = "AGENT_SWITCH_REJECTED";

// Switches started from the composer. They show as the message working, not as
// the switch dialog's progress card. Renderer-lifetime only: after a reload a
// switch still in flight falls back to the card.
const composerSwitchIds = new Set<string>();

export function isComposerAgentSwitch(switchId: string | undefined): boolean {
	return Boolean(switchId && composerSwitchIds.has(switchId));
}

export const switchAgentMutationKey = ["switch-agent"] as const;
export const recoverAgentSwitchMutationKey = ["recover-agent-switch"] as const;

type SwitchAgentMutationState = {
	error: unknown;
	input?: SwitchAgentInput;
	status: "error" | "idle" | "pending" | "success";
	submittedAt: number;
};

function useSwitchAgentMutations() {
	return useMutationState<SwitchAgentMutationState>({
		filters: { mutationKey: switchAgentMutationKey },
		select: (mutation) => ({
			error: mutation.state.error,
			input: mutation.state.variables as SwitchAgentInput | undefined,
			status: mutation.state.status,
			submittedAt: mutation.state.submittedAt,
		}),
	});
}

export function useSwitchAgentState(sessionId: string, hostId?: string) {
	const mutations = useSwitchAgentMutations();
	const targetKey = sessionUiKey(sessionId, hostId);
	let latest: SwitchAgentMutationState | undefined;
	let pending: SwitchAgentMutationState | undefined;
	for (const mutation of mutations) {
		if (!mutation.input || sessionUiKey(mutation.input.session.id, mutation.input.session.hostId) !== targetKey) continue;
		if (!latest || mutation.submittedAt > latest.submittedAt) latest = mutation;
		if (
			mutation.status === "pending" &&
			(!pending || mutation.submittedAt > pending.submittedAt)
		) {
			pending = mutation;
		}
	}

	return {
		error:
			!pending &&
			latest?.status === "error" &&
			latest.error instanceof Error
				? latest.error.message
				: null,
		input: pending?.input,
		isPending: Boolean(pending),
	};
}

export function clearSwitchAgentState(queryClient: QueryClient, sessionId: string, hostId?: string) {
	const mutationCache = queryClient.getMutationCache();
	const targetKey = sessionUiKey(sessionId, hostId);
	for (const mutation of mutationCache.findAll({ mutationKey: switchAgentMutationKey })) {
		const input = mutation.state.variables as SwitchAgentInput | undefined;
		if (input && sessionUiKey(input.session.id, input.session.hostId) === targetKey && mutation.state.status !== "pending") {
			mutationCache.remove(mutation);
		}
	}
}

export function createSwitchAgentIdempotencyKey(): string {
	return crypto.randomUUID();
}

export function useSwitchAgent() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: switchAgentMutationKey,
		onMutate: ({ session, message }: SwitchAgentInput) => {
			if (!message) return;
			seedSwitchLocalEcho(queryClient, session.id, session.hostId, {
				clientMessageId: message.clientMessageId,
				text: message.text,
				excerpts: message.excerpts,
				createdAt: new Date().toISOString(),
			});
		},
		mutationFn: async ({ session, targetHarness, model, effort, idempotencyKey, message }: SwitchAgentInput) => {
			recordDirectWorkerInteraction(session.id, "lifecycle", session.kind, session.hostId);
			const body: components["schemas"]["SwitchAgentRequest"] = { targetHarness, idempotencyKey };
			const normalizedModel = model.trim();
			if (normalizedModel) body.model = normalizedModel;
			if (effort?.trim()) body.reasoningEffort = effort.trim();
			if (message) body.message = message;
			const { data, error, response } = await clientForSessionHost(session.hostId).POST(
				"/api/v1/sessions/{sessionId}/switch-agent",
				{
					params: { path: { sessionId: session.id } },
					body,
				},
			);
			if (error || response.status !== 202 || !data?.switch) {
				const fallback = response
					? `Failed to switch agent (${response.status})`
					: "Failed to switch agent";
				const failure = new Error(apiErrorMessage(error, fallback));
				// A refusal means no switch exists, so the composer may give the text back.
				// Anything else may have been accepted; a retry reuses the same key.
				if (response && response.status >= 400 && response.status < 500) {
					Object.assign(failure, { code: AGENT_SWITCH_REJECTED });
				}
				throw failure;
			}
			return data.switch;
		},
		onError: (_error, { session, message }) => {
			if (message) releaseSwitchLocalEcho(queryClient, session.id, session.hostId, message.clientMessageId);
		},
		onSuccess: (agentSwitch, variables) => {
			if (!agentSwitch) return;
			if (variables.message) composerSwitchIds.add(agentSwitch.id);
			queryClient.setQueryData<AgentSwitch[]>(
				agentSwitchesQueryKey(variables.session.id, variables.session.hostId),
				(current = []) => [agentSwitch, ...current.filter((entry) => entry.id !== agentSwitch.id)],
			);
			clearConversationProviderCatalogs(queryClient, variables.session.id, variables.session.hostId);
			void queryClient.invalidateQueries({ queryKey: conversationQueryKey(variables.session.id, variables.session.hostId) });
		},
		onSettled: (_data, _error, variables) => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(variables.session.hostId) });
			void queryClient.invalidateQueries({ queryKey: agentSwitchesQueryKey(variables.session.id, variables.session.hostId) });
		},
	});
}

export function useRecoverAgentSwitch() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: recoverAgentSwitchMutationKey,
		mutationFn: async ({ sessionId, switchId, hostId }: { sessionId: string; switchId: string; hostId?: string }) => {
			const { data, error, response } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/agent-switches/{switchId}/recover",
				{ params: { path: { sessionId, switchId } } },
			);
			if (error || response.status !== 202 || !data?.switch) {
				const fallback = response
					? `Failed to recover agent switch (${response.status})`
					: "Failed to recover agent switch";
				throw new Error(apiErrorMessage(error, fallback));
			}
			return data.switch;
		},
		onSuccess: (agentSwitch, variables) => {
			queryClient.setQueryData<AgentSwitch[]>(
				agentSwitchesQueryKey(variables.sessionId, variables.hostId),
				(current = []) => [agentSwitch, ...current.filter((entry) => entry.id !== agentSwitch.id)],
			);
		},
		onSettled: (_data, _error, variables) => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(variables.hostId) });
			void queryClient.invalidateQueries({ queryKey: agentSwitchesQueryKey(variables.sessionId, variables.hostId) });
		},
	});
}
