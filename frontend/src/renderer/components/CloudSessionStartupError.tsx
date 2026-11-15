import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronRight, Copy, Loader2, RotateCcw } from "@/components/app-icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../i18n";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudScriptRuns } from "../hooks/useCloudScriptRuns";
import { cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { CloudCpError, type CloudCpClient } from "../lib/cloud-cp";
import { isWorkspaceStartupError, type CloudStartupProblem } from "../lib/cloud-lifecycle";
import { latestScriptRun } from "../lib/cloud-script-runs";
import { cn } from "../lib/utils";
import { CoderWorkspaceLinkButton } from "./CoderWorkspaceLinkButton";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { Button } from "./ui/button";

const RETRY_PICKUP_LIMIT_MS = 5 * 60_000;

const WAKE_RETRY_LIMIT_MS = 3 * 60_000;
const WAKE_RETRY_INTERVAL_MS = 1_000;

export async function requestWorkspaceRetry(
	client: Pick<CloudCpClient, "requestWorkspaceCheckout">,
	orgId: string,
	sessionId: string,
	signal: AbortSignal,
): Promise<void> {
	const deadline = Date.now() + WAKE_RETRY_LIMIT_MS;
	for (;;) {
		try {
			await client.requestWorkspaceCheckout(orgId, sessionId);
			return;
		} catch (error) {
			if (!(error instanceof CloudCpError) || error.code !== "WORKER_UNAVAILABLE" || signal.aborted || Date.now() >= deadline) throw error;
		}
		await new Promise((resolve) => window.setTimeout(resolve, WAKE_RETRY_INTERVAL_MS));
		if (signal.aborted) throw new DOMException("The retry was cancelled.", "AbortError");
	}
}

function useRetryStalled(waiting: boolean, retry: object | undefined): boolean {
	const [stalled, setStalled] = useState<object>();
	useEffect(() => {
		if (!waiting || !retry) return;
		const timer = window.setTimeout(() => setStalled(retry), RETRY_PICKUP_LIMIT_MS);
		return () => window.clearTimeout(timer);
	}, [retry, waiting]);
	return stalled !== undefined && stalled === retry;
}

const FAILED_STARTUP_TITLES: Partial<Record<string, MessageKey>> = {
	workspace_setup_not_ready: "cloud.startupError.workspaceSetupNotReadyTitle",
	project_setup_failed: "cloud.startupError.projectSetupFailedTitle",
	repository_checkout_failed: "cloud.startupError.repositoryCheckoutFailedTitle",
	working_directory_unusable: "cloud.startupError.workingDirectoryUnusableTitle",
	working_directory_occupied: "cloud.startupError.workingDirectoryOccupiedTitle",
	working_directory_not_durable: "cloud.startupError.workingDirectoryNotDurableTitle",
	sandbox_unresponsive: "cloud.startupError.sandboxUnresponsiveTitle",
};

export function CloudSessionStartupError({
	orgId,
	sessionId,
	problem,
	coderWorkspaceUrl,
	ended = false,
}: {
	orgId: string;
	sessionId: string;
	problem: Exclude<CloudStartupProblem, { phase: "retrying" }>;
	coderWorkspaceUrl?: string;
	ended?: boolean;
}) {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const workspaceProblem = problem.phase === "failed" && isWorkspaceStartupError(problem.code);
	const failureAt = problem.phase === "failed" ? problem.at : undefined;
	const [retried, setRetried] = useState<{ failureAt: string; afterSequence: number }>();
	const awaitingRetry = workspaceProblem && retried !== undefined && retried.failureAt === failureAt;
	const scriptRuns = useCloudScriptRuns(orgId, sessionId, { live: awaitingRetry });
	const latestRun = latestScriptRun(scriptRuns, "setup");
	const retryRun = awaitingRetry && latestRun && latestRun.firstSequence > retried.afterSequence ? latestRun : undefined;
	const retryStalled = useRetryStalled(awaitingRetry && !retryRun, retried);
	const rerunning = awaitingRetry
		&& !(retryRun && (retryRun.status === "failed" || retryRun.status === "unknown" || retryRun.status === "interrupted"))
		&& !retryStalled;
	const retryAbort = useRef<AbortController | undefined>(undefined);
	useEffect(() => () => retryAbort.current?.abort(), []);
	const retry = useMutation({
		mutationFn: async (): Promise<number> => {
			const afterSequence = Math.max(0, ...scriptRuns.map((run) => run.lastSequence));
			retryAbort.current?.abort();
			const abort = new AbortController();
			retryAbort.current = abort;
			if (workspaceProblem) await requestWorkspaceRetry(client, orgId, sessionId, abort.signal);
			else await client.retrySessionStartup(orgId, sessionId);
			return afterSequence;
		},
		onSuccess: async (afterSequence) => {
			if (workspaceProblem && failureAt !== undefined) setRetried({ failureAt, afterSequence });
			await Promise.allSettled([
				queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["cloud-session"] }),
			]);
		},
	});
	const failed = problem.phase === "failed";
	const failureDetail = problem.phase === "failed" ? problem.detail : undefined;
	const projectSetupFailed = problem.phase === "failed" && problem.code === "project_setup_failed";
	const agentStartFailed = problem.phase === "failed" && problem.code === "agent_start_failed";
	const title = rerunning
		? t(agentStartFailed ? "cloud.startupError.agentRestartingTitle" : "cloud.startupError.workspaceRerunningTitle")
		: failed
			? t(FAILED_STARTUP_TITLES[problem.code ?? ""] ?? "cloud.startupError.title")
			: t("cloud.startupError.unavailableTitle");
	const latestSetup = projectSetupFailed || rerunning ? latestRun : undefined;
	const setupRun = latestSetup && (latestSetup.status === "failed" || (rerunning && latestSetup.status === "running")) ? latestSetup : undefined;
	const [detailToggled, setShowDetail] = useState<boolean>();
	const showDetail = detailToggled ?? (projectSetupFailed && !setupRun);
	const [copied, setCopied] = useState(false);
	const copiedFor = `${sessionId}\n${problem.phase === "failed" ? problem.message : ""}`;
	useEffect(() => setCopied(false), [copiedFor]);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 2000);
		return () => window.clearTimeout(timer);
	}, [copied]);
	const copyDetail = () => {
		if (problem.phase !== "failed") return;
		const text = [problem.code, problem.message, failureDetail].filter(Boolean).join("\n\n");
		void navigator.clipboard?.writeText(text).then(() => setCopied(true), () => undefined);
	};
	const detail = rerunning ? undefined : failureDetail;
	const retryError = retry.isError
		? retry.error instanceof CloudCpError && retry.error.code === "startup_retry_unavailable"
			? t("cloud.startupError.retryUnavailable")
			: t("cloud.startupError.retryFailed", { message: retry.error instanceof Error ? retry.error.message : String(retry.error) })
		: undefined;
	return (
		<div
			className={cn("absolute inset-0 z-chrome grid place-items-center bg-background p-4", setupRun && "overflow-y-auto")}
			data-testid="cloud-session-startup-error"
		>
			<div className={cn("flex max-w-full flex-col items-center gap-4", setupRun ? "w-[36rem]" : "w-96")}>
				<div className="flex w-full flex-col items-center gap-3 text-center" role="alert">
					<h2 className="text-heading-sm font-semibold text-foreground">
						{title}
					</h2>
					<p className="text-sm leading-relaxed text-muted-foreground">
						{rerunning ? t(agentStartFailed ? "cloud.startupError.agentRestarting" : "cloud.startupError.workspaceRerunning") : problem.message ?? t("cloud.startupError.unavailableDescription")}
					</p>
					{failed && ended ? (
						<p className="text-xs text-muted-foreground">{t("cloud.startupError.restoreToRetry")}</p>
					) : failed ? (
						<Button
							type="button"
							variant="secondary"
							className="mt-1"
							disabled={retry.isPending || rerunning}
							onClick={() => retry.mutate()}
						>
							{retry.isPending || rerunning
								? <Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
								: <RotateCcw aria-hidden="true" className="size-3.5" />}
							{retry.isPending || rerunning ? t("cloud.startupError.retrying") : t("cloud.startupError.retry")}
						</Button>
					) : null}
					{retryError ? <p className="text-xs text-error">{retryError}</p> : null}
					{detail || coderWorkspaceUrl ? (
						<div className="flex w-full flex-col items-center gap-2">
							<div className="flex items-center gap-1">
								{detail ? (
									<>
										<Button type="button" variant="ghost" size="sm" onClick={() => setShowDetail(!showDetail)}>
											{showDetail
												? <ChevronDown aria-hidden="true" className="size-3.5" />
												: <ChevronRight aria-hidden="true" className="size-3.5" />}
											{showDetail ? t("cloud.startupError.hideDetails") : t("cloud.startupError.showDetails")}
										</Button>
										<Button type="button" variant="ghost" size="sm" onClick={copyDetail}>
											{copied ? <Check aria-hidden="true" className="size-3.5" /> : <Copy aria-hidden="true" className="size-3.5" />}
											{copied ? t("cloud.startupError.copied") : t("cloud.startupError.copyDetails")}
										</Button>
									</>
								) : null}
								<CoderWorkspaceLinkButton url={coderWorkspaceUrl} />
							</div>
							{detail && showDetail ? (
								<pre
									className="max-h-48 w-full overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-2 text-left font-mono text-xs text-muted-foreground select-text"
									data-testid="cloud-session-startup-detail"
								>
									{detail}
								</pre>
							) : null}
						</div>
					) : null}
				</div>
				{setupRun ? <ScriptRunPanel className="w-full" run={setupRun} /> : null}
			</div>
		</div>
	);
}
