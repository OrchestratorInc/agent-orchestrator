import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, LoaderCircle, X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAgentAuthPlans } from "../hooks/useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { fetchInstallers, fetchInstallJobs, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installerQueryKey, installJobsQueryKey, maintenanceMethodId, updateAdvisoryQueryKey, useHarnessActionRequest, useHarnessUpdates, versionLabel } from "../hooks/useHarnessUpdates";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { Button } from "./ui/button";

const DISMISSED_KEY = "ao.harness-update-dismissals.v1";
const activeJob = (status?: string) => status === "running" || status === "installing" || status === "verifying";

function readDismissals(): Set<string> {
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((entry): entry is string => typeof entry === "string").slice(-100) : []);
	} catch { return new Set(); }
}

/** Always mounted in the shell, not in a route or Settings. Read-only checks
 * share the page's cache; an explicit click hands off to its existing workflow. */
export function HarnessUpdateNotice({ enabled }: { enabled: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const readiness = useAgentReadinessQuery(enabled);
	const readinessComplete = useEnsureAgentReadiness({ enabled, retryOnError: true });
	const installed = useMemo(() => new Set<AgentId>((readiness.data?.agents ?? [])
		.filter((agent) => agent.installation.state === "installed").map((agent) => agent.id as AgentId)), [readiness.data]);
	const advisories = useHarnessUpdates(installed, undefined, readinessComplete && !readiness.isError);
	const installers = useQuery({ queryKey: installerQueryKey, queryFn: () => fetchInstallers(), enabled, staleTime: 60_000 });
	const authPlans = useAgentAuthPlans(undefined, enabled);
	const jobs = useQuery({ queryKey: installJobsQueryKey, queryFn: () => fetchInstallJobs(), enabled, retry: false,
		refetchInterval: (query) => query.state.data?.some((job) => activeJob(job.status)) ? 1_000 : false });
	const observedJobs = useRef(new Set<string>());
	const [dismissed, setDismissed] = useState(readDismissals);
	const [expanded, setExpanded] = useState(false);
	const stackId = useId();
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const settingsModal = useUiStore((state) => state.settingsModal);
	const pendingRequest = useHarnessActionRequest((state) => state.request);
	useEffect(() => {
		if (pendingRequest && (!enabled || settingsModal?.scope !== "global" || settingsModal.section !== "harness")) {
			useHarnessActionRequest.getState().setRequest(null);
		}
	}, [enabled, settingsModal, pendingRequest]);

	useEffect(() => {
		if (!enabled) return;
		for (const job of jobs.data ?? []) {
			if (activeJob(job.status)) { observedJobs.current.add(job.target); continue; }
			if (!observedJobs.current.delete(job.target)) continue;
			// Refresh even on failure: a command can change the binary before
			// failing verification. Never keep advertising an obsolete version.
			void ensureAgentReadiness([job.target]).then(async (result) => {
				cacheAgentReadiness(queryClient, result);
				await queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(job.target as AgentId) });
			}).catch(() => undefined);
			void queryClient.invalidateQueries({ queryKey: installerQueryKey });
		}
	}, [enabled, jobs.data, queryClient]);

	if (!readinessComplete || readiness.isError) return null;
	// Let the first batch settle so a fast, seldom-used harness cannot jump
	// ahead of the most-used one. Failed lookups don't block the other results.
	if (AGENT_OPTIONS.some((id) => installed.has(id) && advisories.get(id)?.isPending)) return null;
	const readinessById = new Map(readiness.data?.agents.map((agent) => [agent.id, agent]));
	const updates = AGENT_OPTIONS.flatMap((agentId) => {
		const query = advisories.get(agentId);
		const advisory = query?.data;
		if (!installed.has(agentId) || query?.isError || !hasConfirmedHarnessUpdate(advisory)) return [];
		const key = harnessUpdateNoticeKey(agentId, advisory!.latestVersion!);
		return dismissed.has(key) ? [] : [{ agentId, advisory: advisory!, key }];
	}).sort((left, right) => {
		const a = readinessById.get(left.agentId);
		const b = readinessById.get(right.agentId);
		const usage = (b?.usageCount ?? 0) - (a?.usageCount ?? 0);
		const recent = (Date.parse(b?.lastUsedAt ?? "") || 0) - (Date.parse(a?.lastUsedAt ?? "") || 0);
		return usage || recent || agentLabel(left.agentId).localeCompare(agentLabel(right.agentId)) || left.agentId.localeCompare(right.agentId);
	});
	if (updates.length === 0) return null;
	const dismiss = (key: string) => {
		setDismissed((current) => {
			const next = new Set([...current, key].slice(-100));
			try { localStorage.setItem(DISMISSED_KEY, JSON.stringify([...next])); } catch { /* In-memory dismissal still works. */ }
			return next;
		});
		if (updates.length <= 2) setExpanded(false);
	};
	const isExpanded = expanded && updates.length > 1;

	return <aside role="region" aria-label={t("settings.harness.updateNoticeTitle")}
		className="fixed right-4 top-14 isolate z-[calc(var(--z-overlay)-1)] w-[min(300px,calc(100vw-32px))] text-card-foreground"
		data-browser-native-overlay="true" data-state="open">
		{/* The stack toggle sits above the cards so it never reads as part of one notice. */}
		{updates.length > 1 ? <div className="mb-5 flex justify-end">
			<Button type="button" variant="ghost" size="sm" className="h-6 rounded-full border border-border bg-card px-2 text-[11px] text-settings-muted shadow-sm hover:bg-muted" aria-expanded={isExpanded} aria-controls={stackId} onClick={() => setExpanded(!isExpanded)}>{t(isExpanded ? "settings.harness.hideUpdateStack" : "settings.harness.showUpdateStack", { count: updates.length })}</Button>
		</div> : null}
		{/* flow-root keeps the stack's negative margin inside this box, so the cards behind peek out below only. */}
		<div className="relative flow-root">
		{!isExpanded && updates.length > 1 ? <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-0">
			{updates.length > 2 ? <div className="absolute inset-x-4 -bottom-2 top-4 z-0 rounded-xl border border-border bg-card shadow-sm" /> : null}
			<div className="absolute inset-x-2 -bottom-1 top-2 z-10 rounded-xl border border-border bg-card shadow-sm" />
		</div> : null}
		<div id={stackId} className="relative z-10 -m-4 max-h-[min(272px,calc(100dvh-56px))] space-y-5 overflow-y-auto overscroll-contain p-4">
			{(isExpanded ? updates : updates.slice(0, 1)).map(({ agentId, advisory, key }) => {
				const plan = installers.data?.find((candidate) => candidate.agentId === agentId);
				const job = jobs.data?.find((candidate) => candidate.target === agentId);
				const method = plan?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
				const authPlan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
				const authStatus = readiness.data?.agents.find((agent) => agent.id === agentId)?.authentication.state;
				const needsLogin = Boolean(authPlan && authPlan.action !== "instructions" && authStatus === "unauthorized");
				const canUpdate = Boolean(method?.available && method.updateAvailable);
				const busy = activeJob(job?.status);
				const ready = !authPlans.isPending && !authPlans.isError && !installers.isPending && !installers.isError && !jobs.isPending && !jobs.isError;
				const requestAction = () => {
					useHarnessActionRequest.getState().setRequest({ agentId, latestVersion: advisory.latestVersion!, action: needsLogin ? "login" : "update" });
					openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true });
				};
				return <article key={key} aria-label={agentLabel(agentId)} className="relative rounded-xl border border-border bg-card px-2.5 py-2 shadow-lg">
					<Button type="button" variant="ghost" size="icon" className="absolute -right-1 -top-4 z-20 size-6 rounded-full border border-border bg-card shadow-sm hover:bg-muted" aria-label={t("settings.harness.dismissAgentUpdate", { agent: agentLabel(agentId) })} onClick={() => dismiss(key)}><X className="size-3.5" aria-hidden="true" /></Button>
					<div className="flex items-center gap-2">
						<AgentAvatar className="size-5 shrink-0" decorative provider={agentId} />
						<p className="min-w-0 flex-1 break-words text-[13px] font-medium">{agentLabel(agentId)}</p>
					</div>
					<div className="flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
						<p className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1 gap-y-1 text-[11px]"><span className="break-all font-mono text-settings-label" title={t("settings.harness.installedVersion")}>{versionLabel(advisory.currentVersion!)}</span><ArrowRight className="size-3 shrink-0 text-settings-muted" aria-label={t("settings.harness.availableVersion")} /><span className="break-all font-mono font-medium text-settings-accent">{versionLabel(advisory.latestVersion!)}</span></p>
					{busy ? <Button size="sm" variant="outline" className="h-7 min-w-16 shrink-0 px-2 text-xs" disabled><LoaderCircle className="size-3 animate-spin" aria-hidden="true" />{t("settings.harness.working")}</Button>
						: ready && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate)) ? <Button size="sm" className="h-7 min-w-16 shrink-0 px-2 text-xs" onClick={requestAction}>{t(needsLogin ? authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login" : "settings.harness.update")}</Button>
						: ready && !needsLogin && plan?.documentationUrl ? <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" title={method?.updateReason ?? t("settings.harness.ownershipUnknown")} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button>
						: <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true })}>{t("settings.harness.viewDetails")}</Button>}
					</div>
				</article>;
			})}
		</div>
		</div>
	</aside>;
}
