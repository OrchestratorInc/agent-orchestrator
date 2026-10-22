import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, LoaderCircle, X } from "lucide-react";
import { type CSSProperties, type ReactNode, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useAgentAuthPlans } from "../hooks/useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { advisoryInUse, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installedAgentIds, installerQueryKey, installersQueryOptions, installJobsQueryKey, installJobsQueryOptions, isActiveInstallJob, isFailedInstallJob, isWaitingForIdleJob, maintenanceMethodId, needsHarnessLogin, updateAdvisoryQueryKey, useHarnessUpdates, useStartHarnessUpdate, versionLabel, type AgentUpdateAdvisory, type HarnessInUse, type InstallJob } from "../hooks/useHarnessUpdates";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { useLiveBrowserViewportRect, type ViewportRect } from "../hooks/useLiveBrowserViewportRect";
import { useWorkspaceTopbarBottom } from "../hooks/useWorkspaceTopbarBottom";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { isWindowsPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { MiddleTruncatedText } from "./MiddleTruncatedText";
import { HarnessSessionsDialog, IdleUpdateStatus } from "./settings/harness/HarnessSessionsDialog";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const DISMISSED_KEY = "ao.harness-update-dismissals.v1";

function readDismissals(): Set<string> {
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((entry): entry is string => typeof entry === "string").slice(-100) : []);
	} catch { return new Set(); }
}

/** Always mounted in the shell, not in a route or Settings. Read-only checks
 * and updates share the Harness page's queries; a login hands off to that page. */
export function HarnessUpdateNotice({ enabled }: { enabled: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const readiness = useAgentReadinessQuery(enabled);
	const readinessComplete = useEnsureAgentReadiness({ enabled, retryOnError: true });
	const installed = useMemo(() => installedAgentIds(readiness.data), [readiness.data]);
	const advisories = useHarnessUpdates(installed, undefined, readinessComplete && !readiness.isError);
	const installers = useQuery({ ...installersQueryOptions(), enabled });
	const authPlans = useAgentAuthPlans(undefined, enabled);
	const jobs = useQuery({ ...installJobsQueryOptions(), enabled });
	const startHarnessUpdate = useStartHarnessUpdate();
	const observedJobs = useRef(new Set<string>());
	const [dismissed, setDismissed] = useState(readDismissals);
	const [expanded, setExpanded] = useState(false);
	// Updates started from a notice run right here; the notice shows progress
	// and any failure instead of sending the user to Settings.
	const [starting, setStarting] = useState<ReadonlySet<AgentId>>(new Set());
	// Harnesses whose finished job is still refreshing its advisory: until the
	// refresh lands, the old advisory would offer Update again for a moment.
	const [settling, setSettling] = useState<ReadonlySet<AgentId>>(new Set());
	const startingRef = useRef(new Set<AgentId>());
	const updateAllInFlight = useRef(false);
	const [startingAll, setStartingAll] = useState(false);
	const [errors, setErrors] = useState<Partial<Record<AgentId, string>>>({});
	// Sessions a click-time check found that must end first.
	const [inUse, setInUse] = useState<Partial<Record<AgentId, HarnessInUse>>>({});
	const [sessionsView, setSessionsView] = useState<{ agentId: AgentId; inUse: HarnessInUse } | null>(null);
	const [updateAllSummary, setUpdateAllSummary] = useState<string | null>(null);
	const startedJobs = useRef(new Set<AgentId>());
	// The advisory each update started here acted on, so its failure can still
	// be shown after a refresh stops reporting a newer release.
	const startedUpdates = useRef(new Map<AgentId, AgentUpdateAdvisory>());
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
	const settingsOpen = useUiStore((state) => state.settingsModal !== null);

	// Refresh even on failure: a command can change the binary before
	// failing verification. Never keep advertising an obsolete version.
	const refreshAfterJob = (agentId: string) => {
		setSettling((current) => new Set([...current, agentId as AgentId]));
		void ensureAgentReadiness([agentId]).then(async (result) => {
			cacheAgentReadiness(queryClient, result);
			await queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(agentId as AgentId) });
		}).catch(() => undefined).finally(() => {
			setSettling((current) => {
				const next = new Set(current);
				next.delete(agentId as AgentId);
				return next;
			});
		});
		void queryClient.invalidateQueries({ queryKey: installerQueryKey });
	};
	useEffect(() => {
		if (!enabled) return;
		for (const job of jobs.data ?? []) {
			if (isActiveInstallJob(job)) { observedJobs.current.add(job.target); continue; }
			if (observedJobs.current.delete(job.target)) refreshAfterJob(job.target);
		}
	}, [enabled, jobs.data, queryClient]);

	/** Resolves to the sessions that kept the update from starting, if any. */
	const startUpdate = async (agentId: AgentId, advisory: AgentUpdateAdvisory, methodId: string): Promise<HarnessInUse | undefined> => {
		if (startingRef.current.has(agentId) || isActiveInstallJob(queryClient.getQueryData<InstallJob[]>(installJobsQueryKey)?.find((job) => job.target === agentId))) return undefined;
		startedUpdates.current.set(agentId, advisory);
		startingRef.current.add(agentId);
		setStarting((current) => new Set([...current, agentId]));
		setErrors((current) => ({ ...current, [agentId]: undefined }));
		setInUse((current) => ({ ...current, [agentId]: undefined }));
		try {
			const result = await startHarnessUpdate({ agentId, methodId });
			if (result.inUse) {
				setInUse((current) => ({ ...current, [agentId]: result.inUse }));
				return result.inUse;
			}
			// The click-time check may have moved the target to a newer release.
			if (result.advisory) startedUpdates.current.set(agentId, result.advisory);
			if (result.error) setErrors((current) => ({ ...current, [agentId]: result.error }));
			if (!result.job) return undefined;
			startedJobs.current.add(agentId);
			// A job that already finished never shows up as running, so refresh it now.
			if (!isActiveInstallJob(result.job)) refreshAfterJob(agentId);
			return undefined;
		} finally {
			startingRef.current.delete(agentId);
			setStarting((current) => {
				const next = new Set(current);
				next.delete(agentId);
				return next;
			});
		}
	};

	// Settings is a modal that blocks pointer and focus outside it; its Harness
	// page offers the same updates there.
	if (settingsOpen || !readinessComplete || readiness.isError) return null;
	// Only advertise updates AO can run without a login or manual maintenance.
	// Wait for these checks so ineligible harnesses never flash in the stack. A
	// failed background refetch keeps the cached data, so an update in flight
	// or its failure stays visible.
	if (!authPlans.data || !installers.data) return null;
	// Let the first batch settle so a fast, seldom-used harness cannot jump
	// ahead of the most-used one. Failed lookups don't block the other results.
	if (AGENT_OPTIONS.some((id) => installed.has(id) && advisories.get(id)?.isPending)) return null;
	const readinessById = new Map(readiness.data?.agents.map((agent) => [agent.id, agent]));
	// An update started here that failed stays explained until it is dismissed,
	// even once a refresh reports the harness current: an updater can replace
	// the binary and still exit with an error.
	const failedUpdateAdvisory = (agentId: AgentId) => {
		const advisory = startedUpdates.current.get(agentId);
		const job = jobs.data?.find((candidate) => candidate.target === agentId);
		return advisory && (errors[agentId] || (startedJobs.current.has(agentId) && isFailedInstallJob(job))) ? advisory : undefined;
	};
	const updates = AGENT_OPTIONS.flatMap((agentId) => {
		const query = advisories.get(agentId);
		const confirmed = installed.has(agentId) && !query?.isError && hasConfirmedHarnessUpdate(query?.data) ? query?.data : undefined;
		const advisory = confirmed ?? failedUpdateAdvisory(agentId);
		if (!advisory) return [];
		const method = installers.data?.find((plan) => plan.agentId === agentId)?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
		const needsLogin = needsHarnessLogin(authPlans.data?.find((plan) => plan.agentId === agentId), readinessById.get(agentId)?.authentication.state);
		if (!method?.available || !method.updateAvailable || needsLogin) return [];
		const key = harnessUpdateNoticeKey(agentId, advisory.latestVersion!);
		return dismissed.has(key) ? [] : [{ agentId, advisory, key }];
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
		const needsLogin = needsHarnessLogin(authPlans.data?.find((plan) => plan.agentId === agentId), readinessById.get(agentId)?.authentication.state);
		if (!method?.available || !method.updateAvailable || needsLogin || starting.has(agentId) || settling.has(agentId)
			|| isActiveInstallJob(jobs.data?.find((job) => job.target === agentId))) return [];
		return [{ agentId, advisory, methodId: method.id }];
	}) : [];
	const runAllUpdates = async (targets: typeof updateAllTargets, skipped: Array<{ agentId: AgentId; count: number }>) => {
		if (updateAllInFlight.current) return;
		updateAllInFlight.current = true;
		setStartingAll(true);
		setExpanded(true);
		setUpdateAllSummary(null);
		let started = 0;
		try {
			// Each notice keeps its existing target/owner recheck and error display.
			for (const target of targets) {
				const refused = await startUpdate(target.agentId, target.advisory, target.methodId);
				if (refused) skipped.push({ agentId: target.agentId, count: refused.count });
				else started += 1;
			}
		} finally {
			updateAllInFlight.current = false;
			setStartingAll(false);
		}
		if (skipped.length > 0) {
			setUpdateAllSummary(t("settings.harness.updateAllSummary", {
				count: started,
				skipped: skipped.map(({ agentId, count }) => t("settings.harness.updateAllSkipped", { agent: agentLabel(agentId), count })).join(", "),
			}));
		}
	};
	// Harnesses whose sessions must end first are skipped and named in the summary.
	const startAllUpdates = () => {
		if (updateAllInFlight.current || updateAllTargets.length === 0) return;
		const skipped: Array<{ agentId: AgentId; count: number }> = [];
		const runnable = updateAllTargets.filter(({ agentId, advisory }) => {
			const sessions = advisoryInUse(advisory);
			if (!sessions) return true;
			skipped.push({ agentId, count: sessions.count });
			return false;
		});
		void runAllUpdates(runnable, skipped);
	};

	// Top-right and only on hover or focus, so it never covers the harness logo.
	const dismissButton = (label: string, onClick: () => void) => <Button type="button" variant="ghost" size="icon"
		className="absolute -right-1.5 -top-1.5 z-20 size-5 rounded-full border border-border bg-card opacity-0 transition-opacity hover:bg-muted focus-visible:opacity-100 group-hover/notice:opacity-100 group-focus-within/notice:opacity-100"
		aria-label={label} onClick={onClick}><X className="size-3" aria-hidden="true" /></Button>;
	// Several updates collapse into one summary card; Show all lists each one.
	const summary = !isExpanded && updates.length > 1;
	// The caption size is written as a length: tailwind-merge reads a bare `text-caption`
	// as a text colour and drops the button's own, leaving white text on the light fill.
	// While updates start the button stays solid: it reports progress, not an
	// unavailable action.
	const updateAllButton = (className: string) => <Button type="button" size="sm" className={cn(className, "text-[length:var(--font-size-caption)]", startingAll && "disabled:opacity-100")}
		aria-busy={startingAll || undefined}
		disabled={startingAll || updateAllTargets.length === 0} onClick={() => void startAllUpdates()}>
		{startingAll ? <LoaderCircle className="size-3 animate-spin" aria-hidden="true" /> : null}
		<span>{startingAll ? t("settings.harness.startingAllUpdates")
			: t("settings.harness.updateAll")}</span>
	</Button>;
	// Portaled to the end of <body>: Electron applies window drag regions in DOM
	// order, so a later drag strip (the home page's) would otherwise override no-drag.
	return createPortal(<NoticeFrame label={t("settings.harness.updateNoticeTitle")}>
		{/* The stack toggle sits above the cards so it never reads as part of one notice. */}
		{/* Above the stack, whose negative margin reaches up into this row. */}
		{isExpanded ? <div className="relative z-20 mb-4 flex items-center justify-between gap-2">
			<Button type="button" variant="ghost" size="sm" className="h-6 rounded-md border border-border bg-card px-2 text-caption text-settings-muted hover:bg-muted" aria-expanded aria-controls={stackId} onClick={() => setExpanded(false)}>{t("settings.harness.hideUpdateStack")}</Button>
			{updateAllButton("h-6 px-2")}
		</div> : null}
		{/* flow-root keeps the stack's negative margin inside this box, so the cards behind peek out below only. */}
		<div className="relative flow-root">
		{!isExpanded && updates.length > 1 ? <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-0">
			{updates.length > 2 ? <div className="absolute inset-x-4 -bottom-2 top-4 z-0 rounded-xl border border-border bg-card" /> : null}
			<div className="absolute inset-x-2 -bottom-1 top-2 z-10 rounded-xl border border-border bg-card" />
		</div> : null}
		<div id={stackId} ref={stackRef} onScroll={updateStackCue}
			className={cn("relative z-10 -m-4 max-h-[min(272px,calc(100dvh-64px))] space-y-4 overflow-y-auto overscroll-contain p-4",
				stackOverflows && "[mask-image:linear-gradient(to_bottom,black_calc(100%-40px),transparent)]")}>
			{summary ? <article aria-label={t("settings.harness.updateNoticeTitle")} className="group/notice relative rounded-xl border border-border bg-card px-2.5 py-2">
				{dismissButton(t("settings.harness.dismissAllUpdates"), () => dismiss(...updates.map((update) => update.key)))}
				<div className="flex items-center gap-2">
					<span className="flex shrink-0 -space-x-1.5">
						{updates.slice(0, 3).map(({ agentId }) => <AgentAvatar key={agentId} className="size-5 rounded-full ring-2 ring-card" decorative provider={agentId} />)}
					</span>
					<p className="min-w-0 flex-1 truncate text-base font-medium leading-6">{t("settings.harness.updateNoticeTitle")}</p>
				</div>
				<div className="mt-1.5 flex items-center justify-between gap-2">
					<Button type="button" size="sm" variant="outline" className="h-6 shrink-0 px-2 text-caption" aria-expanded={false} aria-controls={stackId} onClick={() => setExpanded(true)}>{t("settings.harness.showUpdateStack", { count: updates.length })}</Button>
					{updateAllButton("h-6 shrink-0 px-2")}
				</div>
			</article> : (isExpanded ? updates : updates.slice(0, 1)).map(({ agentId, advisory, key }) => {
				const plan = installers.data?.find((candidate) => candidate.agentId === agentId);
				const job = jobs.data?.find((candidate) => candidate.target === agentId);
				const method = plan?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
				const authPlan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
				const needsLogin = needsHarnessLogin(authPlan, readinessById.get(agentId)?.authentication.state);
				const canUpdate = Boolean(method?.available && method.updateAvailable);
				const busy = isActiveInstallJob(job) || starting.has(agentId) || settling.has(agentId);
				// A job this notice started that failed stays explained here.
				const failed = startedJobs.current.has(agentId) && isFailedInstallJob(job);
				const error = errors[agentId] ?? (failed ? job?.error || t("settings.harness.updateJobFailed") : undefined);
				// The click-time check outranks the cached advisory once it has answered.
				const sessions = inUse[agentId] ?? advisoryInUse(advisory);
				// A start error means this attempt made no job; any retained job is an older one.
				const failedJobStartedAt = errors[agentId] === undefined && failed ? job?.startedAt ?? undefined : undefined;
				const requestAction = () => {
					if (!needsLogin && method) {
						// A harness that was only starting is retried, never shown as sessions.
						if (sessions && !sessions.starting) { setSessionsView({ agentId, inUse: sessions }); return; }
						void startUpdate(agentId, advisory, method.id);
						return;
					}
					// A login runs in a terminal on the Harness page, which starts it once on open.
					openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true, startLogin: true });
				};
				return <article key={key} aria-label={agentLabel(agentId)} className="group/notice relative rounded-xl border border-border bg-card px-2.5 py-2">
					{dismissButton(t("settings.harness.dismissAgentUpdate", { agent: agentLabel(agentId) }), () => dismiss(key))}
					<div className="flex items-center gap-2">
						<AgentAvatar className="size-5 shrink-0" decorative provider={agentId} />
						<p className="min-w-0 flex-1 break-words text-[13px] font-medium">{agentLabel(agentId)}</p>
					</div>
					<div className="flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
						<p className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1 gap-y-1 text-caption"><MiddleTruncatedText className="whitespace-nowrap font-mono text-settings-label" text={versionLabel(advisory.currentVersion!)} /><ArrowRight className="size-3 shrink-0 text-settings-muted" role="img" aria-label={t("settings.harness.availableVersion")} /><MiddleTruncatedText className="whitespace-nowrap font-mono font-medium text-settings-accent" text={versionLabel(advisory.latestVersion!)} /></p>
					{busy ? <Button size="sm" variant="secondary" className="h-7 min-w-16 shrink-0 px-2 text-xs disabled:opacity-100" aria-busy="true" disabled><LoaderCircle className="size-3 animate-spin" aria-hidden="true" />{t(isWaitingForIdleJob(job) ? "settings.harness.waiting" : "settings.harness.updating")}</Button>
						: ready && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate)) ? <Button size="sm" className="h-7 min-w-16 shrink-0 px-2 text-xs" disabled={startingAll} onClick={requestAction}>{needsLogin ? t(authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login")
							: sessions ? (sessions.starting ? t("settings.harness.update") : t("settings.harness.viewSessions", { count: sessions.count }))
							: t(error ? "settings.harness.retryUpdate" : "settings.harness.update")}</Button>
						: ready && !needsLogin && plan?.documentationUrl ? <Tooltip>
							<TooltipTrigger asChild><Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button></TooltipTrigger>
							<TooltipContent side="bottom" className="max-w-64">{method?.updateReason || t("settings.harness.ownershipUnknown")}</TooltipContent>
						</Tooltip>
						: <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true })}>{t("settings.harness.viewDetails")}</Button>}
					</div>
					{/* One click to the Harness page with this failure's diagnostics open, ready to copy. */}
					{error && !busy ? <div className="mt-1 flex flex-col items-start gap-1">
						<p role="alert" className="line-clamp-3 break-words text-caption text-error">{error}</p>
						<Button type="button" size="sm" variant="outline" className="h-6 px-2 text-[length:var(--font-size-caption)]" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true, showDiagnostics: { jobStartedAt: failedJobStartedAt, error } })}>{t("settings.harness.showDiagnostics")}</Button>
					</div> : null}
					{job && isWaitingForIdleJob(job) ? <IdleUpdateStatus agentId={agentId} job={job} onViewSessions={(waiting) => setSessionsView({ agentId, inUse: waiting })} /> : null}
					{!error && !busy && inUse[agentId] ? <p role="status" className="mt-1 break-words text-caption text-settings-muted">
						{inUse[agentId]!.starting ? t("settings.harness.inUseStarting", { agent: agentLabel(agentId) }) : t("settings.harness.inUseBlocked", { count: inUse[agentId]!.count, agent: agentLabel(agentId) })}
					</p> : null}
				</article>;
			})}
			{updateAllSummary && !startingAll ? <p role="status" className="rounded-xl border border-border bg-card px-2.5 py-2 text-caption text-settings-muted shadow-md">{updateAllSummary}</p> : null}
		</div>
		</div>
		<HarnessSessionsDialog
			agentId={sessionsView?.agentId ?? null}
			inUse={sessionsView?.inUse}
			onOpenChange={(open) => {
				if (open || !sessionsView) return;
				// The click-time answer is a snapshot: once its sessions are seen, the
				// next click checks again instead of reopening the same list.
				const { agentId } = sessionsView;
				setInUse((current) => ({ ...current, [agentId]: undefined }));
				setSessionsView(null);
			}}
		/>
	</NoticeFrame>, document.body);
}

const NOTICE_MAX_WIDTH = 300;
const NOTICE_EDGE = 16; // right-4
const NOTICE_GAP = 12; // --space-3

/** The notice's fixed frame, kept below whichever top bar is showing so the
 * stack and its Show less / Update all row never cover New task or Orchestrator.
 *
 * The live browser page is a native view: anything drawn over it has to raise
 * the whole transparent shell above the page, which then swallows every click
 * and scroll meant for the browser. This notice is non-modal and long-lived, so
 * it never does that. It stays at the app edge and steps aside while
 * the live page overlaps that corner. */
function NoticeFrame({ label, children }: { label: string; children: ReactNode }) {
	const topbarBottom = useWorkspaceTopbarBottom();
	const browserRect = useLiveBrowserViewportRect();
	const frameRef = useRef<HTMLElement>(null);
	const [height, setHeight] = useState(0);
	useLayoutEffect(() => {
		const element = frameRef.current;
		if (!element) {
			publishHarnessUpdateNoticeRect(null);
			return;
		}
		// Session content in this corner (a running step's timer, a session
		// notice's dismiss button) reads this rect to keep itself clear.
		const measure = () => {
			const box = element.getBoundingClientRect();
			setHeight(box.height);
			publishHarnessUpdateNoticeRect(box.width > 0 ? { left: box.left, top: box.top, right: box.right, bottom: box.bottom } : null);
		};
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		window.addEventListener("resize", measure);
		measure();
		return () => {
			observer.disconnect();
			window.removeEventListener("resize", measure);
		};
	});
	useLayoutEffect(() => () => publishHarnessUpdateNoticeRect(null), []);
	// Until a bar is laid out, assume the board's: below the toolbar (and Windows' titlebar).
	const top = topbarBottom === null
		? isWindowsPlatform() ? "calc(var(--size-window-titlebar) + var(--size-toolbar) + var(--space-3))" : "calc(var(--size-toolbar) + var(--space-3))"
		: `calc(${topbarBottom}px + var(--space-3))`;
	if (noticeCoversLiveBrowser(browserRect, (topbarBottom ?? 0) + NOTICE_GAP, height)) return null;
	return <aside ref={frameRef} role="region" aria-label={label}
		className="harness-update-notice fixed right-4 isolate z-[calc(var(--z-overlay)-1)] w-[min(300px,calc(100vw-32px))] text-card-foreground"
		// The stack reaches the window's drag region, which would swallow its clicks.
		style={{ WebkitAppRegion: "no-drag", top } as CSSProperties}>
		{children}
	</aside>;
}

/** Whether the notice at the app edge overlaps the live native browser page. */
export function noticeCoversLiveBrowser(
	browser: ViewportRect | null,
	top: number,
	height: number,
	viewportWidth = document.documentElement.clientWidth,
): boolean {
	const width = Math.min(NOTICE_MAX_WIDTH, viewportWidth - 2 * NOTICE_EDGE);
	const right = viewportWidth - NOTICE_EDGE;
	if (!browser) return false;
	const overlapsX = right - width < browser.right && right > browser.left;
	const overlapsY = top < browser.bottom && top + Math.max(height, 1) > browser.top;
	return overlapsX && overlapsY;
}
