import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, CircleArrowUp, LoaderCircle, X } from "lucide-react";
import { createContext, type ReactNode, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAgentAuthPlans } from "../hooks/useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness, useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { advisoryInUse, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installedAgentIds, installerQueryKey, installersQueryOptions, installJobsQueryKey, installJobsQueryOptions, isActiveInstallJob, isFailedInstallJob, isWaitingForIdleJob, maintenanceMethodId, needsHarnessLogin, updateAdvisoryQueryKey, useHarnessUpdates, useStartHarnessUpdate, versionLabel, type AgentUpdateAdvisory, type HarnessInUse, type InstallJob } from "../hooks/useHarnessUpdates";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { MiddleTruncatedText } from "./MiddleTruncatedText";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { HarnessSessionsDialog, IdleUpdateStatus } from "./settings/harness/HarnessSessionsDialog";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const DISMISSED_KEY = "ao.harness-update-dismissals.v1";

function readDismissals(): Set<string> {
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((entry): entry is string => typeof entry === "string").slice(-100) : []);
	} catch { return new Set(); }
}

type HarnessUpdate = { agentId: AgentId; advisory: AgentUpdateAdvisory; key: string };

type HarnessUpdatesModel = ReturnType<typeof useHarnessUpdatesModel>;

const HarnessUpdatesContext = createContext<HarnessUpdatesModel>(null);

/** Read-only checks and updates share the Harness page's queries; a login hands
 * off to that page. One instance per sidebar footer, so the expanded row and
 * the icon-rail button show the same progress, errors and dismissals. */
export function HarnessUpdatesProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
	const model = useHarnessUpdatesModel(enabled);
	return <HarnessUpdatesContext.Provider value={model}>
		{children}
		{model ? <HarnessSessionsDialog
			agentId={model.sessionsView?.agentId ?? null}
			inUse={model.sessionsView?.inUse}
			onOpenChange={(open) => { if (!open) model.closeSessionsView(); }}
		/> : null}
	</HarnessUpdatesContext.Provider>;
}

function useHarnessUpdatesModel(enabled: boolean) {
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
	// Updates started here run in place; the list shows progress and any
	// failure instead of sending the user to Settings.
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
	// Wait for these checks so ineligible harnesses never flash in the list. A
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
	const updates: HarnessUpdate[] = AGENT_OPTIONS.flatMap((agentId) => {
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
	const dismiss = (key: string) => {
		setDismissed((current) => {
			const next = new Set([...current, key].slice(-100));
			try { localStorage.setItem(DISMISSED_KEY, JSON.stringify([...next])); } catch { /* In-memory dismissal still works. */ }
			return next;
		});
	};
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
		setUpdateAllSummary(null);
		let started = 0;
		try {
			// Each row keeps its existing target/owner recheck and error display.
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
	const rowState = (agentId: AgentId, advisory: AgentUpdateAdvisory) => {
		const plan = installers.data?.find((candidate) => candidate.agentId === agentId);
		const job = jobs.data?.find((candidate) => candidate.target === agentId);
		const method = plan?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
		const authPlan = authPlans.data?.find((candidate) => candidate.agentId === agentId);
		const needsLogin = needsHarnessLogin(authPlan, readinessById.get(agentId)?.authentication.state);
		const canUpdate = Boolean(method?.available && method.updateAvailable);
		const busy = isActiveInstallJob(job) || starting.has(agentId) || settling.has(agentId);
		// A job this list started that failed stays explained here.
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
		return { plan, job, method, authPlan, needsLogin, canUpdate, busy, error, sessions, failedJobStartedAt, requestAction, blockedBy: inUse[agentId] };
	};
	return {
		updates,
		ready,
		startingAll,
		canUpdateAll: updateAllTargets.length > 0,
		startAllUpdates,
		updateAllSummary,
		dismiss,
		rowState,
		sessionsView,
		showSessions: (agentId: AgentId, sessions: HarnessInUse) => setSessionsView({ agentId, inUse: sessions }),
		closeSessionsView: () => {
			if (!sessionsView) return;
			// The click-time answer is a snapshot: once its sessions are seen, the
			// next click checks again instead of reopening the same list.
			const { agentId } = sessionsView;
			setInUse((current) => ({ ...current, [agentId]: undefined }));
			setSessionsView(null);
		},
		openSettings,
	};
}

/** Expanded sidebar footer row: icon, label and a muted count. Hidden when no
 * agent has an update. */
export function HarnessUpdatesRow({ className, tabIndex }: { className?: string; tabIndex?: number }) {
	const { t } = useTranslation();
	const model = useContext(HarnessUpdatesContext);
	const [open, setOpen] = useState(false);
	useEffect(() => { if (tabIndex === -1) setOpen(false); }, [tabIndex]);
	if (!model) return null;
	const count = model.updates.length;
	return <Popover open={open} onOpenChange={setOpen}>
		<PopoverTrigger asChild>
			<button
				aria-label={t("shell.agentUpdatesAvailable", { count })}
				className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, className)}
				data-testid="sidebar-agent-updates"
				tabIndex={tabIndex}
				type="button"
			>
				<NavRowHighlight active={open} />
				<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0">
					<CircleArrowUp aria-hidden="true" />
					<span className="min-w-0 flex-1 truncate tracking-tight">{t("shell.agentUpdates")}</span>
					<span aria-hidden="true" className="shrink-0 text-xs font-normal tabular-nums text-passive">{count}</span>
				</span>
			</button>
		</PopoverTrigger>
		<HarnessUpdatesPopoverContent model={model} />
	</Popover>;
}

/** Collapsed icon rail: the same list behind an icon with a count dot. */
export function HarnessUpdatesRailButton({ className, tabIndex }: { className?: string; tabIndex?: number }) {
	const { t } = useTranslation();
	const model = useContext(HarnessUpdatesContext);
	const [open, setOpen] = useState(false);
	useEffect(() => { if (tabIndex === -1) setOpen(false); }, [tabIndex]);
	if (!model) return null;
	const label = t("shell.agentUpdatesAvailable", { count: model.updates.length });
	return <Popover open={open} onOpenChange={setOpen}>
		<Tooltip>
			<TooltipTrigger asChild>
				<PopoverTrigger asChild>
					<button
						aria-label={label}
						className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, className)}
						data-testid="sidebar-agent-updates-rail"
						tabIndex={tabIndex}
						type="button"
					>
						<NavRowHighlight active={open} />
						<span className="relative z-[1] grid place-items-center [&_svg]:size-icon-base">
							<CircleArrowUp aria-hidden="true" />
							<span aria-hidden="true" className="absolute -right-0.5 -top-0.5 size-1.5 rounded-full bg-foreground" />
						</span>
					</button>
				</PopoverTrigger>
			</TooltipTrigger>
			<TooltipContent side="right">{label}</TooltipContent>
		</Tooltip>
		<HarnessUpdatesPopoverContent model={model} />
	</Popover>;
}

function HarnessUpdatesPopoverContent({ model }: { model: NonNullable<HarnessUpdatesModel> }) {
	const { t } = useTranslation();
	return <PopoverContent
		aria-label={t("shell.agentUpdates")}
		side="right"
		align="end"
		sideOffset={8}
		className="flex max-h-[min(420px,calc(100dvh-32px))] w-96 max-w-[calc(100vw-32px)] flex-col overflow-hidden p-0"
	>
		<div className="flex shrink-0 items-center justify-between gap-2 border-b border-border px-3 py-2">
			<p className="min-w-0 truncate text-sm font-medium">{t("shell.agentUpdates")}</p>
			{/* The popover's one filled action; each row's action is a quiet
			    secondary button, visible at rest, one step below it. */}
			{model.updates.length > 1 ? <Button
				type="button"
				size="sm"
				variant="primary"
				className="h-7 px-2"
				aria-busy={model.startingAll || undefined}
				disabled={model.startingAll || !model.canUpdateAll}
				onClick={model.startAllUpdates}
			>
				{model.startingAll ? <LoaderCircle className="size-3 animate-spin" aria-hidden="true" /> : null}
				{model.startingAll ? t("settings.harness.startingAllUpdates") : t("settings.harness.updateAll")}
			</Button> : null}
		</div>
		<ul className="min-h-0 flex-1 divide-y divide-border overflow-y-auto overscroll-contain">
			{model.updates.map((update) => <HarnessUpdateItem key={update.key} model={model} update={update} />)}
		</ul>
		{model.updateAllSummary && !model.startingAll
			? <p role="status" className="shrink-0 border-t border-border px-3 py-2 text-caption text-muted-foreground">{model.updateAllSummary}</p>
			: null}
	</PopoverContent>;
}

function HarnessUpdateItem({ model, update }: { model: NonNullable<HarnessUpdatesModel>; update: HarnessUpdate }) {
	const { t } = useTranslation();
	const { agentId, advisory, key } = update;
	const { plan, job, method, authPlan, needsLogin, canUpdate, busy, error, sessions, failedJobStartedAt, requestAction, blockedBy } = model.rowState(agentId, advisory);
	const name = agentLabel(agentId);
	const actionClass = "h-7 shrink-0 px-2";
	const action = busy
		? <Button size="sm" variant="ghost" className={cn(actionClass, "text-muted-foreground disabled:opacity-100")} aria-busy="true" disabled>
			<LoaderCircle className="size-3 animate-spin" aria-hidden="true" />
			{t(isWaitingForIdleJob(job) ? "settings.harness.waiting" : "settings.harness.updating")}
		</Button>
		: model.ready && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate))
			? <Button size="sm" variant="secondary" className={actionClass} disabled={model.startingAll} onClick={requestAction}>
				{needsLogin ? t(authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login")
					: sessions ? (sessions.starting ? t("settings.harness.update") : t("settings.harness.viewSessions", { count: sessions.count }))
						: t(error ? "settings.harness.retryUpdate" : "settings.harness.update")}
			</Button>
			: model.ready && !needsLogin && plan?.documentationUrl
				? <Tooltip>
					<TooltipTrigger asChild>
						<Button size="sm" variant="secondary" className={actionClass} onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom" className="max-w-64">{method?.updateReason || t("settings.harness.ownershipUnknown")}</TooltipContent>
				</Tooltip>
				: <Button size="sm" variant="secondary" className={actionClass} onClick={() => model.openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true })}>{t("settings.harness.viewDetails")}</Button>;
	return <li aria-label={name} className="group/update flex flex-col gap-1 px-3 py-2">
		<div className="flex min-w-0 items-center gap-2">
			{/* AgentAvatar's shared frame gives every harness one size, radius and centre. */}
			<AgentAvatar className="size-6" decorative provider={agentId} />
			<div className="min-w-0 flex-1">
				<p className="truncate text-sm font-medium">{name}</p>
				<p className="flex min-w-0 flex-wrap items-center gap-x-1 text-caption"><MiddleTruncatedText className="whitespace-nowrap font-mono text-settings-label" text={versionLabel(advisory.currentVersion!)} /><ArrowRight className="size-3 shrink-0 text-settings-muted" role="img" aria-label={t("settings.harness.availableVersion")} /><MiddleTruncatedText className="whitespace-nowrap font-mono font-medium text-settings-accent" text={versionLabel(advisory.latestVersion!)} /></p>
			</div>
			{action}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button
						type="button"
						variant="ghost"
						size="icon"
						// Reserved slot: hidden until the row is hovered or holds focus, so
						// "Update all" and each row's Update read first.
						className="size-6 shrink-0 text-passive opacity-0 transition-opacity duration-fast hover:text-foreground focus-visible:opacity-100 group-hover/update:opacity-100 group-focus-within/update:opacity-100 motion-reduce:transition-none"
						aria-label={t("settings.harness.dismissAgentUpdate", { agent: name })}
						onClick={() => model.dismiss(key)}
					>
						<X className="size-3.5" aria-hidden="true" />
					</Button>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("settings.harness.dismissAgentUpdate", { agent: name })}</TooltipContent>
			</Tooltip>
		</div>
		{/* One click to the Harness page with this failure's diagnostics open, ready to copy. */}
		{error && !busy ? <div className="flex flex-col items-start gap-1 pl-6">
			<p role="alert" className="line-clamp-3 break-words text-caption text-error">{error}</p>
			<Button type="button" size="sm" variant="ghost" className="h-6 px-1.5 text-caption" onClick={() => model.openSettings("harness", { focusAgentId: agentId, harnessView: "local", preserveProject: true, showDiagnostics: { jobStartedAt: failedJobStartedAt, error } })}>{t("settings.harness.showDiagnostics")}</Button>
		</div> : null}
		{job && isWaitingForIdleJob(job) ? <div className="pl-6"><IdleUpdateStatus agentId={agentId} job={job} onViewSessions={(waiting) => model.showSessions(agentId, waiting)} /></div> : null}
		{!error && !busy && blockedBy ? <p role="status" className="break-words pl-6 text-caption text-muted-foreground">
			{blockedBy.starting ? t("settings.harness.inUseStarting", { agent: name }) : t("settings.harness.inUseBlocked", { count: blockedBy.count, agent: name })}
		</p> : null}
	</li>;
}
