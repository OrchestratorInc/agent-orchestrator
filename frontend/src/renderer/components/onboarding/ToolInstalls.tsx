import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ArrowUpRight } from "@/components/app-icons";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import {
	installersQueryOptions,
	installJobsQueryKey,
	installJobsQueryOptions,
	isActiveInstallJob,
	upsertInstallJob,
} from "../../hooks/useHarnessUpdates";
import type { AgentReadinessSnapshot } from "../../hooks/useAgentReadinessQuery";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ManualCommand } from "../InstallDependencyDialog";
import { installPercent } from "../settings/harness/describe-harness";
import { Button } from "../ui/button";

// Installs on onboarding's Your tools page. Every click shows something at
// once: the row is busy while the request is out, a running install shows a
// progress bar with the installer's latest line (or the command it runs), and
// one that fails, stops or can't run here says why and offers a way on.
//
// Coding agents go through the same per-agent install API as Settings >
// Harness, so every agent the daemon can install gets an Install. The GitHub
// CLI is a system tool and keeps its own install target.

type InstallJob = components["schemas"]["InstallJob"];
type InstallPlan = components["schemas"]["AgentInstallPlan"];

const POLL_MS = 1_000;
// Polls in a row that get no answer before the GitHub CLI install stops waiting (about 30 s).
const POLL_GIVE_UP_AFTER = 30;
const ENDED = new Set<InstallJob["status"]>(["failed", "interrupted", "unsupported"]);

export type AgentRowState = "ready" | "signin" | "missing" | "checking";

/** Where an agent stands on the page: ready, needs signing in, still being checked, or not installed. */
export function agentRowState(agent: AgentReadinessSnapshot): AgentRowState {
	if (agent.effectiveReadiness === "ready") return "ready";
	if (agent.installation.state === "not_installed") return "missing";
	// A check still running: wait for it.
	if (agent.installation.state === "unknown" || agent.authentication.freshness === "checking") return "checking";
	// Installed but not ready: signed out, or AO couldn't tell (an inconclusive
	// check, common right after an install). Signing in settles it either way.
	return "signin";
}

/** What a row shows about its install. */
export type InstallView = {
	/** The request is out or the install is running. */
	busy: boolean;
	/** The latest job, once there is one. */
	job?: InstallJob;
	/** Why the last attempt from this page didn't finish, if it didn't. */
	problem?: string;
	/** The attempt from this page ended without installing (failed, stopped, unsupported). */
	ended: boolean;
};

const IDLE: InstallView = { busy: false, ended: false };

/** Agents: plans and jobs shared with Settings > Harness (the jobs query polls
 * while one runs). `onInstalled` runs once for each install this page started
 * that succeeds. */
export function useAgentInstalls(onInstalled: (agentId: string) => void) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const plans = useQuery(installersQueryOptions());
	const jobs = useQuery(installJobsQueryOptions());
	const [pending, setPending] = useState<ReadonlySet<string>>(new Set());
	const [errors, setErrors] = useState<Record<string, string | undefined>>({});
	// Installs started here, so an old failure from elsewhere isn't shown as this one's.
	const [attempted, setAttempted] = useState<ReadonlySet<string>>(new Set());
	const reported = useRef(new Set<string>());
	const onInstalledRef = useRef(onInstalled);
	onInstalledRef.current = onInstalled;

	const planFor = (agentId: string): InstallPlan | undefined => plans.data?.find((plan) => plan.agentId === agentId);
	const jobFor = (agentId: string): InstallJob | undefined => jobs.data?.find((job) => job.target === agentId);
	/** Whether the daemon can install it in one click on this machine. */
	const canInstall = (agentId: string) => {
		const plan = planFor(agentId);
		return Boolean(plan?.available && plan.automatic && plan.methods.some((method) => method.available));
	};

	useEffect(() => {
		for (const agentId of attempted) {
			const job = jobs.data?.find((candidate) => candidate.target === agentId);
			const key = `${agentId}:${job?.startedAt ?? ""}`;
			if (job?.status !== "succeeded" || reported.current.has(key)) continue;
			reported.current.add(key);
			onInstalledRef.current(agentId);
		}
	}, [attempted, jobs.data]);

	const start = async (agentId: string) => {
		if (pending.has(agentId) || isActiveInstallJob(jobFor(agentId))) return;
		setPending((current) => new Set(current).add(agentId));
		setErrors((current) => ({ ...current, [agentId]: undefined }));
		setAttempted((current) => new Set(current).add(agentId));
		try {
			// No method: the daemon takes the recommended one that works here.
			const { data, error } = await apiClient.POST("/api/v1/agents/{agent}/install", {
				params: { path: { agent: agentId } },
				body: { operation: "install" },
			});
			if (error || !data) {
				setErrors((current) => ({ ...current, [agentId]: apiErrorMessage(error, t("settings.harness.startFailed")) }));
				return;
			}
			queryClient.setQueryData<InstallJob[]>(installJobsQueryKey, (current) => upsertInstallJob(current, data));
			// Polling starts from the next fetch, which sees the running job.
			void queryClient.invalidateQueries({ queryKey: installJobsQueryKey });
		} catch (error) {
			setErrors((current) => ({ ...current, [agentId]: error instanceof Error ? error.message : t("settings.harness.startFailed") }));
		} finally {
			setPending((current) => {
				const next = new Set(current);
				next.delete(agentId);
				return next;
			});
		}
	};

	const view = (agentId: string): InstallView => {
		const job = jobFor(agentId);
		const error = errors[agentId];
		if (!job && !error && !pending.has(agentId)) return IDLE;
		const busy = pending.has(agentId) || isActiveInstallJob(job);
		// A retry in flight hides the last failure until the daemon answers.
		const ended = !busy && attempted.has(agentId) && Boolean(job && ENDED.has(job.status));
		return {
			busy,
			job,
			ended: ended || (!busy && Boolean(error)),
			problem: busy ? undefined : error ?? (ended ? job?.error || t("firstRun.installDidNotFinish") : undefined),
		};
	};

	return { plansReady: !plans.isPending, planFor, canInstall, start, view };
}

/** The GitHub CLI: a system install, started and then polled until it ends. */
export function useGhInstall(onInstalled: () => void) {
	const { t } = useTranslation();
	const [job, setJob] = useState<InstallJob | undefined>();
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const timer = useRef<number | undefined>(undefined);
	// Set from the click, so a second click before the next render can't start another install.
	const starting = useRef(false);
	const onInstalledRef = useRef(onInstalled);
	onInstalledRef.current = onInstalled;
	useEffect(() => () => window.clearInterval(timer.current), []);
	// Ask first whether it can be installed here at all: where it can't (Linux
	// needs an administrator password, which AO can't ask for) the row shows
	// the command to run instead of an Install that is bound to fail.
	useEffect(() => {
		let current = true;
		void apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target: "gh" } } })
			.then(({ data }) => {
				if (current && data?.status === "unsupported") setJob((existing) => existing ?? data);
			})
			.catch(() => undefined);
		return () => { current = false; };
	}, []);

	const settle = useCallback((next: InstallJob) => {
		setJob(next);
		if (isActiveInstallJob(next)) return false;
		window.clearInterval(timer.current);
		if (next.status === "succeeded") onInstalledRef.current();
		return true;
	}, []);

	const start = async () => {
		if (starting.current || isActiveInstallJob(job)) return;
		starting.current = true;
		setPending(true);
		setError(undefined);
		try {
			const { data, error: startError } = await apiClient.POST("/api/v1/system/install/{target}", { params: { path: { target: "gh" } } });
			if (startError || !data) {
				setError(apiErrorMessage(startError, t("settings.harness.startFailed")));
				return;
			}
			if (settle(data)) return;
			window.clearInterval(timer.current);
			let misses = 0;
			const missed = () => {
				// A blip is tried again on the next tick; a daemon gone for good ends the wait.
				if (++misses < POLL_GIVE_UP_AFTER) return;
				window.clearInterval(timer.current);
				setJob(undefined);
				setError(t("firstRun.installDidNotFinish"));
			};
			timer.current = window.setInterval(() => {
				void apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target: "gh" } } })
					.then(({ data: next }) => {
						if (!next) return missed();
						misses = 0;
						settle(next);
					})
					.catch(missed);
			}, POLL_MS);
		} catch (startError) {
			setError(startError instanceof Error ? startError.message : t("settings.harness.startFailed"));
		} finally {
			starting.current = false;
			setPending(false);
		}
	};

	const busy = pending || isActiveInstallJob(job);
	// A retry in flight hides the last failure until the daemon answers.
	const ended = !busy && Boolean(job && ENDED.has(job.status));
	const view: InstallView = !job && !error && !pending ? IDLE : {
		busy,
		job,
		ended: ended || (!busy && Boolean(error)),
		problem: busy ? undefined : error ?? (ended ? job?.error || t("firstRun.installDidNotFinish") : undefined),
	};
	return { start, view };
}

/** Under a row while its install runs: a bar that fills to the installer's
 * reported percentage (or sweeps when it states none) and its latest line,
 * else the command it runs. */
export function InstallProgress({ job, label }: { job?: InstallJob; label: string }) {
	const { t } = useTranslation();
	const percent = installPercent(job);
	const line = job?.progress?.detail
		|| (job?.command ? t("startup.installingCommand", { command: job.command }) : t("startup.installingEllipsis"));
	return (
		<div className="flex flex-col gap-1.5" role="status">
			{percent !== undefined ? (
				<div className="ao-install-progress" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
					<div className="ao-install-progress__bar ao-install-progress__bar--determinate" style={{ width: `${percent}%` }} />
				</div>
			) : (
				<div className="ao-install-progress" role="progressbar" aria-label={label}>
					<div className="ao-install-progress__bar" />
				</div>
			)}
			<p className="truncate font-mono text-xs text-muted-foreground" title={line}>{line}</p>
		</div>
	);
}

/** Under a row whose install didn't finish: why, the command to run by hand
 * when there is one, where to read more, and (for a harness) its full set of
 * installers in Settings. */
export function InstallProblem({ view, docsUrl, docsLabel, otherWays }: {
	view: InstallView;
	docsUrl?: string;
	docsLabel?: string;
	/** Opens the harness in Settings > Harness, with every install method and the failed job's diagnostics. */
	otherWays?: () => void;
}) {
	const { t } = useTranslation();
	const command = view.job?.status === "unsupported" || view.job?.status === "failed" ? view.job.command : undefined;
	return (
		<div className="flex flex-col gap-1.5">
			{view.problem ? <p className="text-xs text-destructive" role="alert">{view.problem}</p> : null}
			{command ? <ManualCommand command={command} t={t} /> : null}
			<div className="flex flex-wrap gap-1">
				{otherWays ? (
					<Button type="button" size="sm" variant="ghost" className="text-muted-foreground" onClick={otherWays}>
						{t("firstRun.installOtherWays")}
						<ArrowRight aria-hidden="true" />
					</Button>
				) : null}
				{docsUrl ? <InstallDocsLink url={docsUrl} label={docsLabel ?? t("firstRun.installHowTo")} /> : null}
			</div>
		</div>
	);
}

/** Opens an install guide in the browser. */
export function InstallDocsLink({ url, label }: { url: string; label: string }) {
	return (
		<Button type="button" size="sm" variant="ghost" className="self-start text-muted-foreground" onClick={() => void aoBridge.app.openExternal(url)}>
			{label}
			<ArrowUpRight aria-hidden="true" />
		</Button>
	);
}
