import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { CloudCpSession, CloudCpSessionChatSettings } from "../lib/cloud-cp";
import { cloudSessionEventsState, subscribeCloudSessionEvents } from "../lib/cloud-cp/session-events-hub";
import { useCachedQueryData } from "./useCloudScriptRuns";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";

export type CloudOrchestratorModel = {
	sessionId: string;
	harness: string;
	model: string;
	reasoningEffort?: string;
};

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

function concrete(value: string | undefined): string | undefined {
	const trimmed = value?.trim();
	return trimmed && trimmed !== "default" ? trimmed : undefined;
}

export function cloudOrchestratorModel(
	session: Pick<CloudCpSession, "id" | "harness" | "model" | "currentModel" | "currentReasoningEffort"> | undefined,
	live: Pick<CloudCpSessionChatSettings, "model" | "reasoningEffort"> | undefined,
): CloudOrchestratorModel | undefined {
	if (!session) return undefined;
	const sessionModel = Object.prototype.hasOwnProperty.call(session, "currentModel")
		? concrete(session.currentModel)
		: concrete(session.model);
	const model = live?.model?.trim() ? concrete(live.model) : sessionModel;
	if (!model) return undefined;
	const reasoningEffort = live?.reasoningEffort?.trim()
		? concrete(live.reasoningEffort)
		: model === sessionModel ? concrete(session.currentReasoningEffort) : undefined;
	return { sessionId: session.id, harness: session.harness, model, ...(reasoningEffort ? { reasoningEffort } : {}) };
}

export function useCloudOrchestratorModel({ baseUrl, orgId, projectId, enabled }: {
	baseUrl: string;
	orgId: string | undefined;
	projectId: string | undefined;
	enabled: boolean;
}): CloudOrchestratorModel | undefined {
	const active = enabled && Boolean(orgId && projectId);
	const sessions = useCachedQueryData<CloudCpSession[]>([...cloudSessionsQueryKey, baseUrl, orgId ?? ""], active);
	const session = active ? liveCloudOrchestrator(sessions, projectId) : undefined;
	const target = { baseUrl, orgId: orgId ?? "", sessionId: session?.id ?? "" };
	const subscribeLive = useCallback(
		(notify: () => void) => (target.sessionId && target.baseUrl && target.orgId
			? subscribeCloudSessionEvents(target, { onStateChange: notify })
			: () => undefined),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- the target's parts are the identity
		[target.baseUrl, target.orgId, target.sessionId],
	);
	const liveKey = useSyncExternalStore(subscribeLive, () => {
		const chat = target.sessionId ? cloudSessionEventsState(target).live?.chatSettings : undefined;
		return chat ? JSON.stringify([chat.model ?? "", chat.reasoningEffort ?? ""]) : "";
	});
	const liveSettings = useMemo(() => {
		if (!liveKey) return undefined;
		const [model, reasoningEffort] = JSON.parse(liveKey) as [string, string];
		return { model, reasoningEffort };
	}, [liveKey]);
	const orchestrator = session ? cloudOrchestratorModel(session, liveSettings) : undefined;
	const answerKey = orchestrator
		? JSON.stringify([orchestrator.sessionId, orchestrator.harness, orchestrator.model, orchestrator.reasoningEffort ?? ""])
		: "";
	// eslint-disable-next-line react-hooks/exhaustive-deps -- the key is the answer's identity
	return useMemo(() => orchestrator, [answerKey]);
}
