import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import type { CloudCpSession, CloudCpSessionChatSettings } from "../lib/cloud-cp";
import { cloudSessionEventsState, subscribeCloudSessionEvents } from "../lib/cloud-cp/session-events-hub";
import { toAgentProvider } from "../types/workspace";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";

/** The model, and effort if known, a Cloud project's live orchestrator runs now. */
export type CloudOrchestratorModel = {
	sessionId: string;
	harness: string;
	model: string;
	reasoningEffort?: string;
};

/** The project's newest orchestrator that is still running, if any. */
export function liveCloudOrchestrator(sessions: readonly CloudCpSession[] | undefined, projectId: string | undefined): CloudCpSession | undefined {
	if (!projectId) return undefined;
	let newest: CloudCpSession | undefined;
	for (const session of sessions ?? []) {
		if (session.projectId !== projectId || session.kind !== "orchestrator") continue;
		if (session.isTerminated || session.status === "terminated") continue;
		if (!newest || Date.parse(session.createdAt) > Date.parse(newest.createdAt)) newest = session;
	}
	return newest;
}

/**
 * What the orchestrator runs: the model its Chat is set to, else the model the
 * session started with (the project's orchestrator model, when it named one).
 * An effort set in its Chat comes with it.
 */
export function cloudOrchestratorModel(
	session: Pick<CloudCpSession, "id" | "harness" | "model"> | undefined,
	chat: Pick<CloudCpSessionChatSettings, "model" | "reasoningEffort"> | undefined,
): CloudOrchestratorModel | undefined {
	if (!session) return undefined;
	const model = chat?.model?.trim() || session.model?.trim() || "";
	if (!model) return undefined;
	const reasoningEffort = chat?.reasoningEffort?.trim();
	return { sessionId: session.id, harness: session.harness, model, ...(reasoningEffort ? { reasoningEffort } : {}) };
}

/**
 * The orchestrator's Chat settings as this computer saved them: its Chat
 * (CloudSessionChatSurface) writes the owner's picks, and the worker's own model
 * once it reports one, under this key.
 */
function savedChatSettings(orgId: string, session: Pick<CloudCpSession, "id" | "harness">): Pick<CloudCpSessionChatSettings, "model" | "reasoningEffort"> | undefined {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(`cloud-chat-settings:${orgId}:${session.id}:${toAgentProvider(session.harness)}`) ?? "null");
		if (!saved || typeof saved !== "object") return undefined;
		const { model, reasoningEffort } = saved as Record<string, unknown>;
		return {
			...(typeof model === "string" && model ? { model } : {}),
			...(typeof reasoningEffort === "string" && reasoningEffort ? { reasoningEffort } : {}),
		};
	} catch {
		return undefined;
	}
}

const NO_SESSIONS: CloudCpSession[] | undefined = undefined;

/**
 * The model a Cloud project's live orchestrator runs now, for a task started
 * beside it to default to. It reads only what the app already holds: the
 * session list the sidebar keeps polling, the orchestrator's settings while its
 * Chat is open (its live stream, never opened here), and the settings its Chat
 * saved on this computer. Until the session list has loaded there is none, so
 * the composer keeps its other defaults meanwhile.
 */
export function useCloudOrchestratorModel({ baseUrl, orgId, projectId, enabled }: {
	baseUrl: string;
	orgId: string | undefined;
	projectId: string | undefined;
	enabled: boolean;
}): CloudOrchestratorModel | undefined {
	const queryClient = useQueryClient();
	const active = enabled && Boolean(orgId && projectId);
	const sessionsKey = [...cloudSessionsQueryKey, baseUrl, orgId ?? ""];
	const subscribeSessions = useCallback(
		(notify: () => void) => (active ? queryClient.getQueryCache().subscribe(notify) : () => undefined),
		[active, queryClient],
	);
	const sessions = useSyncExternalStore(
		subscribeSessions,
		() => (active ? queryClient.getQueryData<CloudCpSession[]>(sessionsKey) : NO_SESSIONS),
	);
	const session = active ? liveCloudOrchestrator(sessions, projectId) : undefined;
	// The live state of an orchestrator whose Chat is open elsewhere in the app.
	// A listener that asks for neither events nor live state opens no stream.
	const target = { baseUrl, orgId: orgId ?? "", sessionId: session?.id ?? "" };
	const subscribeLive = useCallback(
		(notify: () => void) => (target.sessionId && target.baseUrl && target.orgId
			? subscribeCloudSessionEvents(target, { onStateChange: notify })
			: () => undefined),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- the target's parts are the identity
		[target.baseUrl, target.orgId, target.sessionId],
	);
	const liveSettings = useSyncExternalStore(
		subscribeLive,
		() => (target.sessionId ? cloudSessionEventsState(target).live?.chatSettings : undefined),
	);
	if (!session || !orgId) return undefined;
	// A model the session shares wins: the owner may have set it on another computer.
	const chat = liveSettings?.model ? liveSettings : savedChatSettings(orgId, session) ?? liveSettings;
	return cloudOrchestratorModel(session, chat);
}
