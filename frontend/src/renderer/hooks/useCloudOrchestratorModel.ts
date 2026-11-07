import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { CloudCpSession, CloudCpSessionChatSettings } from "../lib/cloud-cp";
import { cloudSessionEventsState, subscribeCloudSessionEvents } from "../lib/cloud-cp/session-events-hub";
import { useCachedQueryData } from "./useCloudScriptRuns";
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

/** A model or effort that names one: blank and "default" mean the agent's own. */
function concrete(value: string | undefined): string | undefined {
	const trimmed = value?.trim();
	return trimmed && trimmed !== "default" ? trimmed : undefined;
}

/**
 * What the orchestrator runs now. The control plane always reports it on the
 * session as `currentModel` (its owner's Chat pick, else the model it started
 * with; "" for the agent's own default). Only one that predates the field
 * leaves it out, and then the model the session started with stands in.
 * While the orchestrator's Chat is open in this window its live settings are
 * newer and win: a field set there replaces the session's ("default" being
 * the agent's own), and a field left unset keeps the session's. An effort
 * stays with the model it was chosen for, so a live model other than the
 * session's takes none of the session's effort.
 */
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

/**
 * The model a Cloud project's live orchestrator runs now, for a task started
 * beside it to default to. It reads only what the app already holds: the
 * session list the sidebar keeps polling, and the orchestrator's live settings
 * while its Chat is open in this window (that stream is never opened here).
 * Until the session list has loaded there is none, so the composer keeps its
 * other defaults meanwhile.
 */
export function useCloudOrchestratorModel({ baseUrl, orgId, projectId, enabled }: {
	baseUrl: string;
	orgId: string | undefined;
	projectId: string | undefined;
	enabled: boolean;
}): CloudOrchestratorModel | undefined {
	const active = enabled && Boolean(orgId && projectId);
	// Wakes only for the session list, not for every query the app holds.
	const sessions = useCachedQueryData<CloudCpSession[]>([...cloudSessionsQueryKey, baseUrl, orgId ?? ""], active);
	const session = active ? liveCloudOrchestrator(sessions, projectId) : undefined;
	// A listener that asks for neither events nor live state opens no stream.
	const target = { baseUrl, orgId: orgId ?? "", sessionId: session?.id ?? "" };
	const subscribeLive = useCallback(
		(notify: () => void) => (target.sessionId && target.baseUrl && target.orgId
			? subscribeCloudSessionEvents(target, { onStateChange: notify })
			: () => undefined),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- the target's parts are the identity
		[target.baseUrl, target.orgId, target.sessionId],
	);
	// Only the two fields read here, as one string: a live update that changes
	// neither (someone opening the Chat, the composer changing hands) re-renders nothing.
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
	// The same answer keeps its identity across the session list's polls.
	const answerKey = orchestrator
		? JSON.stringify([orchestrator.sessionId, orchestrator.harness, orchestrator.model, orchestrator.reasoningEffort ?? ""])
		: "";
	// eslint-disable-next-line react-hooks/exhaustive-deps -- the key is the answer's identity
	return useMemo(() => orchestrator, [answerKey]);
}
