import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, LoaderCircle, X } from "lucide-react";
import { type CSSProperties, useEffect, useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useAgentAuthPlans } from "../hooks/useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { fetchInstallers, fetchInstallJobs, fetchUpdateAdvisory, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installerQueryKey, installJobsQueryKey, maintenanceMethodId, updateAdvisoryQueryKey, useHarnessActionRequest, useHarnessUpdates, versionLabel } from "../hooks/useHarnessUpdates";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
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
	// Updates started from a notice run right here; the notice shows progress
	// and any failure instead of sending the user to Settings.
	const [starting, setStarting] = useState<ReadonlySet<AgentId>>(new Set());
	const startingRef = useRef(new Set<AgentId>());
	const updateAllInFlight = useRef(false);
	const [startingAll, setStartingAll] = useState(false);
	const [errors, setErrors] = useState<Partial<Record<AgentId, string>>>({});
	const startedJobs = useRef(new Set<AgentId>());
	const stackId = useId();
	// The expanded stack scrolls; fade its lower edge while more cards sit below.
	const stackRef = useRef<HTMLDivElement>(null);
	const [stackOverflows, setStackOverflows] = useState(false);
	const updateStackCue = () => {
		const element = stackRef.current;
		setStackOverflows(Boolean(element && element.scrollHeight - element.scrollTop > element.clientHeight + 1));
	};
	useEffect(() => {
		if (!expanded) { setStackOverflows(false); return; }
		updateStackCue();
	});
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

	const startUpdate = async (agentId: AgentId, latestVersion: string, methodId: string) => {
		if (startingRef.current.has(agentId) || activeJob(queryClient.getQueryData<Awaited<ReturnType<typeof fetchInstallJobs>>>(installJobsQueryKey)?.find((job) => job.target === agentId)?.status)) return;
		startingRef.current.add(agentId);
		setStarting((current) => new Set([...current, agentId]));
		setErrors((current) => ({ ...current, [agentId]: undefined }));
		try {
			// The same guard the Harness page applies: install only the release and
			// through the method the user saw, re-checked at click time.
			const key = updateAdvisoryQueryKey(agentId);
			await queryClient.cancelQueries({ queryKey: key });
			const fresh = await fetchUpdateAdvisory(agentId, undefined, true);
			queryClient.setQueryData(key, fresh);
			if (!hasConfirmedHarnessUpdate(fresh)) return;
			if (fresh.latestVersion !== latestVersion || maintenanceMethodId(fresh) !== methodId) {
				setErrors((current) => ({ ...current, [agentId]: fresh.latestVersion !== latestVersion
					? t("settings.harness.updateTargetChanged", { version: versionLabel(fresh.latestVersion!) })
					: t("settings.harness.updateMethodChanged", { agent: agentLabel(agentId) }) }));
				return;
			}
			const { data, error } = await apiClient.POST("/api/v1/agents/{agent}/install", {
				params: { path: { agent: agentId } },
				body: { method: methodId, operation: "update", expectedVersion: fresh.latestVersion },
			});
			if (error || !data) {
				setErrors((current) => ({ ...current, [agentId]: apiErrorMessage(error, t("settings.harness.updateFailed")) }));
				return;
			}
			startedJobs.current.add(agentId);
			await queryClient.invalidateQueries({ queryKey: installJobsQueryKey });
		} catch (error) {
			setErrors((current) => ({ ...current, [agentId]: error instanceof Error ? error.message : t("settings.harness.updateFailed") }));
		} finally {
			startingRef.current.delete(agentId);
			setStarting((current) => {
				const next = new Set(current);
				next.delete(agentId);
				return next;
			});
		}
	};

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
	const dismiss = (...keys: string[]) => {
		setDismissed((current) => {
			const next = new Set([...current, ...keys].slice(-100));
			try { localStorage.setItem(DISMISSED_KEY, JSON.stringify([...next])); } catch { /* In-memory dismissal still works. */ }
			return next;
		});
		if (updates.length <= 2) setExpanded(false);
	};
	const isExpanded = expanded && updates.length > 1;
	const ready = !authPlans.isPending && !authPlans.isError && !installers.isPending && !installers.isError && !jobs.isPending && !jobs.isError;
	const updateAllTargets = ready ? updates.flatMap(({ agentId, advisory }) => {
		const method = installers.data?.find((plan) => plan.agentId === agentId)?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
		const authPlan = authPlans.data?.find((plan) => plan.agentId === agentId);
		const needsLogin = authPlan && authPlan.action !== "instructions" && readinessById.get(agentId)?.authentication.state === "unauthorized";
		if (!method?.available || !method.updateAvailable || needsLogin || starting.has(agentId)
			|| activeJob(jobs.data?.find((job) => job.target === agentId)?.status)) return [];
		return [{ agentId, latestVersion: advisory.latestVersion!, methodId: method.id }];
	}) : [];
	const startAllUpdates = async () => {
		if (updateAllInFlight.current || updateAllTargets.length === 0) return;
		updateAllInFlight.current = true;
		setStartingAll(true);
		setExpanded(true);
		try {
			// Each notice keeps its existing target/owner recheck and error display.
			for (const target of updateAllTargets) await startUpdate(target.agentId, target.latestVersion, target.methodId);
		} finally {
			updateAllInFlight.current = false;
			setStartingAll(false);
		}
	};

	// Top-right and only on hover or focus, so it never covers the harness logo.
	const dismissButton = (label: string, onClick: () => void) => <Button type="button" variant="ghost" size="icon"
		className="absolute -right-1.5 -top-1.5 z-20 size-5 rounded-full border border-border bg-card opacity-0 shadow-sm transition-opacity hover:bg-muted focus-visible:opacity-100 group-hover/notice:opacity-100 group-focus-within/notice:opacity-100"
		aria-label={label} onClick={onClick}><X className="size-3" aria-hidden="true" /></Button>;
	// Several updates collapse into one summary card; Show all lists each one.
	const summary = !isExpanded && updates.length > 1;
	const updateAllButton = (className: string) => <Button type="button" size="sm" className={className}
		disabled={startingAll || updateAllTargets.length === 0} title={t("settings.harness.updateAllHint")} onClick={() => void startAllUpdates()}>
		{startingAll ? <LoaderCircle className="size-3 animate-spin" aria-hidden="true" /> : null}
		<span>{startingAll ? t("settings.harness.startingAllUpdates")
			: t("settings.harness.updateAll")}</span>
	</Button>;
	// Portaled to the end of <body>: Electron applies window drag regions in DOM
	// order, so a later drag strip (the home page's) would otherwise override no-drag.
	return createPortal(<aside role="region" aria-label={t("settings.harness.updateNoticeTitle")}
		// Sit in the free space at the top, except below Settings' topbar so its close button stays reachable.
		className={`fixed right-4 ${settingsModal ? "top-[52px]" : "top-3"} isolate z-[calc(var(--z-overlay)-1)] w-[min(300px,calc(100vw-32px))] text-card-foreground`}
		data-browser-native-overlay="true" data-state="open"
		// Outside Settings the stack reaches the window's drag region, which would swallow its clicks.
		style={{ WebkitAppRegion: "no-drag" } as CSSProperties}>
		{/* The stack toggle sits above the cards so it never reads as part of one notice. */}
		{/* Above the stack, whose negative margin reaches up into this row. */}
		{isExpanded ? <div className="relative z-20 mb-4 flex items-center justify-between gap-2">
			<Button type="button" variant="ghost" size="sm" className="h-6 rounded-md border border-border bg-card px-2 text-[11px] text-settings-muted shadow-sm hover:bg-muted" aria-expanded aria-controls={stackId} onClick={() => setExpanded(false)}>{t("settings.harness.hideUpdateStack")}</Button>
			{updateAllButton("h-6 px-2 text-[11px] shadow-sm")}
		</div> : null}
		{/* flow-root keeps the stack's negative margin inside this box, so the cards behind peek out below only. */}
		<div className="relative flow-root">
		{!isExpanded && updates.length > 1 ? <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-0">
			{updates.length > 2 ? <div className="absolute inset-x-4 -bottom-2 top-4 z-0 rounded-xl border border-border bg-card shadow-sm" /> : null}
			<div className="absolute inset-x-2 -bottom-1 top-2 z-10 rounded-xl border border-border bg-card shadow-sm" />
		</div> : null}
		<div id={stackId} ref={stackRef} onScroll={updateStackCue}
			className={cn("relative z-10 -m-4 max-h-[min(272px,calc(100dvh-64px))] space-y-4 overflow-y-auto overscroll-contain p-4",
				stackOverflows && "[mask-image:linear-gradient(to_bottom,black_calc(100%-40px),transparent)]")}>
			{summary ? <article aria-label={t("settings.harness.updateNoticeTitle")} className="group/notice relative rounded-xl border border-border bg-card px-2.5 py-2 shadow-md">
				{dismissButton(t("settings.harness.dismissAllUpdates"), () => dismiss(...updates.map((update) => update.key)))}
				<div className="flex items-center gap-2">
					<span className="flex shrink-0 -space-x-1.5">
						{updates.slice(0, 3).map(({ agentId }) => <AgentAvatar key={agentId} className="size-5 rounded-full ring-2 ring-card" decorative provider={agentId} />)}
					</span>
					<p className="min-w-0 flex-1 truncate text-base font-medium leading-6">{t("settings.harness.updateNoticeTitle")}</p>
				</div>
				<div className="mt-1.5 flex items-center justify-between gap-2">
					<Button type="button" size="sm" variant="outline" className="h-6 shrink-0 px-2 text-[11px]" aria-expanded={false} aria-controls={stackId} onClick={() => setExpanded(true)}>{t("settings.harness.showUpdateStack", { count: updates.length })}</Button>
					{updateAllButton("h-6 shrink-0 px-2 text-[11px]")}
				</div>
			</article> : (isExpanded ? updates : updates.slice(0, 1)).map(({ agentId, advisory, key }) => {
				const plan = installers.data?.find((candidate) => candidate.agentId === agentId);
				const job = jobs.data?.find((candidate) => candidate.target === agentId);
				const method = plan?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
				const authPlan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
				const authStatus = readiness.data?.agents.find((agent) => agent.id === agentId)?.authentication.state;
				const needsLogin = Boolean(authPlan && authPlan.action !== "instructions" && authStatus === "unauthorized");
				const canUpdate = Boolean(method?.available && method.updateAvailable);
				const busy = activeJob(job?.status) || starting.has(agentId);
				// A job this notice started that failed stays explained here.
				const failed = startedJobs.current.has(agentId) && (job?.status === "failed" || job?.status === "unsupported" || job?.status === "interrupted");
				const error = errors[agentId] ?? (failed ? job?.error || t("settings.harness.updateJobFailed") : undefined);
				const requestAction = () => {
					if (!needsLogin && method) {
						void startUpdate(agentId, advisory.latestVersion!, method.id);
						return;
					}
					// A login runs in a terminal on the Harness page.
					useHarnessActionRequest.getState().setRequest({ agentId, latestVersion: advisory.latestVersion!, action: "login" });
					openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true });
				};
				return <article key={key} aria-label={agentLabel(agentId)} className="group/notice relative rounded-xl border border-border bg-card px-2.5 py-2 shadow-md">
					{dismissButton(t("settings.harness.dismissAgentUpdate", { agent: agentLabel(agentId) }), () => dismiss(key))}
					<div className="flex items-center gap-2">
						<AgentAvatar className="size-5 shrink-0" decorative provider={agentId} />
						<p className="min-w-0 flex-1 break-words text-[13px] font-medium">{agentLabel(agentId)}</p>
					</div>
					<div className="flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
						<p className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1 gap-y-1 text-[11px]"><span className="break-all font-mono text-settings-label" title={t("settings.harness.installedVersion")}>{versionLabel(advisory.currentVersion!)}</span><ArrowRight className="size-3 shrink-0 text-settings-muted" aria-label={t("settings.harness.availableVersion")} /><span className="break-all font-mono font-medium text-settings-accent">{versionLabel(advisory.latestVersion!)}</span></p>
					{busy ? <Button size="sm" variant="outline" className="h-7 min-w-16 shrink-0 px-2 text-xs" disabled><LoaderCircle className="size-3 animate-spin" aria-hidden="true" />{t("settings.harness.updating")}</Button>
						: ready && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate)) ? <Button size="sm" className="h-7 min-w-16 shrink-0 px-2 text-xs" disabled={startingAll} onClick={requestAction}>{t(needsLogin ? authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login" : error ? "settings.harness.retryUpdate" : "settings.harness.update")}</Button>
						: ready && !needsLogin && plan?.documentationUrl ? <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" title={method?.updateReason ?? t("settings.harness.ownershipUnknown")} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button>
						: <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true })}>{t("settings.harness.viewDetails")}</Button>}
					</div>
					{error && !busy ? <p role="alert" className="mt-1 break-words text-[11px] text-error">{error}</p> : null}
				</article>;
			})}
		</div>
		</div>
	</aside>, document.body);
}
