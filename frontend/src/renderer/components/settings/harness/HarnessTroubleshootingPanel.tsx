import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import type { AgentReadinessSnapshot } from "../../../hooks/useAgentReadinessQuery";
import { toWorkspaceSession, workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiClient, apiErrorMessage } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { STANDALONE_WORKSPACE_ID } from "../../../types/workspace";
import { useResolvedTheme } from "../../../stores/ui-store";
import { TerminalPane } from "../../TerminalPane";
import { Button } from "../../ui/button";
import {
	collectTroubleshootingSnapshot, TROUBLESHOOTING_CLAIMS, TROUBLESHOOTING_HELPERS,
	troubleshootingIssueDraft, troubleshootingPrompt, troubleshootingVerified,
	type TroubleshootingClaim, type TroubleshootingHelper, type TroubleshootingSession, type TroubleshootingSnapshot,
} from "./harness-troubleshooting";

type TroubleshootingState = {
	claim: TroubleshootingClaim;
	before?: TroubleshootingSnapshot;
	after?: TroubleshootingSnapshot;
	session?: TroubleshootingSession;
	stopped?: boolean;
	busy?: boolean;
	error?: string;
	draft?: string;
	requestId?: string;
	verificationFailed?: boolean;
};
// Retain the helper and evidence across harness/tab switches and Settings visits.
// Memory only: never store diagnostic packages in localStorage.
export const useHarnessTroubleshooting = create<{ entries: Partial<Record<AgentId, TroubleshootingState>> }>(() => ({ entries: {} }));
function update(agentId: AgentId, patch: Partial<TroubleshootingState>) {
	useHarnessTroubleshooting.setState(({ entries }) => ({ entries: {
		...entries, [agentId]: { claim: "login", ...entries[agentId], ...patch },
	} }));
}

export function HarnessTroubleshootingPanel({ agentId, agents, installationBusy }: {
	agentId: AgentId; agents: AgentReadinessSnapshot[]; installationBusy: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const theme = useResolvedTheme();
	const state = useHarnessTroubleshooting((store) => store.entries[agentId]);
	const claim = state?.claim ?? "login";
	const [helperChoice, setHelperChoice] = useState<TroubleshootingHelper>();
	const [copied, setCopied] = useState(false);
	const helpers = TROUBLESHOOTING_HELPERS.filter((id) => agents.some((agent) => agent.id === id
		&& agent.installation.state === "installed" && agent.effectiveReadiness === "ready"
		&& agent.authentication.freshness === "fresh"
		&& ["authorized", "not_applicable"].includes(agent.authentication.state)));
	const helper = helpers.find((id) => id === helperChoice) ?? helpers.find((id) => id !== agentId) ?? helpers[0];
	const busy = Boolean(state?.busy);
	const latest = state?.after ?? state?.before;
	const run = async (operation: () => Promise<void>) => {
		if (useHarnessTroubleshooting.getState().entries[agentId]?.busy) return;
		update(agentId, { busy: true, error: undefined });
		try { await operation(); }
		catch (error) { update(agentId, { error: error instanceof Error ? error.message : t("settings.harness.troubleshoot.failed") }); }
		finally { update(agentId, { busy: false }); }
	};
	const verify = () => run(async () => {
		update(agentId, { verificationFailed: true });
		const version = await aoBridge.app.getVersion();
		const snapshot = await collectTroubleshootingSnapshot(agentId, queryClient, version, document.documentElement.dataset.aoPlatform ?? "unknown");
		const current = useHarnessTroubleshooting.getState().entries[agentId];
		update(agentId, { ...(current?.before ? { after: snapshot } : { before: snapshot }), verificationFailed: false });
	});
	const start = () => run(async () => {
		if (!helper || !latest || state?.session) return;
		const requestId = state?.requestId ?? crypto.randomUUID();
		update(agentId, { requestId });
		const { data, error } = await apiClient.POST("/api/v1/sessions", { body: {
			clientRequestId: requestId, kind: "worker", harness: helper, mode: "tui", approvalMode: "accept-edits",
			displayName: `Troubleshoot ${agentLabel(agentId)}`, prompt: troubleshootingPrompt(claim, latest),
		} });
		if (error || !data) throw new Error(apiErrorMessage(error, t("settings.harness.troubleshoot.failed")));
		update(agentId, { session: data.session });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	});
	const stop = () => run(async () => {
		if (!state?.session) return;
		const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", { params: { path: { sessionId: state.session.id } } });
		if (error) throw new Error(apiErrorMessage(error, t("settings.harness.troubleshoot.failed")));
		update(agentId, { stopped: true });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	});
	const copy = (text: string) => run(async () => {
		await aoBridge.clipboard.writeText(text);
		setCopied(true);
	});
	const session = state?.session ? toWorkspaceSession(state.session, { id: STANDALONE_WORKSPACE_ID, name: "" }) : undefined;
	return <section className="space-y-4 text-sm" aria-label={t("settings.harness.troubleshoot.title")}>
		<p className="text-settings-muted">{t("settings.harness.troubleshoot.description")}</p>
		<label className="block space-y-2">
			<span>{t("settings.harness.troubleshoot.observation")}</span>
			<select className="block w-full rounded border border-border bg-surface p-2" value={claim} disabled={busy || Boolean(session)} onChange={(event) => update(agentId, { claim: event.target.value as TroubleshootingClaim, draft: undefined, requestId: undefined })}>
				{Object.keys(TROUBLESHOOTING_CLAIMS).map((key) => <option key={key} value={key}>{t(`settings.harness.troubleshoot.claim.${key}`)}</option>)}
			</select>
		</label>
		<div className="flex flex-wrap gap-2">
			<Button size="sm" variant="outline" disabled={busy || installationBusy} onClick={() => void verify()}>{t(latest ? "settings.harness.troubleshoot.verify" : "settings.harness.troubleshoot.collect")}</Button>
			{latest ? <Button size="sm" variant="outline" disabled={busy} onClick={() => void copy(troubleshootingPrompt(claim, latest))}>{t("settings.harness.troubleshoot.copyPrompt")}</Button> : null}
		</div>
		{busy ? <p role="status">{t("settings.harness.troubleshoot.working")}</p> : null}
		{state?.error ? <p role="alert" className="text-error">{state.error}</p> : null}
		{latest ? <>
			<p role="status" className="text-settings-muted">{state?.verificationFailed ? t("settings.harness.troubleshoot.unresolved") : state?.after ? t(troubleshootingVerified(latest) ? "settings.harness.troubleshoot.verified" : "settings.harness.troubleshoot.unresolved") : t("settings.harness.troubleshoot.initial")}</p>
			<details><summary className="cursor-pointer">{t("settings.harness.troubleshoot.reviewDiagnostics")}</summary><pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded border border-border p-3 text-xs">{JSON.stringify(latest, null, 2)}</pre></details>
			{!session ? <>
				{helpers.length ? <label className="block space-y-2"><span>{t("settings.harness.troubleshoot.helper")}</span><select className="block w-full rounded border border-border bg-surface p-2" value={helper} disabled={busy} onChange={(event) => setHelperChoice(event.target.value as TroubleshootingHelper)}>{helpers.map((id) => <option key={id} value={id}>{agentLabel(id)}</option>)}</select></label> : <p className="text-settings-muted">{t("settings.harness.troubleshoot.noHelper")}</p>}
				<p className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.disclosure")}</p>
				<Button size="sm" disabled={busy || !helper || installationBusy || state?.verificationFailed} onClick={() => void start()}>{t("settings.harness.troubleshoot.start")}</Button>
			</> : <>
				<div className="flex flex-wrap items-center gap-2"><span>{session.title}</span><Button size="sm" variant="outline" disabled={busy || state?.stopped} onClick={() => void stop()}>{t(state?.stopped ? "settings.harness.troubleshoot.stopped" : "settings.harness.troubleshoot.stop")}</Button></div>
				<p className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.sessionLifetime")}</p>
				<div className="h-[min(600px,65vh)] min-h-80 overflow-hidden rounded border border-border bg-terminal" data-settings-inline-edit=""><TerminalPane session={session} terminalTarget={{ kind: "worker" }} theme={theme} daemonReady fontSize={12} onTerminalStateChange={(status) => { if (status === "exited") update(agentId, { stopped: true }); }} /></div>
			</>}
			<Button size="sm" variant="outline" disabled={busy} onClick={() => { if (state?.before) { setCopied(false); update(agentId, { draft: troubleshootingIssueDraft(claim, state.before, state.after, state.verificationFailed) }); } }}>{t(state?.draft === undefined ? "settings.harness.troubleshoot.draft" : "settings.harness.troubleshoot.regenerate")}</Button>
		</> : null}
		{state?.draft !== undefined ? <div className="space-y-2"><label className="block space-y-2"><span>{t("settings.harness.troubleshoot.reviewDraft")}</span><textarea className="block min-h-80 w-full rounded border border-border bg-surface p-3 font-mono text-xs" value={state.draft} onChange={(event) => { setCopied(false); update(agentId, { draft: event.target.value }); }} /></label><p className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.draftHelp")}</p><Button size="sm" variant="outline" disabled={busy} onClick={() => void copy(state.draft ?? "")}>{t("settings.harness.troubleshoot.copyDraft")}</Button></div> : null}
		{copied ? <p role="status">{t("settings.harness.troubleshoot.copied")}</p> : null}
	</section>;
}
