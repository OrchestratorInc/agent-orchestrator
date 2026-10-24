import { useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Clipboard, FileText, LoaderCircle, RefreshCw, ShieldCheck, Square, TerminalSquare } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import type { components } from "../../../../api/schema";
import type { AgentReadinessSnapshot } from "../../../hooks/useAgentReadinessQuery";
import { toWorkspaceSession, workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiClient, apiErrorMessage } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import { STANDALONE_WORKSPACE_ID } from "../../../types/workspace";
import { useResolvedTheme } from "../../../stores/ui-store";
import { TerminalPane } from "../../TerminalPane";
import { ConfirmDialog } from "../../ConfirmDialog";
import { Button } from "../../ui/button";
import { HarnessStateText } from "./harness-ui";
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
	operation?: "checking" | "starting" | "stopping" | "copying";
	error?: string;
	draft?: string;
	draftSource?: string;
	// Keep the exact request until the daemon acknowledges its session. A lost
	// response must not turn a changed claim, helper or snapshot into another launch.
	launchRequest?: components["schemas"]["SpawnSessionRequest"];
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
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const theme = useResolvedTheme();
	const state = useHarnessTroubleshooting((store) => store.entries[agentId]);
	const claim = state?.claim ?? (agents.find((agent) => agent.id === agentId)?.installation.state === "not_installed" ? "installation" : "login");
	const [helperChoice, setHelperChoice] = useState<TroubleshootingHelper>();
	const [copied, setCopied] = useState<"prompt" | "draft">();
	const [replacementDraft, setReplacementDraft] = useState<string>();
	const draftRef = useRef<HTMLTextAreaElement>(null);
	const draftOpen = state?.draft !== undefined;
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(undefined), 2500);
		return () => window.clearTimeout(timer);
	}, [copied, agentId]);
	useEffect(() => { if (draftOpen) draftRef.current?.focus(); }, [draftOpen, agentId]);
	const helpers = TROUBLESHOOTING_HELPERS.filter((id) => agents.some((agent) => agent.id === id
		&& agent.installation.state === "installed" && agent.effectiveReadiness === "ready"
		&& agent.authentication.freshness === "fresh"
		&& ["authorized", "not_applicable"].includes(agent.authentication.state)));
	const helper = helpers.find((id) => id === helperChoice) ?? helpers.find((id) => id !== agentId) ?? helpers[0];
	const busy = Boolean(state?.busy);
	const pendingLaunch = state?.launchRequest;
	const latest = state?.after ?? state?.before;
	const run = async (operation: NonNullable<TroubleshootingState["operation"]>, action: () => Promise<void>) => {
		if (useHarnessTroubleshooting.getState().entries[agentId]?.busy) return;
		update(agentId, { busy: true, operation, claim, error: undefined });
		try { await action(); }
		catch (error) { update(agentId, { error: error instanceof Error ? error.message : t("settings.harness.troubleshoot.failed") }); }
		finally { update(agentId, { busy: false, operation: undefined }); }
	};
	const verify = () => run("checking", async () => {
		update(agentId, { verificationFailed: true });
		const version = await aoBridge.app.getVersion();
		const snapshot = await collectTroubleshootingSnapshot(agentId, queryClient, version, document.documentElement.dataset.aoPlatform ?? "unknown");
		const current = useHarnessTroubleshooting.getState().entries[agentId];
		update(agentId, { ...(current?.before ? { after: snapshot } : { before: snapshot }), verificationFailed: false });
	});
	const start = () => run("starting", async () => {
		const current = useHarnessTroubleshooting.getState().entries[agentId];
		if (current?.session && !current.stopped) return;
		let request = current?.launchRequest;
		if (!request) {
			if (!helper || !latest) return;
			request = {
				clientRequestId: crypto.randomUUID(), kind: "worker", harness: helper, mode: "tui", approvalMode: "accept-edits",
				displayName: `Troubleshoot ${agentLabel(agentId)}`, prompt: troubleshootingPrompt(claim, latest),
			};
		}
		update(agentId, { launchRequest: request });
		const { data, error } = await apiClient.POST("/api/v1/sessions", { body: request });
		if (error || !data) throw new Error(apiErrorMessage(error, t("settings.harness.troubleshoot.failed")));
		update(agentId, { session: data.session, stopped: data.session.isTerminated, launchRequest: undefined });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	});
	const stop = () => run("stopping", async () => {
		if (!state?.session) return;
		const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", { params: { path: { sessionId: state.session.id } } });
		if (error) throw new Error(apiErrorMessage(error, t("settings.harness.troubleshoot.failed")));
		update(agentId, { stopped: true });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	});
	const copy = (target: "prompt" | "draft", text: string) => run("copying", async () => {
		await aoBridge.clipboard.writeText(text);
		setCopied(target);
	});
	const prepareDraft = () => {
		if (!state?.before) return;
		const draft = troubleshootingIssueDraft(claim, state.before, state.after, state.verificationFailed);
		if (draftOpen && state.draft !== state.draftSource) { setReplacementDraft(draft); return; }
		setCopied(undefined);
		update(agentId, { draft, draftSource: draft });
	};
	const session = state?.session ? toWorkspaceSession(state.session, { id: STANDALONE_WORKSPACE_ID, name: "" }) : undefined;
	const checking = state?.operation === "checking";
	const progress = checking ? t("settings.harness.troubleshoot.checkingProgress") : state?.operation === "starting" ? t("settings.harness.troubleshoot.startingProgress") : state?.operation === "stopping" ? t("settings.harness.troubleshoot.stoppingProgress") : undefined;
	const installationOK = latest?.installationVerification === "succeeded" && latest.installation.state === "installed" && latest.installation.freshness === "fresh";
	const authenticationOK = latest?.authentication.freshness === "fresh" && ["authorized", "not_applicable"].includes(latest.authentication.state);
	const authState = latest?.authentication.state;
	const selectClass = "block w-full min-w-0 rounded-md border border-border bg-surface px-3 py-2 text-sm text-settings-label outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60";
	const actionClass = "h-auto min-h-8 max-w-full whitespace-normal py-1.5 text-left focus-visible:ring-2 focus-visible:ring-ring";
	const summaryClass = "flex cursor-pointer list-none items-center gap-2 rounded text-xs text-settings-muted outline-none hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden";
	return <section className="space-y-5 pb-3 text-sm" aria-label={t("settings.harness.troubleshoot.title")}>
		<p className="max-w-prose text-pretty text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.description")}</p>
		<section className="space-y-3 rounded-lg border border-border bg-(--color-bg-secondary) p-4" aria-label={t("settings.harness.troubleshoot.stepCheck")}>
			<div className="flex flex-wrap items-center justify-between gap-2"><h3 className="flex items-center gap-2 text-sm font-medium text-settings-label"><span className="grid size-5 shrink-0 place-items-center rounded-full border border-border text-xs text-settings-muted" aria-hidden="true">1</span>{t("settings.harness.troubleshoot.stepCheck")}</h3>
				{latest && !session ? <Button className={actionClass} size="sm" variant="outline" disabled={busy || installationBusy} onClick={() => void verify()}>{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}{t("settings.harness.troubleshoot.verify")}</Button> : null}
			</div>
			<label className="block space-y-1.5"><span className={latest ? "sr-only" : "text-xs text-settings-muted"}>{t("settings.harness.troubleshoot.observation")}</span>
				<select className={selectClass} value={claim} disabled={busy || Boolean(session && !state?.stopped) || Boolean(pendingLaunch)} onChange={(event) => { setCopied(undefined); update(agentId, { claim: event.target.value as TroubleshootingClaim }); }}>
					{(Object.keys(TROUBLESHOOTING_CLAIMS) as TroubleshootingClaim[]).map((key) => <option key={key} value={key}>{t(`settings.harness.troubleshoot.claim.${key}`)}</option>)}
				</select>
			</label>
			{!latest ? <Button className={actionClass} size="sm" disabled={busy || installationBusy} onClick={() => void verify()}>{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}{t("settings.harness.troubleshoot.collect")}</Button> : null}
			{latest ? <>
				<dl className="grid gap-2 border-t border-border pt-3 text-xs sm:grid-cols-2">
					<div className="space-y-1"><dt className="text-settings-muted">{t("settings.harness.installation")}</dt><dd><HarnessStateText tone={!state?.verificationFailed && installationOK ? "ok" : "off"}>{t(installationOK ? "settings.harness.installed" : latest.installation.state === "not_installed" ? "settings.harness.notInstalled" : "settings.harness.installationUnknown")}</HarnessStateText></dd></div>
					<div className="space-y-1"><dt className="text-settings-muted">{t("settings.harness.health.authentication")}</dt><dd><HarnessStateText tone={!state?.verificationFailed && authenticationOK ? "ok" : "off"}>{t(latest.authentication.freshness !== "fresh" ? "settings.harness.loginUnknown" : authState === "authorized" ? "settings.harness.loggedIn" : authState === "not_applicable" ? "settings.harness.troubleshoot.authNotRequired" : authState === "unauthorized" ? "settings.harness.notLoggedIn" : authState === "configured" ? "settings.harness.health.authUnverified" : "settings.harness.loginUnknown")}</HarnessStateText></dd></div>
				</dl>
				<p className="text-caption text-settings-muted">{t("settings.harness.health.lastChecked")}: <time dateTime={latest.capturedAt}>{new Date(latest.capturedAt).toLocaleString(i18n.resolvedLanguage, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time></p>
				{!checking && (state?.after || state?.verificationFailed || !session) ? <p role="status" className={state?.after || state?.verificationFailed ? "text-xs leading-5 text-settings-muted" : "sr-only"}>{state?.verificationFailed ? t("settings.harness.troubleshoot.unresolved") : state?.after ? t(troubleshootingVerified(latest) ? "settings.harness.troubleshoot.verified" : "settings.harness.troubleshoot.unresolved") : t("settings.harness.troubleshoot.initial")}</p> : null}
				<details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 transition-transform group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.reviewDiagnostics")}</summary><pre className="mt-3 max-h-52 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-surface p-3 text-xs">{JSON.stringify(latest, null, 2)}</pre></details>
			</> : null}
		</section>
		{progress ? <p role="status" className="flex items-center gap-2 text-xs text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{progress}</p> : null}
		{state?.error ? <p role="alert" className="rounded-md border border-error/25 bg-error/5 p-3 text-xs leading-5 text-error">{state.error}</p> : null}
		<section className="space-y-3" aria-label={t("settings.harness.troubleshoot.stepHelper")}>
			<h3 className="flex items-center gap-2 text-sm font-medium text-settings-label"><span className="grid size-5 shrink-0 place-items-center rounded-full border border-border text-xs text-settings-muted" aria-hidden="true">2</span>{t("settings.harness.troubleshoot.stepHelper")}</h3>
			{!latest ? <p className="rounded-lg border border-dashed border-border p-4 text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.checksFirst")}</p> : <>
				{!session || state?.stopped ? <div className="space-y-3 rounded-lg border border-border bg-(--color-bg-secondary) p-4">
					{pendingLaunch ? <div className="space-y-2"><p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.pendingLaunch")}</p><details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.reviewPrompt")}</summary><pre className="mt-2 max-h-52 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-surface p-3 text-xs">{pendingLaunch.prompt}</pre></details></div>
						: helpers.length ? <label className="block space-y-1.5"><span className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.helper")}</span><select className={selectClass} value={helper} disabled={busy} onChange={(event) => setHelperChoice(event.target.value as TroubleshootingHelper)}>{helpers.map((id) => <option key={id} value={id}>{agentLabel(id)}</option>)}</select></label>
							: <p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.noHelper")}</p>}
					<p className="flex items-start gap-2 text-xs leading-5 text-settings-muted"><ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />{t("settings.harness.troubleshoot.sharingSummary")}</p>
					<details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.sharingDetails")}</summary><p className="mt-2 text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.disclosure")}</p></details>
					<Button className={actionClass} size="sm" disabled={busy || (!pendingLaunch && (!helper || installationBusy || state?.verificationFailed))} onClick={() => void start()}>{state?.operation === "starting" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <TerminalSquare aria-hidden="true" />}{t(pendingLaunch ? "settings.harness.troubleshoot.retryLaunch" : "settings.harness.troubleshoot.start")}</Button>
				</div> : null}
				{session ? <div className="rounded-lg border border-border bg-(--color-bg-secondary)">
					<div className="sticky top-0 z-10 flex flex-wrap items-center justify-between gap-2 rounded-t-lg border-b border-border bg-(--color-bg-secondary) px-3 py-2.5" data-testid="helper-controls">
						<span className="flex min-w-0 items-center gap-2 text-xs font-medium text-settings-label"><TerminalSquare className="size-4 shrink-0" aria-hidden="true" />{session.title}</span>
						<div className="flex flex-wrap gap-2"><Button className={actionClass} size="sm" variant="outline" disabled={busy || state?.stopped} onClick={() => void stop()}><Square aria-hidden="true" />{t(state?.stopped ? "settings.harness.troubleshoot.stopped" : "settings.harness.troubleshoot.stop")}</Button><Button className={actionClass} size="sm" disabled={busy || installationBusy} onClick={() => void verify()}>{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}{t("settings.harness.troubleshoot.verify")}</Button></div>
					</div>
					<div className="h-[min(540px,55vh)] min-h-80 overflow-hidden rounded-b-lg bg-terminal" data-settings-inline-edit=""><TerminalPane key={session.id} session={session} terminalTarget={{ kind: "worker" }} theme={theme} daemonReady fontSize={12} onTerminalStateChange={(status) => { if (status === "exited" && useHarnessTroubleshooting.getState().entries[agentId]?.session?.id === session.id) update(agentId, { stopped: true }); }} /></div>
				</div> : null}
				<div className="flex flex-wrap items-center gap-x-4 gap-y-2">
					<Button className={actionClass} size="sm" variant="ghost" aria-label={t("settings.harness.troubleshoot.copyPrompt")} disabled={busy} onClick={() => void copy("prompt", troubleshootingPrompt(claim, latest))}>{copied === "prompt" ? <Check aria-hidden="true" /> : <Clipboard aria-hidden="true" />}<span aria-live="polite">{t(copied === "prompt" ? "settings.harness.troubleshoot.copied" : "settings.harness.troubleshoot.copyPrompt")}</span></Button>
					{session ? <details className="group max-w-prose"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.sessionDetails")}</summary><p className="mt-2 text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.sessionLifetime")}</p></details> : null}
				</div>
			</>}
		</section>
		{latest ? <section className="space-y-3 border-t border-border pt-4" aria-label={t("settings.harness.troubleshoot.stepReport")}>
			<div className="flex flex-wrap items-center justify-between gap-3"><h3 className="flex items-center gap-2 text-sm font-medium text-settings-label"><FileText className="size-4 text-settings-muted" aria-hidden="true" />{t("settings.harness.troubleshoot.stepReport")}</h3><Button className={actionClass} size="sm" variant="outline" disabled={busy} onClick={prepareDraft}>{t(draftOpen ? "settings.harness.troubleshoot.regenerate" : "settings.harness.troubleshoot.draft")}</Button></div>
			{draftOpen ? <><label className="block space-y-1.5"><span className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.reviewDraft")}</span><textarea ref={draftRef} className="block min-h-64 w-full rounded-md border border-border bg-surface p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring" value={state?.draft} onChange={(event) => { setCopied(undefined); update(agentId, { draft: event.target.value }); }} /></label><p className="max-w-prose text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.draftHelp")}</p><Button className={actionClass} size="sm" variant="outline" aria-label={t("settings.harness.troubleshoot.copyDraft")} disabled={busy} onClick={() => void copy("draft", state?.draft ?? "")}>{copied === "draft" ? <Check aria-hidden="true" /> : <Clipboard aria-hidden="true" />}<span aria-live="polite">{t(copied === "draft" ? "settings.harness.troubleshoot.copied" : "settings.harness.troubleshoot.copyDraft")}</span></Button></> : null}
		</section> : null}
		<ConfirmDialog open={replacementDraft !== undefined} title={t("settings.harness.troubleshoot.replaceDraftTitle")} description={t("settings.harness.troubleshoot.replaceDraftDescription")} confirmLabel={t("settings.harness.troubleshoot.regenerate")} onOpenChange={(open) => { if (!open) setReplacementDraft(undefined); }} onConfirm={() => { if (replacementDraft !== undefined) { update(agentId, { draft: replacementDraft, draftSource: replacementDraft }); setCopied(undefined); setReplacementDraft(undefined); } }} />
	</section>;
}
