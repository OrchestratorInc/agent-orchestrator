import type { QueryClient } from "@tanstack/react-query";
import type { TriageChoice, TriageOrigin, TriageRun, TriageSelection } from "../../shared/triage";
import type { AgentReadiness } from "../hooks/useAgentReadinessQuery";
import type { AgentModelCatalog } from "../hooks/useAgentModelsQuery";
import type { Settings } from "../hooks/useSettings";
import { agentAccountProviderFor, agentAccountsQueryKey, type AgentAccount } from "../hooks/useAgentAccounts";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { readTaskComposerPreferences } from "./task-composer-preferences";
import { buildRankedAgentOptions, DEFAULT_AGENT_PRIORITY_RANK, isReadyAgent } from "./agent-select-options";

export function triageHref(runId?: string): string {
	return runId ? `#/triage?run=${encodeURIComponent(runId)}` : "#/triage";
}

export function isTriageActive(run: TriageRun): boolean {
	return !isTriageSessionRun(run) && !run.preparation && ["starting", "working", "running", "needs_input"].includes(run.state);
}

export function isTriageSessionRun(run: TriageRun): boolean {
	return run.execution === "session" || !!run.sessionId;
}

export function validTriageSelection(choices: TriageChoice[], selection: TriageSelection | null): boolean {
	const choice = choices.find((item) => item.harness === selection?.harness);
	if (!selection || !choice?.available || !choice.models.some((model) => model.id === (selection.model ?? ""))) return false;
	if (selection.effort && !choice.models.find((model) => model.id === selection.model)?.efforts?.includes(selection.effort)) return false;
	return !selection.accountId || !!choice.accounts?.some((account) => account.id === selection.accountId);
}

function configuredAccounts(queryClient: QueryClient, harness: string, prior: TriageChoice["accounts"]): TriageChoice["accounts"] {
	const provider = agentAccountProviderFor(harness);
	const cached = queryClient.getQueryData<AgentAccount[]>(agentAccountsQueryKey);
	return !provider || cached === undefined ? prior : cached.filter((account) => account.provider === provider && account.signedIn)
		.map(({ id, email, default: isDefault }) => ({ id, label: email || `${provider} account`, default: isDefault }));
}

export function currentTriageOrigin(): TriageOrigin {
	const route = window.location.hash.replace(/^#/, "").split("?")[0];
	const sessionId = /\/(?:sessions|session)\/([^/]+)/.exec(route)?.[1];
	const hostId = /^\/host\/([^/]+)\//.exec(route)?.[1];
	return { surface: "report-problem", ...(sessionId ? { sessionId: decodeURIComponent(sessionId) } : {}), ...(hostId ? { hostId: decodeURIComponent(hostId) } : {}) };
}

export function cachedTriageModels(queryClient: QueryClient, harness: string): AgentModelCatalog | undefined {
	return queryClient.getQueryData<AgentModelCatalog>(["agent-models", harness, "", "catalog"])
		?? queryClient.getQueryData<AgentModelCatalog>(["agent-models", harness, ""]);
}

/** Choose from this machine's cached defaults and the standalone composer preference. */
export function defaultTriageSelection(queryClient: QueryClient, choices: TriageChoice[], _origin: TriageOrigin, harnessOverride?: string): TriageSelection | null {
	const readiness = queryClient.getQueryData<AgentReadiness>(["agent-readiness"]);
	const remembered = readTaskComposerPreferences(STANDALONE_WORKSPACE_ID);
	const configured = (id: string | undefined) => id && choices.some((choice) => choice.harness === id) ? id : undefined;
	const harness = harnessOverride || configured(remembered?.lastAgent) ||
		(readiness && buildRankedAgentOptions({ agents: readiness.agents, priorityRank: DEFAULT_AGENT_PRIORITY_RANK, fallbackAgents: [] }).find((agent) => isReadyAgent(agent) && configured(agent.id))?.id) ||
		[...choices].sort((a, b) => (DEFAULT_AGENT_PRIORITY_RANK.get(a.harness) ?? 999) - (DEFAULT_AGENT_PRIORITY_RANK.get(b.harness) ?? 999))[0]?.harness;
	if (!harness) return null;
	const choice = choices.find((item) => item.harness === harness);
	if (!choice) return { harness };
	const preference = remembered?.agents[harness];
	const disabled = new Set(queryClient.getQueryData<Settings>(["settings"])?.harnessDefaults?.[harness]?.disabledModels ?? []);
	const models = choice.models.filter((model) => !disabled.has(model.id));
	const catalog = cachedTriageModels(queryClient, harness);
	const model = [catalog?.selectionMode === "mode" ? preference?.mode : preference?.model, catalog?.models.find((item) => item.isDefault)?.id, models.find((item) => item.default)?.id,
		models.some((item) => item.id === "") ? "" : undefined, models.length === 1 ? models[0].id : undefined].find((id) => id !== undefined && models.some((item) => item.id === id));
	const selected = models.find((item) => item.id === model);
	const accounts = configuredAccounts(queryClient, harness, choice.accounts);
	const accountId = accounts?.find((account) => account.default)?.id || (!agentAccountProviderFor(harness) && accounts?.length === 1 ? accounts[0].id : undefined);
	return { harness, ...(model === undefined ? {} : { model }), ...(preference?.effort && selected?.efforts?.includes(preference.effort) ? { effort: preference.effort } : {}),
		...(accountId ? { accountId } : {}), ...(choice.mode ? { mode: choice.mode } : {}), ...(choice.permissions ? { permissions: choice.permissions } : {}), sessionMode: choice.sessionMode };
}

/** Project only the locally cached configured harnesses and models to the helper. */
export function configuredTriageSnapshot(queryClient: QueryClient, native: TriageChoice[]): TriageChoice[] | undefined {
	const readiness = queryClient.getQueryData<AgentReadiness>(["agent-readiness"]);
	const settings = queryClient.getQueryData<Settings>(["settings"]);
	if (!readiness || !settings) return undefined;
	const ready = new Set(readiness.agents.filter((agent) => agent.authentication.state !== "unauthorized" &&
		(["authorized", "configured"].includes(agent.authentication.state) || !!agent.authentication.account)).map((agent) => agent.id));
	const choices = new Map(native.map((choice) => [choice.harness, choice]));
	for (const agent of readiness.agents) {
		if (ready.has(agent.id) && !choices.has(agent.id)) choices.set(agent.id, { harness: agent.id, label: agent.label, models: [], modelsMissing: true, available: true });
	}
	return [...choices.values()].filter((choice) => ready.has(choice.harness))
		.map((choice) => {
			const catalog = cachedTriageModels(queryClient, choice.harness);
			const disabled = new Set(settings.harnessDefaults?.[choice.harness]?.disabledModels ?? []);
			const models = catalog?.models.filter((model) => !disabled.has(model.id) && (!model.provider || catalog.configuredProviders === undefined || catalog.configuredProviders.includes(model.provider)))
				.map(({ id, label, efforts, isDefault }) => ({ id, label, efforts, default: isDefault })) ?? choice.models.filter((model) => !disabled.has(model.id));
			const accounts = configuredAccounts(queryClient, choice.harness, choice.accounts);
			const sessionMode = choice.harness === "unreal-agent" ? "chat" : choice.mode ? "tui" : settings.defaultSessionMode && settings.chatHarnesses ?
				(settings.defaultSessionMode === "chat" && settings.chatHarnesses.includes(choice.harness) ? "chat" : "tui") : choice.sessionMode;
			return { ...choice, models, modelsMissing: catalog ? false : choice.modelsMissing, accounts, sessionMode, available: choice.available && models.length > 0 };
		});
}

/** Add only cached context for the affected session and this machine. */
export function cachedTriageOrigin(queryClient: QueryClient, origin: TriageOrigin): TriageOrigin {
	const workspaces = queryClient.getQueryData<{ sessions: WorkspaceSession[] }[]>(origin.hostId ? ["remote-workspaces", origin.hostId] : ["workspaces"]);
	const session = origin.session && origin.session.id === origin.sessionId ? origin.session :
		workspaces?.flatMap((workspace) => workspace.sessions).find((item) => item.id === origin.sessionId);
	const machine = !origin.hostId && !session?.cloud ? queryClient.getQueryData<{ app?: unknown; system?: unknown; fetchedAt?: string }>(["session-memory", "all"]) : undefined;
	return { ...origin, ...(session ? { session: { ...session } } : {}),
		...(machine ? { machine: { app: machine.app, system: machine.system, fetchedAt: machine.fetchedAt } } : {}) };
}

export type AgentListRow =
	| { kind: "session"; id: string; session: WorkspaceSession }
	| { kind: "triage"; id: string; run: TriageRun };

/** Keep only native recovery runs here; linked runs already have a session row. */
export function mergeAgentRows(sessions: WorkspaceSession[], runs: TriageRun[]): AgentListRow[] {
	return [
		...runs.filter((run) => !run.preparation && !isTriageSessionRun(run) && !!run.transport).map((run): AgentListRow => ({ kind: "triage", id: `triage:${run.id}`, run })),
		...sessions.map((session): AgentListRow => ({ kind: "session", id: session.id, session })),
	];
}
