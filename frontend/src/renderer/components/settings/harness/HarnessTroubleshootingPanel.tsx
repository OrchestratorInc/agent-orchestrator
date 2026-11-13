import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Clipboard, ExternalLink, FileText, LoaderCircle, RefreshCw, ShieldCheck, Square, TerminalSquare } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import type { components } from "../../../../api/schema";
import type { AgentReadinessSnapshot } from "../../../hooks/useAgentReadinessQuery";
import { toWorkspaceSession, workspaceQueryKey } from "../../../hooks/useWorkspaceQuery";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../../lib/api-client";
import { subscribeArtifactFileChanges } from "../../../lib/artifact-file-events";
import { aoBridge } from "../../../lib/bridge";
import { STANDALONE_WORKSPACE_ID } from "../../../types/workspace";
import { useResolvedTheme } from "../../../stores/ui-store";
import { TerminalPane } from "../../TerminalPane";
import { ConfirmDialog } from "../../ConfirmDialog";
import { Button } from "../../ui/button";
import { HarnessStateText } from "./harness-ui";
import {
	collectTroubleshootingSnapshot, fetchTroubleshootingFindings, TROUBLESHOOTING_CLAIMS, TROUBLESHOOTING_HELPERS,
	troubleshootingFindingsQueryKey, troubleshootingIssueDraft, troubleshootingIssueUrl, troubleshootingNextStep, troubleshootingPrompt, troubleshootingReportAdvice,
	type TroubleshootingCheckStage, type TroubleshootingClaim, type TroubleshootingFindings, type TroubleshootingHelper, type TroubleshootingSession, type TroubleshootingSnapshot,
} from "./harness-troubleshooting";

type TroubleshootingState = {
	claim: TroubleshootingClaim;
	before?: TroubleshootingSnapshot;
	after?: TroubleshootingSnapshot;
	session?: TroubleshootingSession;
	/** The harness the launched helper runs, named in its findings. */
	helperHarness?: string;
	stopped?: boolean;
	busy?: boolean;
	operation?: "checking" | "starting" | "stopping" | "copying" | "opening";
	error?: string;
	draft?: string;
	draftSource?: string;
	// Keep the exact request until the daemon acknowledges its session. A lost
	// response must not turn a changed claim, helper or snapshot into another launch.
	launchRequest?: components["schemas"]["SpawnSessionRequest"];
	// The daemon reported an earlier launch with this request ID as unfinished.
	launchIncomplete?: boolean;
	verificationFailed?: boolean;
	checkStage?: TroubleshootingCheckStage;
};
// Retain the helper and evidence across harness/tab switches and Settings visits.
// Memory only: never store diagnostic packages in localStorage.
export const useHarnessTroubleshooting = create<{ entries: Partial<Record<AgentId, TroubleshootingState>> }>(() => ({ entries: {} }));
function update(agentId: AgentId, patch: Partial<TroubleshootingState>) {
	useHarnessTroubleshooting.setState(({ entries }) => ({ entries: {
		...entries, [agentId]: { claim: "login", ...entries[agentId], ...patch },
	} }));
}

const actionClass = "h-auto min-h-8 max-w-full whitespace-normal py-1.5 text-left focus-visible:ring-2 focus-visible:ring-ring";
const summaryClass = "flex cursor-pointer list-none items-center gap-2 rounded text-xs text-settings-muted outline-none hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden";

/** The helper's own account of its investigation, labeled as a hypothesis rather than AO's verdict. */
function HelperFindings({ findings, status, failed, refreshing, onRefresh }: {
	findings?: TroubleshootingFindings; status?: "missing" | "invalid" | "ready"; failed: boolean; refreshing: boolean; onRefresh: () => void;
}) {
	const { t } = useTranslation();
	const list = (label: string, items: string[]) => items.length ? <div className="space-y-1"><p className="text-xs text-settings-muted">{label}</p><ul className="list-disc space-y-1 pl-5 text-xs leading-5 text-settings-label">{items.map((item, index) => <li key={index} className="break-words">{item}</li>)}</ul></div> : null;
	return <section className="space-y-3 rounded-lg border border-border bg-(--color-bg-secondary) p-4" aria-label={t("settings.harness.troubleshoot.findings.title")}>
		<div className="flex flex-wrap items-center justify-between gap-2">
			<h4 className="text-sm font-medium text-settings-label">{t("settings.harness.troubleshoot.findings.title")}</h4>
			<Button className={actionClass} size="sm" variant="ghost" disabled={refreshing} onClick={onRefresh}>{refreshing ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}{t("settings.harness.troubleshoot.findings.refresh")}</Button>
		</div>
		{findings ? <>
			<div className="space-y-1">
				<p className="font-medium text-settings-label">{t(`settings.harness.troubleshoot.findings.cause.${findings.cause}`)}</p>
				<p className="max-w-prose break-words text-xs leading-5 text-settings-label">{findings.summary}</p>
				<p className="max-w-prose text-xs leading-5 text-settings-muted">{t(`settings.harness.troubleshoot.findings.outcome.${findings.outcome}`)}</p>
			</div>
			{list(t("settings.harness.troubleshoot.findings.repairs"), findings.repairs)}
			{findings.evidence.length ? <details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.findings.evidence", { total: findings.evidence.length })}</summary><div className="mt-2">{list(t("settings.harness.troubleshoot.findings.evidenceList"), findings.evidence)}</div></details> : null}
			{findings.uncertainty ? <p className="max-w-prose break-words text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.findings.uncertainty", { text: findings.uncertainty })}</p> : null}
			<p className="max-w-prose text-caption text-settings-muted">{t("settings.harness.troubleshoot.findings.hypothesis")}</p>
		</> : <p className="max-w-prose text-xs leading-5 text-settings-muted">{t(failed ? "settings.harness.troubleshoot.findings.unavailable" : status === "invalid" ? "settings.harness.troubleshoot.findings.invalid" : "settings.harness.troubleshoot.findings.waiting")}</p>}
	</section>;
}

export function HarnessTroubleshootingPanel({ agentId, agents, installationBusy, onOpenAccount }: {
	agentId: AgentId; agents: AgentReadinessSnapshot[]; installationBusy: boolean; onOpenAccount: (id: AgentId) => void;
}) {
	const { i18n, t } = useTranslation();
	const queryClient = useQueryClient();
	const theme = useResolvedTheme();
	const state = useHarnessTroubleshooting((store) => store.entries[agentId]);
	const claim = state?.claim ?? (agents.find((agent) => agent.id === agentId)?.installation.state === "not_installed" ? "installation" : "login");
	const [helperChoice, setHelperChoice] = useState<TroubleshootingHelper>();
	const [copied, setCopied] = useState<"prompt" | "draft">();
	const [replacementDraft, setReplacementDraft] = useState<string>();
	const [pasteRequired, setPasteRequired] = useState(false);
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
	const helperSessionId = state?.session?.id;
	// The helper writes its findings as a session artifact; the artifact stream
	// rereads them as it works, and they remain readable after it stops.
	const findingsQuery = useQuery({
		queryKey: troubleshootingFindingsQueryKey(helperSessionId ?? ""),
		queryFn: ({ signal }) => fetchTroubleshootingFindings(helperSessionId ?? "", signal),
		enabled: Boolean(helperSessionId),
	});
	const helperRunning = Boolean(helperSessionId && !state?.stopped);
	useEffect(() => helperSessionId && helperRunning ? subscribeArtifactFileChanges(helperSessionId, queryClient) : undefined, [helperRunning, helperSessionId, queryClient]);
	const findings = findingsQuery.data?.status === "ready" ? findingsQuery.data.findings : undefined;
	const run = async (operation: NonNullable<TroubleshootingState["operation"]>, action: () => Promise<void>) => {
		if (useHarnessTroubleshooting.getState().entries[agentId]?.busy) return;
		update(agentId, { busy: true, operation, claim, error: undefined });
		try { await action(); }
		catch (error) { update(agentId, { error: error instanceof Error ? error.message : t("settings.harness.troubleshoot.failed") }); }
		finally { update(agentId, { busy: false, operation: undefined, checkStage: undefined }); }
	};
	const verify = () => run("checking", async () => {
		update(agentId, { verificationFailed: true, checkStage: "installation" });
		const version = await aoBridge.app.getVersion();
		const snapshot = await collectTroubleshootingSnapshot(agentId, queryClient, version, document.documentElement.dataset.aoPlatform ?? "unknown", (checkStage) => update(agentId, { checkStage }));
		const current = useHarnessTroubleshooting.getState().entries[agentId];
		update(agentId, { ...(current?.before ? { after: snapshot } : { before: snapshot }), verificationFailed: false });
		if (current?.session) void queryClient.invalidateQueries({ queryKey: troubleshootingFindingsQueryKey(current.session.id) });
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
		update(agentId, { launchRequest: request, launchIncomplete: false });
		// A thrown request is a transport failure: the helper may have started,
		// so the exact request stays pinned for Retry.
		const { data, error } = await apiClient.POST("/api/v1/sessions", { body: request });
		if (error || !data) {
			// A coded error is the daemon's answer that this request did not start a
			// helper, so release it for a new launch. An unfinished earlier launch
			// with the same ID may still complete: keep Retry and offer a new launch.
			const code = apiErrorCode(error);
			if (code === "CLIENT_REQUEST_INCOMPLETE") update(agentId, { launchIncomplete: true });
			else if (code) update(agentId, { launchRequest: undefined });
			throw new Error(apiErrorMessage(error, t("settings.harness.troubleshoot.failed")));
		}
		update(agentId, { session: data.session, helperHarness: request.harness, stopped: data.session.isTerminated, launchRequest: undefined, launchIncomplete: false });
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
		const helperName = state.helperHarness ? agentLabel(state.helperHarness as AgentId) : undefined;
		const draft = troubleshootingIssueDraft(claim, state.before, state.after, state.verificationFailed, findings, helperName);
		if (draftOpen && state.draft !== state.draftSource) { setReplacementDraft(draft); return; }
		setCopied(undefined);
		setPasteRequired(false);
		update(agentId, { draft, draftSource: draft });
	};
	// Opens GitHub's new-issue page for the user to review and submit; AO never files it.
	const openIssue = () => run("opening", async () => {
		const draft = useHarnessTroubleshooting.getState().entries[agentId]?.draft ?? "";
		const issue = troubleshootingIssueUrl(draft);
		if (issue.pasteRequired) {
			await aoBridge.clipboard.writeText(draft);
			setCopied("draft");
		}
		setPasteRequired(issue.pasteRequired);
		await aoBridge.app.openExternal(issue.url);
	});
	const adviceFromChecks = latest ? troubleshootingReportAdvice(latest, findings, Boolean(state?.session && state.after)) : undefined;
	// Earlier passing checks do not confirm the current state after a failed recheck.
	const reportAdvice = state?.verificationFailed && adviceFromChecks === "not_needed" ? "optional" : adviceFromChecks;
	const session = state?.session ? toWorkspaceSession(state.session, { id: STANDALONE_WORKSPACE_ID, name: "" }) : undefined;
	const checking = state?.operation === "checking";
	const progress = checking ? t(`settings.harness.troubleshoot.progress.${state?.checkStage ?? "installation"}`) : state?.operation === "starting" ? t("settings.harness.troubleshoot.startingProgress") : state?.operation === "stopping" ? t("settings.harness.troubleshoot.stoppingProgress") : undefined;
	const nextStep = latest ? troubleshootingNextStep(latest, state?.verificationFailed) : undefined;
	const installationOK = latest?.installationVerification === "succeeded" && latest.installation.state === "installed" && latest.installation.freshness === "fresh";
	const authenticationOK = latest?.authentication.freshness === "fresh" && ["authorized", "not_applicable"].includes(latest.authentication.state);
	const authState = latest?.authentication.state;
	const selectClass = "block w-full min-w-0 rounded-md border border-border bg-surface px-3 py-2 text-sm text-settings-label outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60";
	return <section className="space-y-5 pb-3 text-sm" aria-label={t("settings.harness.troubleshoot.title")}>
		<p className="max-w-prose text-pretty text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.description")}</p>
		<p className="flex max-w-prose items-start gap-2 text-xs leading-5 text-settings-muted"><ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />{t("settings.harness.troubleshoot.localChecks")}</p>
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
			{progress && checking ? <p role="status" className="flex items-center gap-2 text-xs text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{progress}</p> : null}
			{latest ? <>
				<dl className="grid gap-2 border-t border-border pt-3 text-xs sm:grid-cols-2">
					<div className="space-y-1"><dt className="text-settings-muted">{t("settings.harness.installation")}</dt><dd><HarnessStateText tone={!state?.verificationFailed && installationOK ? "ok" : "off"}>{t(installationOK ? "settings.harness.installed" : latest.installation.state === "not_installed" ? "settings.harness.notInstalled" : "settings.harness.installationUnknown")}</HarnessStateText></dd></div>
					<div className="space-y-1"><dt className="text-settings-muted">{t("settings.harness.health.authentication")}</dt><dd><HarnessStateText tone={!state?.verificationFailed && authenticationOK ? "ok" : "off"}>{t(latest.authentication.freshness !== "fresh" ? "settings.harness.loginUnknown" : authState === "authorized" ? "settings.harness.loggedIn" : authState === "not_applicable" ? "settings.harness.troubleshoot.authNotRequired" : authState === "unauthorized" ? "settings.harness.notLoggedIn" : authState === "configured" ? "settings.harness.health.authUnverified" : "settings.harness.loginUnknown")}</HarnessStateText></dd></div>
				</dl>
				<p className="text-caption text-settings-muted">{t("settings.harness.health.lastChecked")}: <time dateTime={latest.capturedAt}>{new Date(latest.capturedAt).toLocaleString(i18n.resolvedLanguage, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time></p>
				{!checking && nextStep ? <div className="space-y-2 rounded-md border border-border bg-surface p-3" role="status">
					<p className="font-medium text-settings-label">{t(`settings.harness.troubleshoot.result.${nextStep}`)}</p>
					<p className="max-w-prose text-xs leading-5 text-settings-muted">{t(`settings.harness.troubleshoot.next.${nextStep}`)}</p>
					{nextStep === "installation" || nextStep === "login" ? <Button className={actionClass} size="sm" variant="outline" disabled={busy || installationBusy} onClick={() => onOpenAccount(agentId)}>{t("settings.harness.troubleshoot.openSetup", { agent: agentLabel(agentId) })}</Button> : null}
				</div> : null}
				<details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 transition-transform group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.reviewDiagnostics")}</summary><pre className="mt-3 max-h-52 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-surface p-3 text-xs">{JSON.stringify(latest, null, 2)}</pre></details>
			</> : null}
		</section>
		{progress && !checking ? <p role="status" className="flex items-center gap-2 text-xs text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{progress}</p> : null}
		{state?.error ? <div role="alert" className="space-y-2 rounded-md border border-error/25 bg-error/5 p-3 text-xs leading-5"><p className="text-error">{t("settings.harness.troubleshoot.failed")}</p><details><summary className={summaryClass}>{t("settings.harness.troubleshoot.reviewDiagnostics")}</summary><p className="mt-2 break-words text-settings-muted">{state.error}</p></details></div> : null}
		<section className="space-y-3" aria-label={t("settings.harness.troubleshoot.stepHelper")}>
			<h3 className="flex items-center gap-2 text-sm font-medium text-settings-label"><span className="grid size-5 shrink-0 place-items-center rounded-full border border-border text-xs text-settings-muted" aria-hidden="true">2</span>{t("settings.harness.troubleshoot.stepHelper")}</h3>
			{!latest ? <div className="space-y-3 rounded-lg border border-dashed border-border p-4">
				<p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.checksFirst")}</p>
				{/* A failed first check leaves no evidence for a helper or prompt, but setup needs none. */}
				{state?.error ? <Button className={actionClass} size="sm" variant="outline" disabled={busy || installationBusy} onClick={() => onOpenAccount(agentId)}>{t("settings.harness.troubleshoot.openSetup", { agent: agentLabel(agentId) })}</Button> : null}
			</div> : <>
				{!session || state?.stopped ? <div className="space-y-3 rounded-lg border border-border bg-(--color-bg-secondary) p-4">
					{pendingLaunch ? <div className="space-y-2"><p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.pendingLaunch")}</p><details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.reviewPrompt")}</summary><pre className="mt-2 max-h-52 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-surface p-3 text-xs">{pendingLaunch.prompt}</pre></details></div>
						: helpers.length ? <label className="block space-y-1.5"><span className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.helper")}</span><select className={selectClass} value={helper} disabled={busy} onChange={(event) => setHelperChoice(event.target.value as TroubleshootingHelper)}>{helpers.map((id) => <option key={id} value={id}>{agentLabel(id)}</option>)}</select></label>
							: <div className="space-y-3"><p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.noHelper")}</p><div className="flex flex-wrap gap-2">{TROUBLESHOOTING_HELPERS.map((id) => <Button key={id} className={actionClass} size="sm" variant="outline" disabled={busy || installationBusy} onClick={() => onOpenAccount(id)}>{t("settings.harness.troubleshoot.openSetup", { agent: agentLabel(id) })}</Button>)}</div><p className="text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.returnAfterSetup")}</p></div>}
					<p className="flex items-start gap-2 text-xs leading-5 text-settings-muted"><ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />{t("settings.harness.troubleshoot.sharingSummary")}</p>
					<details className="group"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.sharingDetails")}</summary><p className="mt-2 text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.disclosure")}</p></details>
					{pendingLaunch && state?.launchIncomplete ? <p className="max-w-prose text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.launchIncomplete")}</p> : null}
					{pendingLaunch || helper ? <div className="flex flex-wrap gap-2">
						<Button className={actionClass} size="sm" disabled={busy || (!pendingLaunch && (installationBusy || state?.verificationFailed))} onClick={() => void start()}>{state?.operation === "starting" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <TerminalSquare aria-hidden="true" />}{t(pendingLaunch ? "settings.harness.troubleshoot.retryLaunch" : "settings.harness.troubleshoot.start")}</Button>
						{pendingLaunch && state?.launchIncomplete ? <Button className={actionClass} size="sm" variant="secondary" disabled={busy} onClick={() => update(agentId, { launchRequest: undefined, launchIncomplete: false, error: undefined })}>{t("settings.harness.troubleshoot.newLaunch")}</Button> : null}
					</div> : null}
				</div> : null}
				{session ? <div className="rounded-lg border border-border bg-(--color-bg-secondary)">
					<div className="sticky top-0 z-10 flex flex-wrap items-center justify-between gap-2 rounded-t-lg border-b border-border bg-(--color-bg-secondary) px-3 py-2.5" data-testid="helper-controls">
						<span className="flex min-w-0 items-center gap-2 text-xs font-medium text-settings-label"><TerminalSquare className="size-4 shrink-0" aria-hidden="true" />{session.title}</span>
						<div className="flex flex-wrap gap-2"><Button className={actionClass} size="sm" variant="outline" disabled={busy || state?.stopped} onClick={() => void stop()}><Square aria-hidden="true" />{t(state?.stopped ? "settings.harness.troubleshoot.stopped" : "settings.harness.troubleshoot.stop")}</Button><Button className={actionClass} size="sm" disabled={busy || installationBusy} onClick={() => void verify()}>{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}{t("settings.harness.troubleshoot.verify")}</Button></div>
					</div>
					<div className="h-[min(540px,55vh)] min-h-80 overflow-hidden rounded-b-lg bg-terminal" data-settings-inline-edit=""><TerminalPane key={session.id} session={session} terminalTarget={{ kind: "worker" }} theme={theme} daemonReady fontSize={12} onTerminalStateChange={(status) => { if (status === "exited" && useHarnessTroubleshooting.getState().entries[agentId]?.session?.id === session.id) update(agentId, { stopped: true }); }} /></div>
				</div> : null}
				{session ? <HelperFindings findings={findings} status={findingsQuery.data?.status} failed={findingsQuery.isError} refreshing={findingsQuery.isFetching} onRefresh={() => void findingsQuery.refetch()} /> : null}
				<div className="flex flex-wrap items-center gap-x-4 gap-y-2">
					<Button className={actionClass} size="sm" variant={!helper && !session && !pendingLaunch ? "primary" : "ghost"} aria-label={t("settings.harness.troubleshoot.copyPrompt")} disabled={busy} onClick={() => void copy("prompt", troubleshootingPrompt(claim, latest))}>{copied === "prompt" ? <Check aria-hidden="true" /> : <Clipboard aria-hidden="true" />}<span aria-live="polite">{t(copied === "prompt" ? "settings.harness.troubleshoot.copied" : "settings.harness.troubleshoot.copyPrompt")}</span></Button>
					{session ? <details className="group max-w-prose"><summary className={summaryClass}><ChevronDown className="size-3.5 shrink-0 group-open:rotate-180" aria-hidden="true" />{t("settings.harness.troubleshoot.sessionDetails")}</summary><p className="mt-2 text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.sessionLifetime")}</p></details> : null}
				</div>
			</>}
		</section>
		{latest ? <section className="space-y-3 border-t border-border pt-4" aria-label={t("settings.harness.troubleshoot.stepReport")}>
			<div className="flex flex-wrap items-center justify-between gap-3"><h3 className="flex items-center gap-2 text-sm font-medium text-settings-label"><FileText className="size-4 text-settings-muted" aria-hidden="true" />{t("settings.harness.troubleshoot.stepReport")}</h3><Button className={actionClass} size="sm" variant={reportAdvice === "recommended" && !draftOpen ? "primary" : reportAdvice === "not_needed" ? "ghost" : "secondary"} disabled={busy} onClick={prepareDraft}>{t(draftOpen ? "settings.harness.troubleshoot.regenerate" : "settings.harness.troubleshoot.draft")}</Button></div>
			{reportAdvice ? <p className={reportAdvice === "recommended" ? "max-w-prose rounded-md border border-border bg-surface p-3 text-xs leading-5 text-settings-label" : "max-w-prose text-xs leading-5 text-settings-muted"} data-advice={reportAdvice}>{t(`settings.harness.troubleshoot.report.${reportAdvice}`)}</p> : null}
			{draftOpen ? <><label className="block space-y-1.5"><span className="text-xs text-settings-muted">{t("settings.harness.troubleshoot.reviewDraft")}</span><textarea ref={draftRef} className="block min-h-64 w-full rounded-md border border-border bg-surface p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring" value={state?.draft} onChange={(event) => { setCopied(undefined); setPasteRequired(false); update(agentId, { draft: event.target.value }); }} /></label><p className="max-w-prose text-xs leading-5 text-settings-muted">{t("settings.harness.troubleshoot.draftHelp")}</p>
				<div className="flex flex-wrap items-center gap-2">
					<Button className={actionClass} size="sm" variant="accent" disabled={busy || !state?.draft?.trim()} onClick={() => void openIssue()}>{state?.operation === "opening" ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : <ExternalLink aria-hidden="true" />}{t("settings.harness.troubleshoot.openIssue")}</Button>
					<Button className={actionClass} size="sm" variant="secondary" aria-label={t("settings.harness.troubleshoot.copyDraft")} disabled={busy} onClick={() => void copy("draft", state?.draft ?? "")}>{copied === "draft" ? <Check aria-hidden="true" /> : <Clipboard aria-hidden="true" />}<span aria-live="polite">{t(copied === "draft" ? "settings.harness.troubleshoot.copied" : "settings.harness.troubleshoot.copyDraft")}</span></Button>
				</div>
				<p className="max-w-prose text-xs leading-5 text-settings-muted" aria-live="polite">{t(pasteRequired ? "settings.harness.troubleshoot.issuePasteRequired" : "settings.harness.troubleshoot.openIssueHelp")}</p></> : null}
		</section> : null}
		<ConfirmDialog open={replacementDraft !== undefined} title={t("settings.harness.troubleshoot.replaceDraftTitle")} description={t("settings.harness.troubleshoot.replaceDraftDescription")} confirmLabel={t("settings.harness.troubleshoot.regenerate")} onOpenChange={(open) => { if (!open) setReplacementDraft(undefined); }} onConfirm={() => { if (replacementDraft !== undefined) { update(agentId, { draft: replacementDraft, draftSource: replacementDraft }); setCopied(undefined); setReplacementDraft(undefined); } }} />
	</section>;
}
