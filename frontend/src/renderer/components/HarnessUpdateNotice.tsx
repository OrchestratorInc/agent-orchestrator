import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ChevronDown, CircleCheck, LoaderCircle, X } from "lucide-react";
import { type CSSProperties, type ReactNode, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useAgentAuthPlans } from "../hooks/useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { advisoryInUse, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installedAgentIds, installerQueryKey, installersQueryOptions, installJobsQueryKey, installJobsQueryOptions, isActiveInstallJob, isFailedInstallJob, isWaitingForIdleJob, maintenanceMethodId, needsHarnessLogin, updateAdvisoryQueryKey, useHarnessUpdates, useStartHarnessUpdate, versionLabel, type AgentUpdateAdvisory, type HarnessInUse, type InstallJob } from "../hooks/useHarnessUpdates";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { useLiveBrowserViewportRect, type ViewportRect } from "../hooks/useLiveBrowserViewportRect";
import { useCenterPanelBox } from "../hooks/useCenterPanelBox";
import { useSwipeToDismiss } from "../hooks/useSwipeToDismiss";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
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
	const { t, i18n } = useTranslation();
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
	// Updates started here that succeeded, by the version they moved to: shown
	// as done for a moment once the advisory stops offering them, so success
	// never looks like the notice simply vanishing.
	const [updated, setUpdated] = useState<ReadonlyMap<AgentId, string>>(new Map());
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
	const showUpdated = (agentId: AgentId, job: InstallJob) => {
		const version = startedUpdates.current.get(agentId)?.latestVersion;
		if (job.status !== "succeeded" || !startedJobs.current.has(agentId) || !version) return;
		setUpdated((current) => new Map(current).set(agentId, version));
		window.setTimeout(() => setUpdated((current) => {
			const next = new Map(current);
			next.delete(agentId);
			return next;
		}), UPDATED_MS);
	};
	useEffect(() => {
		if (!enabled) return;
		for (const job of jobs.data ?? []) {
			if (isActiveInstallJob(job)) { observedJobs.current.add(job.target); continue; }
			if (observedJobs.current.delete(job.target)) {
				refreshAfterJob(job.target);
				showUpdated(job.target as AgentId, job);
			}
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
			if (!isActiveInstallJob(result.job)) {
				refreshAfterJob(agentId);
				showUpdated(agentId, result.job);
			}
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
	const updatedCards = [...updated].filter(([agentId]) => !updates.some((update) => update.agentId === agentId));
	if (updates.length === 0 && updatedCards.length === 0) return null;
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

	const jobFor = (agentId: AgentId) => jobs.data?.find((candidate) => candidate.target === agentId);
	// While updates start the button stays solid: it reports progress, not an
	// unavailable action.
	const updateAllButton = <Button type="button" size="sm" className={cn("min-w-20 shrink-0", startingAll && "disabled:opacity-100")}
		aria-busy={startingAll || undefined}
		disabled={startingAll || updateAllTargets.length === 0} onClick={() => void startAllUpdates()}>
		{startingAll ? <LoaderCircle className="size-icon-2xs animate-spin" aria-hidden="true" /> : null}
		<span>{startingAll ? t("settings.harness.startingAllUpdates")
			: t("settings.harness.updateAll")}</span>
	</Button>;
	const help = t("settings.harness.updateNoticeHelp");
	const updateItem = ({ agentId, advisory, key }: (typeof updates)[number], variant: "card" | "row") => {
		const plan = installers.data?.find((candidate) => candidate.agentId === agentId);
		const job = jobFor(agentId);
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
		return <SwipeCard key={key} variant={variant} label={agentLabel(agentId)} onDismiss={() => dismiss(key)}
			dismissLabel={t("settings.harness.dismissAgentUpdate", { agent: agentLabel(agentId) })}
			icon={<AgentAvatar className="size-icon-xl shrink-0" decorative provider={agentId} />}
			title={variant === "row" ? agentLabel(agentId) : t("settings.harness.updateCardTitle", { agent: agentLabel(agentId) })}
			help={variant === "card" ? help : undefined}>
			<div className="mt-2 flex items-center gap-2">
				<VersionChange from={advisory.currentVersion!} to={advisory.latestVersion!} />
				{/* One width for every label, so Updating… never pushes the versions around. */}
				{busy ? <Button size="sm" variant="secondary" className="min-w-20 disabled:opacity-100" aria-busy="true" disabled><LoaderCircle className="size-icon-2xs animate-spin" aria-hidden="true" />{t(isWaitingForIdleJob(job) ? "settings.harness.waiting" : "settings.harness.updating")}</Button>
				: ready && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate)) ? <Button size="sm" className="min-w-20" disabled={startingAll} onClick={requestAction}>{needsLogin ? t(authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login")
					: sessions ? (sessions.starting ? t("settings.harness.update") : t("settings.harness.viewSessions", { count: sessions.count }))
					: t(error ? "settings.harness.retryUpdate" : "settings.harness.update")}</Button>
				: ready && !needsLogin && plan?.documentationUrl ? <Tooltip>
					<TooltipTrigger asChild><Button size="sm" variant="outline" onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button></TooltipTrigger>
					<TooltipContent side="top" className="max-w-64">{method?.updateReason || t("settings.harness.ownershipUnknown")}</TooltipContent>
				</Tooltip>
				: <Button size="sm" variant="outline" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true })}>{t("settings.harness.viewDetails")}</Button>}
			</div>
			{/* One click to the Harness page with this failure's diagnostics open, ready to copy. */}
			{error && !busy ? <div className="mt-2 flex flex-col items-start gap-1.5">
				<p role="alert" className="line-clamp-3 break-words text-caption text-error">{error}</p>
				<Button type="button" size="sm" variant="outline" onClick={() => openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true, showDiagnostics: { jobStartedAt: failedJobStartedAt, error } })}>{t("settings.harness.showDiagnostics")}</Button>
			</div> : null}
			{job && isWaitingForIdleJob(job) ? <IdleUpdateStatus agentId={agentId} job={job} onViewSessions={(waiting) => setSessionsView({ agentId, inUse: waiting })} /> : null}
			{!error && !busy && inUse[agentId] ? <p role="status" className="mt-2 break-words text-caption text-muted-foreground">
				{inUse[agentId]!.starting ? t("settings.harness.inUseStarting", { agent: agentLabel(agentId) }) : t("settings.harness.inUseBlocked", { count: inUse[agentId]!.count, agent: agentLabel(agentId) })}
			</p> : null}
		</SwipeCard>;
	};
	const updatedItems = updatedCards.map(([agentId, version]) => <SwipeCard key={`updated:${agentId}`} variant="card" label={agentLabel(agentId)} role="status"
		onDismiss={() => setUpdated((current) => {
			const next = new Map(current);
			next.delete(agentId);
			return next;
		})}
		dismissLabel={t("settings.harness.dismissAgentUpdate", { agent: agentLabel(agentId) })}
		icon={<span className="relative shrink-0">
			<AgentAvatar className="size-icon-xl" decorative provider={agentId} />
			<CircleCheck className="absolute -bottom-1 -right-1 size-icon-2xs rounded-full bg-settings-menu text-success" aria-hidden="true" />
		</span>}
		title={t("settings.harness.updatedCardTitle", { agent: agentLabel(agentId) })}>
		<p className="mt-2 text-caption text-muted-foreground">{t("settings.harness.updatedTo", { version: friendlyVersion(version, i18n.resolvedLanguage ?? i18n.language) })}</p>
	</SwipeCard>);
	const stackTitle = t("settings.harness.updateStackTitle", { count: updates.length });
	const dismissAll = () => dismiss(...updates.map((update) => update.key));
	// Portaled to the end of <body>: Electron applies window drag regions in DOM
	// order, so a later drag strip (the home page's) would otherwise override no-drag.
	return createPortal(<NoticeFrame label={t("settings.harness.updateNoticeTitle")}>
		<div className="space-y-2">
		{updates.length > 1 ? <SwipeCard variant="card" role="group" label={t("settings.harness.updateNoticeTitle")} onDismiss={dismissAll} swipeable={!isExpanded}
			dismissLabel={t("settings.harness.dismissAllUpdates")}
			// Overlapping marks, each cut out of the one beneath by a halo in the card's colour.
			icon={<span className="flex shrink-0 items-center -space-x-1.5">
				{updates.slice(0, 3).map(({ agentId }) => <AgentAvatar key={agentId} className="relative size-icon-xl [filter:drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_2px_var(--color-bg-settings-menu))_drop-shadow(0_0_3px_var(--color-bg-settings-menu))]" decorative provider={agentId} />)}
			</span>}
			title={stackTitle}
			// The Update all summary takes the help line's place while it is shown.
			help={updateAllSummary && !startingAll ? updateAllSummary : help}
			footer={<>
				{/* The list toggle keeps one place, opposite Update all, whichever way it points. */}
				<Button type="button" size="sm" variant="outline" className="shrink-0" aria-expanded={isExpanded} aria-controls={stackId}
					onClick={() => setExpanded(!isExpanded)}>
					{isExpanded ? t("settings.harness.hideUpdateStack") : t("settings.harness.showUpdateStack", { count: updates.length })}
					<ChevronDown className={cn("size-icon-2xs transition-transform duration-200 motion-reduce:transition-none", isExpanded && "rotate-180")} aria-hidden="true" />
				</Button>
				<span className="flex-1" />
				{updateAllButton}
			</>}>
			{/* Mounted while closed, so opening only animates: building every row on
			    the click would stall the first frames. */}
			<div inert={!isExpanded} aria-hidden={!isExpanded || undefined}
				className={cn("-mx-3 overflow-hidden transition-[height,opacity] duration-200 ease-out [interpolate-size:allow-keywords] motion-reduce:transition-none",
					isExpanded ? "h-auto opacity-100" : "h-0 opacity-0")}>
				<div id={stackId} ref={stackRef} onScroll={updateStackCue}
					className={cn("mt-2 max-h-[min(280px,calc(100dvh-240px))] divide-y divide-(--color-border-overlay) overflow-x-hidden overflow-y-auto overscroll-contain border-y border-(--color-border-overlay)",
						stackOverflows && "[mask-image:linear-gradient(to_bottom,black_calc(100%-32px),transparent)]")}>
					{updates.map((update) => updateItem(update, "row"))}
				</div>
			</div>
		</SwipeCard> : updates.map((update) => updateItem(update, "card"))}
		{updatedItems}
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

/** A notice card, or a row in the expanded list: swipe it right (two fingers
 * or a drag) or press × to dismiss it. Every card puts its main action in the
 * same bottom-right place, so Update and Update all line up. */
function SwipeCard({ variant, role, label, icon, title, help, dismissLabel, onDismiss, swipeable = true, footer, children }: {
	variant: "card" | "row";
	role?: "status" | "group";
	label: string;
	icon: ReactNode;
	title: string;
	help?: string;
	dismissLabel: string;
	onDismiss: () => void;
	swipeable?: boolean;
	footer?: ReactNode;
	children?: ReactNode;
}) {
	const ref = useRef<HTMLElement>(null);
	const leave = useSwipeToDismiss(ref, onDismiss, swipeable);
	return <article ref={ref} aria-label={label} role={role}
		// The chat model selector's menu surface, so it reads as a floating menu
		// apart from the composer and other cards it can sit over.
		className={cn("relative touch-pan-y select-none px-3 py-2.5",
			variant === "card" && "rounded-(--radius-settings-panel) border border-(--color-border-settings-menu) bg-settings-menu shadow-(--shadow-popover)")}>
		<div className="flex items-center gap-2">
			{icon}
			<p className="min-w-0 flex-1 truncate text-control font-medium">{title}</p>
			<button type="button" aria-label={dismissLabel} onClick={leave}
				className="-mr-1 grid size-control-xs shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring">
				<X className="size-icon-2xs" aria-hidden="true" />
			</button>
		</div>
		{help ? <p aria-live="polite" className="mt-0.5 text-caption text-muted-foreground">{help}</p> : null}
		{children}
		{footer ? <div className="mt-2 flex items-center gap-2">{footer}</div> : null}
	</article>;
}

// Builds published as 0.0.<unix seconds>-g<sha> (Amp's) say nothing readable in
// their digits, and cut short they look like neighbouring patch releases. Show
// the build date instead; the full version stays in the hover title.
const BUILD_STAMP = /^v?\d+\.\d+\.(\d{10})(?:[-+.].*)?$/;

function buildDate(version: string): Date | null {
	const seconds = Number(BUILD_STAMP.exec(version)?.[1]);
	return seconds >= 1_500_000_000 && seconds < 2_500_000_000 ? new Date(seconds * 1000) : null;
}

export function friendlyVersion(version: string, locale: string): string {
	const date = buildDate(version);
	if (!date) return versionLabel(version);
	const sameYear = date.getFullYear() === new Date().getFullYear();
	return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", ...(sameYear ? {} : { year: "numeric" }) }).format(date);
}

function VersionChange({ from, to }: { from: string; to: string }) {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage ?? i18n.language;
	let fromLabel = friendlyVersion(from, locale);
	let toLabel = friendlyVersion(to, locale);
	// Two builds from one day read the same as dates; fall back to the versions.
	if (fromLabel === toLabel) {
		fromLabel = versionLabel(from);
		toLabel = versionLabel(to);
	}
	return <p className="flex min-w-0 flex-1 items-center gap-1 text-caption text-muted-foreground">
		<span className="min-w-0 truncate" title={from}>{fromLabel}</span>
		<ArrowRight className="size-icon-2xs shrink-0" role="img" aria-label={t("settings.harness.availableVersion")} />
		<span className="min-w-0 truncate font-medium text-foreground" title={to}>{toLabel}</span>
	</p>;
}

// Longer than the list's, a closing row's and the top-bar slide's transitions (200ms, 180ms, 200ms).
const MOTION_SETTLE_MS = 260;
const UPDATED_MS = 4_000;

/** The notice's frame in the top-right corner of the framed center panel, just
 * below the page's top bar when it has one, so it never covers a page's own
 * actions and slides, rather than jumps, between pages. A fixed box that
 * tracks the panel clips it, so a swipe slides it out under the panel's edge
 * instead of over the window frame.
 *
 * The live browser page is a native view: anything drawn over it has to raise
 * the whole transparent shell above the page, which then swallows every click
 * and scroll meant for the browser. This notice never does that. It turns
 * invisible while the live page overlaps it, and stays measurable meanwhile. */
function NoticeFrame({ label, children }: { label: string; children: ReactNode }) {
	const browserRect = useLiveBrowserViewportRect();
	const panel = useCenterPanelBox();
	const frameRef = useRef<HTMLElement>(null);
	const [box, setBox] = useState<ViewportRect | null>(null);
	useLayoutEffect(() => {
		const element = frameRef.current;
		if (!element) return;
		// While a height or position transition runs (the list opening, a row
		// closing, the notice following a top bar) the rect changes every frame;
		// measuring then would re-render every reader of the rect each frame, so
		// it measures once the motion has settled.
		let settle: number | undefined;
		const measure = () => {
			if (settle !== undefined) return;
			const rect = element.getBoundingClientRect();
			const next = rect.width > 0 ? { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom } : null;
			setBox((current) => (current && next && current.left === next.left && current.top === next.top && current.right === next.right && current.bottom === next.bottom ? current : next));
		};
		const onTransition = (event: TransitionEvent) => {
			if (event.propertyName !== "height" && event.propertyName !== "translate") return;
			window.clearTimeout(settle);
			settle = window.setTimeout(() => {
				settle = undefined;
				measure();
			}, MOTION_SETTLE_MS);
		};
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		window.addEventListener("resize", measure);
		element.addEventListener("transitionrun", onTransition);
		measure();
		return () => {
			window.clearTimeout(settle);
			observer.disconnect();
			window.removeEventListener("resize", measure);
			element.removeEventListener("transitionrun", onTransition);
		};
	}, [panel]);
	const covered = noticeCoversLiveBrowser(browserRect, box);
	// Session content in this corner reads this rect to keep itself clear.
	useLayoutEffect(() => publishHarnessUpdateNoticeRect(covered ? null : box), [box, covered]);
	useLayoutEffect(() => () => publishHarnessUpdateNoticeRect(null), []);
	const area = panel ?? { left: 0, top: 0, right: document.documentElement.clientWidth, bottom: document.documentElement.clientHeight };
	return <div className="pointer-events-none fixed isolate z-[calc(var(--z-overlay)-1)] overflow-hidden"
		style={{ left: area.left, top: area.top, width: area.right - area.left, height: area.bottom - area.top, borderRadius: panel?.radius }}>
		<aside ref={frameRef} role="region" aria-label={label} inert={covered} aria-hidden={covered || undefined}
			// Its own compositor layer: the card's height animation repaints only the card.
			// Below the page's top bar when it has one, sliding to match as pages
			// change; `translate` stays clear of the enter animation's transform.
			className={cn("harness-update-notice pointer-events-auto absolute top-3 right-3 w-[min(320px,calc(100%-24px))] text-card-foreground will-change-transform transition-[translate] duration-200 ease-out motion-reduce:transition-none",
				covered && "invisible", "toast-enter")}
			// The window's drag region would otherwise swallow the notice's clicks.
			style={{ WebkitAppRegion: "no-drag", translate: `0 ${panel?.topInset ?? 0}px` } as CSSProperties}>
			{children}
		</aside>
	</div>;
}

/** Whether the notice overlaps the live native browser page. */
export function noticeCoversLiveBrowser(browser: ViewportRect | null, notice: ViewportRect | null): boolean {
	if (!browser || !notice) return false;
	return notice.left < browser.right && notice.right > browser.left && notice.top < browser.bottom && notice.bottom > browser.top;
}
