import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useSettings } from "../../hooks/useSettings";
import { isConcreteModelID, modelChoiceLabel } from "../../lib/agent-model-choices";
import { clientForSessionHost } from "../../lib/host-clients";
import { sessionUiKey } from "../../lib/hosts";
import { useChatAgentDraftStore, type ChatAgentDraft } from "../../stores/chat-agent-draft-store";
import type { ChatModel, TurnSettings } from "../../types/conversation";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../../types/workspace";
import { AgentMenuSection } from "../TaskComposer";

export type SessionAgentSwitch = {
	/** The Agent row at the top of the model menu. */
	agentMenu?: ReactNode;
	/** Set while the chat will continue with another agent on the next send. */
	pending?: ChatAgentDraft & {
		models: ChatModel[];
		settings: TurnSettings;
		choose: (next: TurnSettings) => void;
	};
	clear: () => void;
};

/**
 * The agent choice inside a running chat. Picking an agent only records it, so
 * the bar shows that agent and its models at once and the choice can change
 * freely; the next send carries it and starts the switch.
 */
export function useSessionAgentSwitch({
	session,
	hostId,
	enabled,
}: {
	session: WorkspaceSession;
	hostId?: string;
	enabled: boolean;
}): SessionAgentSwitch {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const key = sessionUiKey(session.id, hostId);
	const draft = useChatAgentDraftStore((state) => state.drafts[key]);
	const setDraft = useChatAgentDraftStore((state) => state.setDraft);
	const projectId = session.workspaceId === STANDALONE_WORKSPACE_ID ? "" : session.workspaceId;
	const agentsQuery = useAgentReadinessQuery(enabled, hostId);
	const { settings } = useSettings(hostId);
	// A chat can only continue with an agent that runs as a chat.
	const agents = useMemo(() => {
		const chatHarnesses = settings?.chatHarnesses;
		return agentsQuery.data?.agents?.filter((agent) => !chatHarnesses || chatHarnesses.includes(agent.id));
	}, [agentsQuery.data?.agents, settings?.chatHarnesses]);
	// Kept after the switch lands, until the surface hands the bar to the new
	// agent's own settings (it then calls clear), so the bar never blanks.
	const pendingDraft = enabled ? draft : undefined;
	const catalog = useQuery({
		...agentModelsQueryOptions(pendingDraft?.harness ?? "", projectId, hostId),
		enabled: Boolean(pendingDraft),
	}).data;
	const models = useMemo<ChatModel[]>(() => {
		const usesModes = catalog?.selectionMode === "mode";
		return (catalog?.models ?? []).filter((item) => isConcreteModelID(item.id)).map((item) => ({
			id: item.id,
			displayName: item.id === "auto" ? t("settings.models.autoRouteLabel") : usesModes ? modelChoiceLabel(item) : item.label,
			default: Boolean(item.isDefault),
			efforts: item.efforts,
			defaultEffort: item.defaultEffort,
		}));
	}, [catalog, t]);

	// One model always shows, and it is the one the switch starts with: the
	// agent's default when it marks one, otherwise the first listed.
	const model = pendingDraft
		? models.find((item) => item.id === pendingDraft.model) ?? models.find((item) => item.default) ?? models[0]
		: undefined;
	const effort = pendingDraft?.effort && model?.efforts?.includes(pendingDraft.effort)
		? pendingDraft.effort
		: model?.defaultEffort;
	const catalogLoaded = catalog !== undefined;
	const pendingHarness = pendingDraft?.harness;
	const modelId = model?.id ?? "";
	const effortId = effort ?? "";

	// Start the picked agent's process now, with the model and effort the send
	// will carry, so the switch adopts it instead of starting one. Picking the
	// chat's own agent again drops it. The daemon replaces an earlier standby.
	// It waits for the choice to settle: each new choice replaces the process the
	// daemon started for the last one.
	const prewarmed = useRef(false);
	useEffect(() => {
		if (!enabled) return;
		if (pendingHarness && !catalogLoaded) return;
		if (!pendingHarness && !prewarmed.current) return;
		const timer = setTimeout(() => {
			prewarmed.current = Boolean(pendingHarness);
			void clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/switch-agent/prewarm", {
				params: { path: { sessionId: session.id } },
				body: pendingHarness
					? { targetHarness: pendingHarness, model: modelId || undefined, reasoningEffort: effortId || undefined }
					: { targetHarness: session.provider },
			}).catch(() => undefined);
		}, 400);
		return () => clearTimeout(timer);
	}, [enabled, hostId, session.id, session.provider, pendingHarness, catalogLoaded, modelId, effortId]);

	const clear = () => setDraft(key, undefined);
	const choose = (harness: string) => {
		setDraft(key, harness === session.provider ? undefined : { harness, model: "", effort: "" });
		// The daemon checks an agent can run as a chat the first time it starts one,
		// which can take a second; run that now so the send does not wait on it.
		if (harness !== session.provider) {
			void clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/chat/prewarm", {
				params: { path: { agent: harness } },
			}).catch(() => undefined);
		}
	};
	const agentMenu = enabled ? (
		<AgentMenuSection
			agents={agents}
			value={pendingDraft?.harness ?? session.provider}
			onChange={choose}
			// Load an agent's models before it is picked, so switching shows its model at once.
			onPreview={(agentId) => void queryClient.prefetchQuery(agentModelsQueryOptions(agentId, projectId, hostId))}
			hostId={hostId}
			manageView="local"
		/>
	) : undefined;

	if (!pendingDraft) return { agentMenu, clear };
	return {
		agentMenu,
		clear,
		pending: {
			...pendingDraft,
			model: model?.id ?? "",
			effort: effort ?? "",
			models,
			settings: { model: model?.id, reasoningEffort: effort || undefined },
			choose: (next) => setDraft(key, {
				harness: pendingDraft.harness,
				model: next.model ?? "",
				// Levels are per model, so a new model starts at its own default.
				effort: next.model !== model?.id ? "" : next.reasoningEffort ?? "",
			}),
		},
	};
}
