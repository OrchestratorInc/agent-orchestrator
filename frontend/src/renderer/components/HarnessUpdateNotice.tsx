import { useQueries, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ChevronDown, CircleCheck, LoaderCircle } from "@/components/app-icons";
import { type CSSProperties, type ReactNode, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { agentAuthPlansQueryOptions } from "../hooks/useAgentAuth";
import { agentReadinessQueryOptionsForHost, cacheAgentReadiness, ensureAgentReadiness, useEnsuredReadinessHosts } from "../hooks/useAgentReadinessQuery";
import { CLOUD_HARNESS_HOST, CLOUD_VERSIONED_HARNESSES, describeCloudHarness, useCloudHarnessVersions, type CloudHarnessDetails } from "../hooks/useCloudHarnessVersions";
import { advisoryInUse, hasConfirmedHarnessUpdate, harnessUpdateNoticeKey, installedAgentIds, installerQueryKeyForHost, installersQueryOptions, installJobsQueryKeyForHost, installJobsQueryOptions, isActiveInstallJob, isFailedInstallJob, maintenanceMethodId, needsHarnessLogin, updateAdvisoryQueryKey, useHarnessUpdatesOnHosts, useStartHarnessUpdateOnHost, versionLabel, type AgentUpdateAdvisory, type HarnessInUse, type InstallJob } from "../hooks/useHarnessUpdates";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { useLiveBrowserViewportRect, type ViewportRect } from "../hooks/useLiveBrowserViewportRect";
import { useCenterPanelBox } from "../hooks/useCenterPanelBox";
import { useSwipeToDismiss } from "../hooks/useSwipeToDismiss";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { aoBridge } from "../lib/bridge";
import { labelForHost } from "../lib/host-clients";
import { isLocal, LOCAL_HOST, refKey, type HostId } from "../lib/hosts";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const DISMISSED_KEY = "ao.harness-update-dismissals.v1";

function readDismissals(): Set<string> {
	try {
		const saved: unknown = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((entry): entry is string => typeof entry === "string").slice(-100) : []);
	} catch { return new Set(); }
}

/** One harness on one host: the same harness on two hosts never shares state. */
type UpdateTarget = { hostId: HostId; agentId: AgentId };

function targetKey({ hostId, agentId }: UpdateTarget): string {
	return refKey({ host: hostId, id: agentId });
}

/** The host argument the API helpers take, which is none for this computer. */
function apiHost(hostId: HostId): string | undefined {
	return isLocal(hostId) ? undefined : hostId;
}

function without<T>(current: ReadonlySet<T>, value: T): Set<T> {
	const next = new Set(current);
	next.delete(value);
	return next;
}

/** Always mounted in the shell, not in a route or Settings. Read-only checks
 * and updates share the Harness page's queries; a login hands off to that page.
 * It covers this computer and every connected remote host, each checked by its
 * own daemon, and an update runs on the host it names. */
export function HarnessUpdateNotice({ enabled }: { enabled: boolean }) {
	const { t, i18n } = useTranslation();
	const queryClient = useQueryClient();
	const remotes = useConnectedHosts();
	// Each connected remote in the order it connected, then this computer: a
	// long local list would otherwise bury the remotes at the bottom of the stack.
	const hostIds = useMemo(() => [...remotes.filter((hostId) => !isLocal(hostId)), LOCAL_HOST], [remotes]);
	const ensured = useEnsuredReadinessHosts(hostIds, { enabled, retryOnError: true });
	const readinessQueries = useQueries({ queries: hostIds.map((hostId) => ({ ...agentReadinessQueryOptionsForHost(apiHost(hostId)), enabled })) });
	const installerQueries = useQueries({ queries: hostIds.map((hostId) => ({ ...installersQueryOptions(apiHost(hostId)), enabled })) });
	const authPlanQueries = useQueries({ queries: hostIds.map((hostId) => ({ ...agentAuthPlansQueryOptions(apiHost(hostId)), enabled })) });
	const jobQueries = useQueries({ queries: hostIds.map((hostId) => ({ ...installJobsQueryOptions(apiHost(hostId)), enabled })) });
	const installedByHost = hostIds.map((_, index) => installedAgentIds(readinessQueries[index].data));
	const advisoriesByHost = useHarnessUpdatesOnHosts(hostIds.map((hostId, index) => ({
		hostId: apiHost(hostId), installed: installedByHost[index], enabled: ensured.has(hostId) && !readinessQueries[index].isError,
	})));
	const hosts = hostIds.map((hostId, index) => ({
		hostId,
		readiness: readinessQueries[index],
		installers: installerQueries[index],
		authPlans: authPlanQueries[index],
		jobs: jobQueries[index],
		installed: installedByHost[index],
		advisories: advisoriesByHost[index],
	}));
	const hostById = new Map(hosts.map((host) => [host.hostId, host]));
	const startHarnessUpdate = useStartHarnessUpdateOnHost();
	// Everything below is keyed by targetKey, so equal harnesses on two hosts stay apart.
	const observedJobs = useRef(new Set<string>());
	const [dismissed, setDismissed] = useState(readDismissals);
	const [expanded, setExpanded] = useState(false);
	// Updates started from a notice run right here; the notice shows progress
	// and any failure instead of sending the user to Settings.
	const [starting, setStarting] = useState<ReadonlySet<string>>(new Set());
	// Harnesses whose finished job is still refreshing its advisory: until the
	// refresh lands, the old advisory would offer Update again for a moment.
	const [settling, setSettling] = useState<ReadonlySet<string>>(new Set());
	const startingRef = useRef(new Set<string>());
	const updateAllInFlight = useRef(false);
	const [startingAll, setStartingAll] = useState(false);
	const [errors, setErrors] = useState<Partial<Record<string, string>>>({});
	const [updateAllSummary, setUpdateAllSummary] = useState<string | null>(null);
	// Updates started here that succeeded, by the version they moved to: shown
	// as done for a moment once the advisory stops offering them, so success
	// never looks like the notice simply vanishing.
	const [updated, setUpdated] = useState<ReadonlyMap<string, UpdateTarget & { version: string }>>(new Map());
	const startedJobs = useRef(new Set<string>());
	// The advisory each update started here acted on, so its failure can still
	// be shown after a refresh stops reporting a newer release.
	const startedUpdates = useRef(new Map<string, AgentUpdateAdvisory>());
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
	const hostLabel = (hostId: HostId) => isLocal(hostId) ? t("settings.harness.thisComputer") : labelForHost(hostId) ?? hostId;
	// Names the host only off this computer, so a local update reads as it always has.
	const targetLabel = ({ hostId, agentId }: UpdateTarget) => isLocal(hostId) ? agentLabel(agentId)
		: t("settings.harness.agentOnHost", { agent: agentLabel(agentId), host: hostLabel(hostId) });
	// AO Cloud is one more place a harness runs: its update joins that
	// harness's entry for this computer, for someone who may change it, never
	// a card of its own. Its state is keyed by harness: only this computer's
	// entry carries it.
	const cloud = useCloudHarnessVersions(enabled);
	const [cloudStarting, setCloudStarting] = useState<ReadonlySet<AgentId>>(new Set());
	const cloudStartingRef = useRef(new Set<AgentId>());
	const [cloudErrors, setCloudErrors] = useState<Partial<Record<AgentId, string>>>({});
	// The cloud update each failed attempt acted on, so its failure stays shown.
	const failedCloudUpdates = useRef(new Map<AgentId, CloudHarnessDetails>());

	// Refresh even on failure: a command can change the binary before
	// failing verification. Never keep advertising an obsolete version.
	const refreshAfterJob = (target: UpdateTarget) => {
		const key = targetKey(target);
		const host = apiHost(target.hostId);
		setSettling((current) => new Set([...current, key]));
		void ensureAgentReadiness([target.agentId], "display", host).then(async (result) => {
			cacheAgentReadiness(queryClient, result, host);
			await queryClient.invalidateQueries({ queryKey: updateAdvisoryQueryKey(target.agentId, host) });
		}).catch(() => undefined).finally(() => {
			setSettling((current) => without(current, key));
		});
		void queryClient.invalidateQueries({ queryKey: installerQueryKeyForHost(host) });
	};
	const showUpdated = (target: UpdateTarget, job: InstallJob) => {
		const key = targetKey(target);
		const version = startedUpdates.current.get(key)?.latestVersion;
		if (job.status !== "succeeded" || !startedJobs.current.has(key) || !version) return;
		setUpdated((current) => new Map(current).set(key, { ...target, version }));
		window.setTimeout(() => setUpdated((current) => {
			const next = new Map(current);
			next.delete(key);
			return next;
		}), UPDATED_MS);
	};
	// Runs after every render: a job is acted on once, when it stops running,
	// so a pass that finds nothing new does nothing.
	useEffect(() => {
		if (!enabled) return;
		for (const { hostId, jobs } of hosts) {
			for (const job of jobs.data ?? []) {
				const target = { hostId, agentId: job.target as AgentId };
				const key = targetKey(target);
				if (isActiveInstallJob(job)) { observedJobs.current.add(key); continue; }
				if (observedJobs.current.delete(key)) {
					refreshAfterJob(target);
					showUpdated(target, job);
				}
			}
		}
	});

	/** Resolves to the sessions that kept the update from starting, if any. */
	const startUpdate = async (target: UpdateTarget, advisory: AgentUpdateAdvisory, methodId: string): Promise<HarnessInUse | undefined> => {
		const { hostId, agentId } = target;
		const key = targetKey(target);
		if (startingRef.current.has(key) || isActiveInstallJob(queryClient.getQueryData<InstallJob[]>(installJobsQueryKeyForHost(apiHost(hostId)))?.find((job) => job.target === agentId))) return undefined;
		startedUpdates.current.set(key, advisory);
		startingRef.current.add(key);
		setStarting((current) => new Set([...current, key]));
		setErrors((current) => ({ ...current, [key]: undefined }));
		try {
			const result = await startHarnessUpdate(apiHost(hostId), { agentId, methodId });
			if (result.inUse) {
				// The notice only updates; a refusal is explained, never a session list.
				const refusal = result.inUse.starting ? t("settings.harness.inUseStarting", { agent: agentLabel(agentId) })
					: t("settings.harness.inUseBlocked", { count: result.inUse.count, agent: agentLabel(agentId) });
				setErrors((current) => ({ ...current, [key]: refusal }));
				return result.inUse;
			}
			// The click-time check may have moved the target to a newer release.
			if (result.advisory) startedUpdates.current.set(key, result.advisory);
			if (result.error) setErrors((current) => ({ ...current, [key]: result.error }));
			if (!result.job) return undefined;
			startedJobs.current.add(key);
			// A job that already finished never shows up as running, so refresh it now.
			if (!isActiveInstallJob(result.job)) {
				refreshAfterJob(target);
				showUpdated(target, result.job);
			}
			return undefined;
		} finally {
			startingRef.current.delete(key);
			setStarting((current) => without(current, key));
		}
	};

	/** Saves AO Cloud's newer release for new Cloud sessions; it has no job to
	 * watch. alone: the entry has no update on this computer, whose finished
	 * job would otherwise say it is done. */
	const startCloudUpdate = async (agentId: AgentId, details: CloudHarnessDetails, alone: boolean) => {
		if (cloudStartingRef.current.has(agentId)) return;
		cloudStartingRef.current.add(agentId);
		setCloudStarting(new Set(cloudStartingRef.current));
		setCloudErrors((current) => ({ ...current, [agentId]: undefined }));
		failedCloudUpdates.current.delete(agentId);
		try {
			const result = await cloud.startUpdate(agentId);
			if (result.error) {
				failedCloudUpdates.current.set(agentId, details);
				setCloudErrors((current) => ({ ...current, [agentId]: result.error }));
			} else if (result.entry?.advisory.currentVersion && alone) {
				const version = result.entry.advisory.currentVersion;
				const key = targetKey({ hostId: LOCAL_HOST, agentId });
				setUpdated((current) => new Map(current).set(key, { hostId: LOCAL_HOST, agentId, version }));
				window.setTimeout(() => setUpdated((current) => {
					const next = new Map(current);
					next.delete(key);
					return next;
				}), UPDATED_MS);
			}
		} finally {
			cloudStartingRef.current.delete(agentId);
			setCloudStarting(new Set(cloudStartingRef.current));
		}
	};

	// Settings is a modal that blocks pointer and focus outside it; its Harness
	// page offers the same updates there.
	if (settingsOpen) return null;
	// Only advertise updates AO can run without a login or manual maintenance.
	// Each host joins once its own checks are in, so ineligible harnesses never
	// flash in the stack and a slow or offline remote never holds back this
	// computer's updates. A failed background refetch keeps the cached data, so
	// an update in flight or its failure stays visible. Within a host, let the
	// first batch settle so a fast, seldom-used harness cannot jump ahead of the
	// most-used one; failed lookups don't block the other results. AO Cloud's
	// answer joins this computer's first batch, so a harness never gains its
	// cloud target after its entry is up.
	const settled = hosts.filter((host) => ensured.has(host.hostId) && !host.readiness.isError && host.authPlans.data && host.installers.data
		&& !AGENT_OPTIONS.some((id) => host.installed.has(id) && host.advisories.get(id)?.isPending)
		&& !(isLocal(host.hostId) && cloud.isPending));
	const readinessByHost = new Map(hosts.map(({ hostId, readiness }) => [hostId, new Map(readiness.data?.agents.map((agent) => [agent.id, agent]))]));
	const readinessFor = ({ hostId, agentId }: UpdateTarget) => readinessByHost.get(hostId)?.get(agentId);
	const planFor = ({ hostId, agentId }: UpdateTarget) => hostById.get(hostId)?.installers.data?.find((plan) => plan.agentId === agentId);
	const methodFor = (target: UpdateTarget, advisory: AgentUpdateAdvisory) => planFor(target)?.methods.find((candidate) => candidate.id === maintenanceMethodId(advisory));
	const authPlanFor = ({ hostId, agentId }: UpdateTarget) => hostById.get(hostId)?.authPlans.data?.find((plan) => plan.agentId === agentId);
	const needsLoginFor = (target: UpdateTarget) => needsHarnessLogin(authPlanFor(target), readinessFor(target)?.authentication.state);
	const jobFor = ({ hostId, agentId }: UpdateTarget) => hostById.get(hostId)?.jobs.data?.find((candidate) => candidate.target === agentId);
	// An update started here that failed stays explained until it is dismissed,
	// even once a refresh reports the harness current: an updater can replace
	// the binary and still exit with an error.
	const failedUpdateAdvisory = (target: UpdateTarget) => {
		const key = targetKey(target);
		const advisory = startedUpdates.current.get(key);
		return advisory && (errors[key] || (startedJobs.current.has(key) && isFailedInstallJob(jobFor(target)))) ? advisory : undefined;
	};
	const hostUpdate = (host: (typeof hosts)[number], target: UpdateTarget) => {
		const query = host.advisories.get(target.agentId);
		const confirmed = host.installed.has(target.agentId) && !query?.isError && hasConfirmedHarnessUpdate(query?.data) ? query?.data : undefined;
		const advisory = confirmed ?? failedUpdateAdvisory(target);
		if (!advisory) return undefined;
		const method = methodFor(target, advisory);
		if (!method?.available || !method.updateAvailable || needsLoginFor(target)) return undefined;
		const key = harnessUpdateNoticeKey(target.agentId, advisory.latestVersion!, target.hostId);
		return dismissed.has(key) ? undefined : { advisory, key };
	};
	// Only an update the person can make: someone who may not change AO
	// Cloud's version never sees one.
	const cloudUpdate = (agentId: AgentId) => {
		if (!CLOUD_VERSIONED_HARNESSES.has(agentId)) return undefined;
		const current = describeCloudHarness(cloud.data, agentId);
		const details = current?.actionable ? current : cloudErrors[agentId] ? failedCloudUpdates.current.get(agentId) : undefined;
		if (!details?.latestVersion) return undefined;
		const key = harnessUpdateNoticeKey(agentId, details.latestVersion, CLOUD_HARNESS_HOST);
		return dismissed.has(key) ? undefined : { details, key };
	};
	const updates = settled.flatMap((host) => AGENT_OPTIONS.flatMap((agentId) => {
		const target = { hostId: host.hostId, agentId };
		const own = hostUpdate(host, target);
		const cloudTarget = isLocal(host.hostId) ? cloudUpdate(agentId) : undefined;
		if (!own && !cloudTarget) return [];
		return [{ ...target, id: targetKey(target), advisory: own?.advisory, key: own?.key ?? cloudTarget!.key,
			keys: [own?.key, cloudTarget?.key].filter((key): key is string => Boolean(key)), cloud: cloudTarget?.details }];
	}).sort((left, right) => {
		const a = readinessFor(left);
		const b = readinessFor(right);
		const usage = (b?.usageCount ?? 0) - (a?.usageCount ?? 0);
		const recent = (Date.parse(b?.lastUsedAt ?? "") || 0) - (Date.parse(a?.lastUsedAt ?? "") || 0);
		return usage || recent || agentLabel(left.agentId).localeCompare(agentLabel(right.agentId)) || left.agentId.localeCompare(right.agentId);
	}));
	const updatedCards = [...updated].filter(([key]) => !updates.some((update) => update.id === key));
	if (updates.length === 0 && updatedCards.length === 0) return null;
	// The hosts with updates, in the stack's order. A list that spans several is
	// grouped under host headings; one remote alone is named on the stack.
	const groups = hostIds.flatMap((hostId) => {
		const items = updates.filter((update) => update.hostId === hostId);
		return items.length > 0 ? [{ hostId, items }] : [];
	});
	const grouped = groups.length > 1;
	const soleRemote = groups.length === 1 && !isLocal(groups[0].hostId) ? groups[0].hostId : undefined;
	const dismiss = (...keys: string[]) => {
		setDismissed((current) => {
			const next = new Set([...current, ...keys].slice(-100));
			try { localStorage.setItem(DISMISSED_KEY, JSON.stringify([...next])); } catch { /* In-memory dismissal still works. */ }
			return next;
		});
		if (updates.length <= 2) setExpanded(false);
	};
	const isExpanded = expanded && updates.length > 1;
	const hostReady = (hostId: HostId) => {
		const host = hostById.get(hostId);
		return Boolean(host && !host.authPlans.isPending && !host.authPlans.isError && !host.installers.isPending && !host.installers.isError && !host.jobs.isPending && !host.jobs.isError);
	};
	const cloudUpdateAllTargets = updates.flatMap(({ agentId, advisory, cloud: details }) =>
		details?.actionable && !cloudStarting.has(agentId) ? [{ agentId, details, alone: !advisory }] : []);
	const updateAllTargets = updates.flatMap((update) => {
		const { advisory } = update;
		if (!advisory) return [];
		const method = methodFor(update, advisory);
		if (!hostReady(update.hostId) || !method?.available || !method.updateAvailable || needsLoginFor(update) || starting.has(update.id) || settling.has(update.id)
			|| isActiveInstallJob(jobFor(update))) return [];
		return [{ target: { hostId: update.hostId, agentId: update.agentId }, advisory, methodId: method.id }];
	});
	const runAllUpdates = async (targets: typeof updateAllTargets, skipped: Array<{ target: UpdateTarget; count: number }>) => {
		if (updateAllInFlight.current) return;
		updateAllInFlight.current = true;
		setStartingAll(true);
		setExpanded(true);
		setUpdateAllSummary(null);
		let started = 0;
		// AO Cloud's side only saves a release, so it never waits on a session.
		for (const target of cloudUpdateAllTargets) void startCloudUpdate(target.agentId, target.details, target.alone);
		try {
			// Each notice keeps its existing target/owner recheck and error display.
			for (const target of targets) {
				const refused = await startUpdate(target.target, target.advisory, target.methodId);
				if (refused) skipped.push({ target: target.target, count: refused.count });
				else started += 1;
			}
		} finally {
			updateAllInFlight.current = false;
			setStartingAll(false);
		}
		if (skipped.length > 0) {
			setUpdateAllSummary(t("settings.harness.updateAllSummary", {
				count: started,
				skipped: skipped.map(({ target, count }) => t("settings.harness.updateAllSkipped", { agent: targetLabel(target), count })).join(", "),
			}));
		}
	};
	// Harnesses whose sessions must end first are skipped and named in the summary.
	const startAllUpdates = () => {
		if (updateAllInFlight.current || (updateAllTargets.length === 0 && cloudUpdateAllTargets.length === 0)) return;
		const skipped: Array<{ target: UpdateTarget; count: number }> = [];
		const runnable = updateAllTargets.filter(({ target, advisory }) => {
			const sessions = advisoryInUse(advisory);
			if (!sessions) return true;
			skipped.push({ target, count: sessions.count });
			return false;
		});
		void runAllUpdates(runnable, skipped);
	};

	// While updates start the button stays solid: it reports progress, not an
	// unavailable action.
	const updateAllButton = <Button type="button" size="sm" className={cn("min-w-20 shrink-0", startingAll && "disabled:opacity-100")}
		aria-busy={startingAll || undefined}
		disabled={startingAll || (updateAllTargets.length === 0 && cloudUpdateAllTargets.length === 0)} onClick={() => void startAllUpdates()}>
		{startingAll ? <LoaderCircle className="size-icon-2xs animate-spin" aria-hidden="true" /> : null}
		<span>{startingAll ? t("settings.harness.startingAllUpdates")
			: t("settings.harness.updateAll")}</span>
	</Button>;
	const help = t("settings.harness.updateNoticeHelp");
	const updateItem = (update: (typeof updates)[number], variant: "card" | "row") => {
		const { hostId, agentId, advisory, key, keys, id, cloud: cloudDetails } = update;
		const plan = planFor(update);
		const job = jobFor(update);
		const method = advisory ? methodFor(update, advisory) : undefined;
		const authPlan = authPlanFor(update);
		const needsLogin = Boolean(advisory) && needsLoginFor(update);
		const canUpdate = Boolean(method?.available && method.updateAvailable) || Boolean(cloudDetails?.actionable);
		// Cloud's state is per harness, so only the entry carrying it reads it.
		const busy = isActiveInstallJob(job) || starting.has(id) || settling.has(id) || (Boolean(cloudDetails) && cloudStarting.has(agentId));
		const ready = hostReady(hostId);
		// A job this notice started that failed stays explained here.
		const failed = startedJobs.current.has(id) && isFailedInstallJob(job);
		const error = errors[id] ?? (failed ? job?.error || t("settings.harness.updateJobFailed") : undefined) ?? (cloudDetails ? cloudErrors[agentId] : undefined);
		// A start error means this attempt made no job; any retained job is an older one.
		const failedJobStartedAt = errors[id] === undefined && failed ? job?.startedAt ?? undefined : undefined;
		// The Harness page opens on the host this update belongs to.
		const harnessPage = { focusAgentId: agentId, hostId, harnessView: "local", preserveProject: true } as const;
		const requestAction = () => {
			if (cloudDetails?.actionable) void startCloudUpdate(agentId, cloudDetails, !advisory);
			if (!advisory) return;
			if (!needsLogin && method) {
				void startUpdate(update, advisory, method.id);
				return;
			}
			// A login runs in a terminal on the Harness page, which starts it once on open.
			openSettings("harness", { ...harnessPage, startLogin: true });
		};
		// Which places one Update covers, with each one's versions on hover.
		const targets = cloudDetails ? <Tooltip>
			<TooltipTrigger asChild>
				<p tabIndex={0} className="mt-1 w-fit text-caption text-muted-foreground">{advisory ? t("settings.harness.cloud.targetBoth", { host: t("settings.harness.thisComputer") }) : t("settings.harness.cloud.label")}</p>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-64">{advisory
				? t("settings.harness.cloud.targetVersions", { host: t("settings.harness.thisComputer"), local: versionLabel(advisory.currentVersion!), localLatest: versionLabel(advisory.latestVersion!), cloud: versionLabel(cloudDetails.currentVersion), cloudLatest: versionLabel(cloudDetails.latestVersion!) })
				: t("settings.harness.cloud.versionAvailable", { version: versionLabel(cloudDetails.latestVersion!) })}</TooltipContent>
		</Tooltip> : null;
		const versions = advisory ? <VersionChange from={advisory.currentVersion!} to={advisory.latestVersion!} /> : <VersionChange from={cloudDetails!.currentVersion} to={cloudDetails!.latestVersion!} />;
		return <SwipeCard key={key} variant={variant} label={targetLabel(update)} onDismiss={() => dismiss(...keys)}
			dismissLabel={t("settings.harness.dismissAgentUpdate", { agent: targetLabel(update) })}
			icon={<AgentAvatar className="size-icon-xl shrink-0" decorative provider={agentId} />}
			title={variant === "row" ? agentLabel(agentId) : t("settings.harness.updateCardTitle", { agent: agentLabel(agentId) })}
			help={variant === "card" ? help : undefined}>
			{targets}
			<div className="mt-2 flex items-center gap-2">
				{/* Rows sit under a host heading or a stack that names the host. */}
				{variant === "card" && !isLocal(hostId) ? <HostTag label={hostLabel(hostId)} /> : null}
				{versions}
				{/* One width for every label, so Updating… never pushes the versions around. */}
				{busy ? <Button size="sm" variant="secondary" className="min-w-20 disabled:opacity-100" aria-busy="true" disabled><LoaderCircle className="size-icon-2xs animate-spin" aria-hidden="true" />{t("settings.harness.updating")}</Button>
				: (ready || !advisory) && ((needsLogin && authPlan?.available) || (!needsLogin && canUpdate)) ? <Button size="sm" className="min-w-20" disabled={startingAll} onClick={requestAction}>{needsLogin ? t(authPlan?.action === "setup" ? "settings.harness.setup" : "settings.harness.login")
					: t(error ? "settings.harness.retryUpdate" : "settings.harness.update")}</Button>
				: ready && advisory && !needsLogin && plan?.documentationUrl ? <Tooltip>
					<TooltipTrigger asChild><Button size="sm" variant="secondary" onClick={() => void aoBridge.app.openExternal(plan.documentationUrl)}>{t("settings.harness.manualUpdate")}</Button></TooltipTrigger>
					<TooltipContent side="top" className="max-w-64">{method?.updateReason || t("settings.harness.ownershipUnknown")}</TooltipContent>
				</Tooltip>
				: <Button size="sm" variant="secondary" onClick={() => openSettings("harness", harnessPage)}>{t("settings.harness.viewDetails")}</Button>}
			</div>
			{/* One click to the Harness page with this failure's diagnostics open, ready to copy. */}
			{error && !busy ? <div className="mt-2 flex flex-col items-start gap-1.5">
				<p role="alert" className="line-clamp-3 break-words text-caption text-error">{error}</p>
				<Button type="button" size="sm" variant="secondary" onClick={() => openSettings("harness", { ...harnessPage, showDiagnostics: { jobStartedAt: failedJobStartedAt, error } })}>{t("settings.harness.showDiagnostics")}</Button>
			</div> : null}
		</SwipeCard>;
	};
	const updatedItems = updatedCards.map(([key, target]) => <SwipeCard key={`updated:${key}`} variant="card" label={targetLabel(target)} role="status"
		onDismiss={() => setUpdated((current) => {
			const next = new Map(current);
			next.delete(key);
			return next;
		})}
		dismissLabel={t("settings.harness.dismissAgentUpdate", { agent: targetLabel(target) })}
		icon={<span className="relative shrink-0">
			<AgentAvatar className="size-icon-xl" decorative provider={target.agentId} />
			<CircleCheck className="absolute -bottom-1 -right-1 size-icon-2xs rounded-full bg-settings-menu text-success" aria-hidden="true" />
		</span>}
		title={t("settings.harness.updatedCardTitle", { agent: agentLabel(target.agentId) })}>
		<div className="mt-2 flex items-center gap-2">
			{!isLocal(target.hostId) ? <HostTag label={hostLabel(target.hostId)} /> : null}
			<p className="min-w-0 text-caption text-muted-foreground">{t("settings.harness.updatedTo", { version: friendlyVersion(target.version, i18n.resolvedLanguage ?? i18n.language) })}</p>
		</div>
	</SwipeCard>);
	const stackTitle = t("settings.harness.updateStackTitle", { count: updates.length });
	const dismissAll = () => dismiss(...updates.flatMap((update) => update.keys));
	// Portaled to the end of <body>: Electron applies window drag regions in DOM
	// order, so a later drag strip (the home page's) would otherwise override no-drag.
	return createPortal(<NoticeFrame label={t("settings.harness.updateNoticeTitle")}>
		<div className="space-y-2">
		{updates.length > 1 ? <SwipeCard variant="card" role="group" label={t("settings.harness.updateNoticeTitle")} onDismiss={dismissAll} swipeable={!isExpanded}
			dismissLabel={t("settings.harness.dismissAllUpdates")}
			// Overlapping marks, each cut out of the one beneath by a halo in the card's colour.
			icon={<span className="flex shrink-0 items-center -space-x-1.5">
				{updates.slice(0, 3).map(({ agentId, id }) => <AgentAvatar key={id} className="relative size-icon-xl [filter:drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_1px_var(--color-bg-settings-menu))_drop-shadow(0_0_2px_var(--color-bg-settings-menu))_drop-shadow(0_0_3px_var(--color-bg-settings-menu))]" decorative provider={agentId} />)}
			</span>}
			title={stackTitle}
			// The Update all summary takes the help line's place while it is shown.
			help={updateAllSummary && !startingAll ? updateAllSummary : help}
			footer={<>
				{/* The list toggle keeps one place, opposite Update all, whichever way it points. */}
				<Button type="button" size="sm" variant="secondary" className="shrink-0" aria-expanded={isExpanded} aria-controls={stackId}
					onClick={() => setExpanded(!isExpanded)}>
					{isExpanded ? t("settings.harness.hideUpdateStack") : t("settings.harness.showUpdateStack", { count: updates.length })}
					<ChevronDown className={cn("size-icon-2xs transition-transform duration-200 motion-reduce:transition-none", isExpanded && "rotate-180")} aria-hidden="true" />
				</Button>
				<span className="flex-1" />
				{updateAllButton}
			</>}>
			{grouped || soleRemote ? <div className="mt-2 flex">
				<HostTag label={soleRemote ? hostLabel(soleRemote) : t("settings.harness.updateHostCount", { count: groups.length })} />
			</div> : null}
			{/* Mounted while closed, so opening only animates: building every row on
			    the click would stall the first frames. */}
			<div inert={!isExpanded} aria-hidden={!isExpanded || undefined}
				className={cn("-mx-3 overflow-hidden transition-[height,opacity] duration-200 ease-out [interpolate-size:allow-keywords] motion-reduce:transition-none",
					isExpanded ? "h-auto opacity-100" : "h-0 opacity-0")}>
				<div id={stackId} ref={stackRef} onScroll={updateStackCue}
					className={cn("mt-2 max-h-[min(280px,calc(100dvh-240px))] divide-y divide-(--color-border-overlay) overflow-x-hidden overflow-y-auto overscroll-contain border-y border-(--color-border-overlay)",
						stackOverflows && "[mask-image:linear-gradient(to_bottom,black_calc(100%-32px),transparent)]")}>
					{grouped ? groups.map(({ hostId, items }) => <section key={hostId} aria-label={hostLabel(hostId)}>
						<h3 className="truncate px-3 pt-2 text-caption font-medium text-muted-foreground">{hostLabel(hostId)}</h3>
						<div className="divide-y divide-(--color-border-overlay)">{items.map((update) => updateItem(update, "row"))}</div>
					</section>) : updates.map((update) => updateItem(update, "row"))}
				</div>
			</div>
		</SwipeCard> : updates.map((update) => updateItem(update, "card"))}
		{updatedItems}
		</div>
	</NoticeFrame>, document.body);
}

/** The host an update belongs to, named the way the sidebar names a session's host. */
function HostTag({ label }: { label: string }) {
	return <Badge variant="outline" className="h-4 min-w-0 max-w-1/2 shrink justify-start px-1.5 text-2xs"><span className="truncate">{label}</span></Badge>;
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
			<CloseButton aria-label={dismissLabel} className="-mr-1" onClick={leave} size="icon-xs" />
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
