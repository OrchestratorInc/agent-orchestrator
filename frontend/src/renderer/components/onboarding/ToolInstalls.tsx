import { type QueryClient, useQuery, useQueryClient } from "@tanstack/react-query";
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
import { probeAgentAuth } from "../../hooks/useAgentAuth";
import { invalidateAgentModelCatalogs } from "../../hooks/useAgentModelsQuery";
import type { AgentReadinessSnapshot } from "../../hooks/useAgentReadinessQuery";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { ManualCommand } from "../InstallDependencyDialog";
import { installPercent } from "../settings/harness/describe-harness";
import { closeAuthTerminal, type AuthTerminalWorkflow } from "../settings/harness/HarnessAuthTerminalPanel";
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

/** How long a harness login may stay open, as in Settings > Harness. */
export const LOGIN_LIFETIME_MS = 15 * 60_000;

/**
 * Closes a harness login's terminal. When the close fails the workflow is
 * kept, as Settings > Harness does, marked cleanup_failed so its panel offers
 * Retry, rather than dropped while the daemon still holds the login (which
 * would refuse the next Sign in). Returns whether it closed.
 */
export async function closeLoginWorkflow(
	workflow: AuthTerminalWorkflow,
	setWorkflow: (next: AuthTerminalWorkflow | null) => void,
	failedReason: string,
): Promise<boolean> {
	try {
		await closeAuthTerminal(workflow.terminal.handleId);
		setWorkflow(null);
		return true;
	} catch (error) {
		setWorkflow({ ...workflow, phase: "cleanup_failed", reason: error instanceof Error ? error.message : failedReason });
		return false;
	}
}

/**
 * Ends a harness login left running for LOGIN_LIFETIME_MS, as Settings >
 * Harness does: its terminal closes and the panel says it timed out (Retry
 * starts a new login), or, if the close fails, offers Retry for the cleanup.
 * `onTimedOut` runs after a clean close (to verify sign-in once more).
 */
export function useLoginLifetime(
	workflow: AuthTerminalWorkflow | null,
	setWorkflow: (next: AuthTerminalWorkflow | null) => void,
	messages: { timedOut: string; failed: string },
	onTimedOut: (agentId: string) => void,
) {
	const current = useRef(workflow);
	current.current = workflow;
	const latest = useRef({ setWorkflow, messages, onTimedOut });
	latest.current = { setWorkflow, messages, onTimedOut };
	const running = workflow?.phase === "running" ? workflow : null;
	useEffect(() => {
		if (!running) return;
		const handleId = running.terminal.handleId;
		const still = () => current.current?.terminal.handleId === handleId;
		const timer = window.setTimeout(async () => {
			if (!still()) return;
			const { setWorkflow: set, messages: text, onTimedOut: timedOut } = latest.current;
			set({ ...running, phase: "closing", reason: undefined });
			try {
				await closeAuthTerminal(handleId);
				if (still()) set({ ...running, phase: "timed_out", reason: text.timedOut });
				timedOut(running.agentId);
			} catch (error) {
				if (still()) set({ ...running, phase: "cleanup_failed", reason: error instanceof Error ? error.message : text.failed });
			}
		}, Math.max(0, LOGIN_LIFETIME_MS - (Date.now() - running.startedAt)));
		return () => window.clearTimeout(timer);
	}, [running]);
}

/** How often an open login re-verifies sign-in (each check waits for the last). */
export const LOGIN_VERIFY_MS = 5_000;

/**
 * While a harness login is open, verifies its sign-in afresh every
 * LOGIN_VERIFY_MS until it is done. Some logins finish inside a terminal that
 * stays open (a TUI's in-session login command), so the terminal ending can't
 * be the only signal, and readiness alone would reuse a signed-out answer for
 * minutes. Each check waits for the one before it, so probes never pile up.
 */
export function useVerifyWhileSigningIn(agentId: string | undefined, done: boolean, verify: (agentIds: string[]) => Promise<void>) {
	const verifyRef = useRef(verify);
	verifyRef.current = verify;
	useEffect(() => {
		if (!agentId || done) return;
		let active = true;
		let timer: number | undefined;
		const next = () => {
			timer = window.setTimeout(async () => {
				await verifyRef.current([agentId]).catch(() => undefined);
				if (active) next();
			}, LOGIN_VERIFY_MS);
		};
		next();
		return () => {
			active = false;
			window.clearTimeout(timer);
		};
	}, [agentId, done]);
}

/**
 * A harness login started on Your tools, kept by the onboarding flow rather
 * than the step: going Back and returning reattaches the same terminal (the
 * daemon allows one login at a time, so a lost handle would block the next
 * Sign in). When the flow ends its terminal closes, and a login that comes back
 * after that has its new terminal closed rather than stranded.
 */
export function useOnboardingLogin() {
	const [workflow, setWorkflowState] = useState<AuthTerminalWorkflow | null>(null);
	const current = useRef<AuthTerminalWorkflow | null>(null);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => {
			mounted.current = false;
			const open = current.current;
			current.current = null;
			if (open) void closeAuthTerminal(open.terminal.handleId).catch(() => undefined);
		};
	}, []);
	const setWorkflow = useCallback((next: AuthTerminalWorkflow | null) => {
		if (!mounted.current) {
			if (next) void closeAuthTerminal(next.terminal.handleId).catch(() => undefined);
			return;
		}
		current.current = next;
		setWorkflowState(next);
	}, []);
	return [workflow, setWorkflow] as const;
}

/**
 * Readiness reuses sign-in observations for a while, so a login that just
 * finished can still read as signed out. Where the answer must be current (a
 * login's terminal ended, or the person asked to check again) this makes the
 * daemon verify each harness's sign-in first, as Settings > Harness does, then
 * reads readiness again through `refresh` (which caches it).
 */
export async function verifyAgentSignIn(queryClient: QueryClient, agentIds: string[], refresh: (agentIds: string[]) => Promise<void>) {
	await Promise.all(agentIds.map(async (agentId) => {
		try {
			await probeAgentAuth(agentId);
			// The probe also refreshes the agent's model catalogs; drop stale copies.
			void invalidateAgentModelCatalogs(queryClient, agentId);
		} catch {
			// The read below still reports what the daemon knows.
		}
	}));
	await refresh(agentIds);
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
export function InstallProgress({ job, label, compact = false }: {
	job?: InstallJob;
	label: string;
	/** Just a short bar, for a row's trailing spot; the command and the
	 * installer's latest line show on hover. */
	compact?: boolean;
}) {
	const { t } = useTranslation();
	const percent = installPercent(job);
	const command = job?.command ? t("startup.installingCommand", { command: job.command }) : undefined;
	const line = job?.progress?.detail || command || t("startup.installingEllipsis");
	const bar = percent !== undefined ? (
		<div className="ao-install-progress" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
			<div className="ao-install-progress__bar ao-install-progress__bar--determinate" style={{ width: `${percent}%` }} />
		</div>
	) : (
		<div className="ao-install-progress" role="progressbar" aria-label={label}>
			<div className="ao-install-progress__bar" />
		</div>
	);
	if (compact) {
		// The row keeps to a short bar, but what is running on this machine
		// (brew, sudo, npm…) stays a hover away, in full, and is read out to
		// screen readers. A native title, since the row re-renders as progress
		// comes in.
		const shown = [command, job?.progress?.detail].filter((text): text is string => Boolean(text)).join("\n") || t("startup.installingEllipsis");
		return (
			<div className="w-24 py-1.5" role="status" title={shown}>
				{bar}
				<span className="sr-only">{shown}</span>
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-1.5" role="status">
			{bar}
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
	const [showOutput, setShowOutput] = useState(false);
	const command = view.job?.status === "unsupported" || view.job?.status === "failed" ? view.job.command : undefined;
	// The error alone can be just "exit status 1"; the installer's own output
	// usually says why (and how to recover), so it can be opened here.
	const output = view.job?.status === "failed" ? view.job.output?.trim() : undefined;
	return (
		<div className="flex flex-col gap-1.5">
			{view.problem ? <p className="text-xs text-destructive" role="alert">{view.problem}</p> : null}
			{command ? <ManualCommand command={command} t={t} /> : null}
			{output && showOutput ? (
				<pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted px-2.5 py-2 font-mono text-xs leading-relaxed text-muted-foreground">
					{output}
				</pre>
			) : null}
			<div className="flex flex-wrap gap-1">
				{output ? (
					<Button type="button" size="sm" variant="ghost" className="text-muted-foreground" aria-expanded={showOutput} onClick={() => setShowOutput((shown) => !shown)}>
						{showOutput ? t("firstRun.installHideOutput") : t("firstRun.installShowOutput")}
					</Button>
				) : null}
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
