import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronRight, Copy, Loader2, RotateCcw } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { CloudCpError } from "../lib/cloud-cp";
import { isWorkspaceStartupError, type CloudStartupProblem } from "../lib/cloud-lifecycle";
import { CoderWorkspaceLinkButton } from "./CoderWorkspaceLinkButton";
import { Button } from "./ui/button";

/**
 * Replaces a cloud session's pane once its runtime has ended without a worker:
 * either AO gave up starting it (a server-provided reason plus Retry) or the
 * runtime stopped with no recorded startup reason. Never leaves the pane blank.
 */
export function CloudSessionStartupError({
	orgId,
	sessionId,
	problem,
	coderWorkspaceUrl,
}: {
	orgId: string;
	sessionId: string;
	problem: Exclude<CloudStartupProblem, { phase: "retrying" }>;
	/** Bring-your-own Coder workspace page, so the user can inspect it. */
	coderWorkspaceUrl?: string;
}) {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	// The worker could not prepare the workspace (checkout, working directory or
	// the project's Setup). It is already running, so a retry asks it to prepare
	// the workspace again instead of re-running the worker bootstrap.
	const workspaceProblem = problem.phase === "failed" && isWorkspaceStartupError(problem.code);
	const failureAt = problem.phase === "failed" ? problem.at : undefined;
	// A workspace retry only queues the request: the worker reports again once
	// the checkout and Setup finish, which can take minutes. Until a newer
	// failure arrives (or the session starts and this screen goes away), show
	// that it is running rather than the old failure with Retry enabled.
	const [retriedFailureAt, setRetriedFailureAt] = useState<string | undefined>(undefined);
	const rerunning = workspaceProblem && retriedFailureAt !== undefined && retriedFailureAt === failureAt;
	const retry = useMutation({
		mutationFn: async (): Promise<void> => {
			if (workspaceProblem) await client.requestWorkspaceCheckout(orgId, sessionId);
			else await client.retrySessionStartup(orgId, sessionId);
		},
		onSuccess: async () => {
			if (workspaceProblem && failureAt !== undefined) setRetriedFailureAt(failureAt);
			await Promise.allSettled([
				queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["cloud-session"] }),
			]);
		},
	});
	const failed = problem.phase === "failed";
	const failureDetail = problem.phase === "failed" ? problem.detail : undefined;
	// The workspace's ready check never passed: the server message names the
	// check, and the details carry its last output.
	const setupNotReady = problem.phase === "failed" && problem.code === "workspace_setup_not_ready";
	const projectSetupFailed = problem.phase === "failed" && problem.code === "project_setup_failed";
	const title = rerunning
		? t("cloud.startupError.workspaceRerunningTitle")
		: setupNotReady
		? t("cloud.startupError.workspaceSetupNotReadyTitle")
		: projectSetupFailed
			? t("cloud.startupError.projectSetupFailedTitle")
			: problem.phase === "failed" && problem.code === "repository_checkout_failed"
				? t("cloud.startupError.repositoryCheckoutFailedTitle")
				: problem.phase === "failed" && problem.code === "working_directory_unusable"
					? t("cloud.startupError.workingDirectoryUnusableTitle")
					: failed ? t("cloud.startupError.title") : t("cloud.startupError.unavailableTitle");
	// A failed Setup step's output is what the user needs to fix the script, so
	// it starts expanded.
	const [showDetail, setShowDetail] = useState(projectSetupFailed);
	const [copied, setCopied] = useState(false);
	const copyDetail = () => {
		if (problem.phase !== "failed") return;
		const text = [problem.code, problem.message, failureDetail].filter(Boolean).join("\n\n");
		void navigator.clipboard?.writeText(text).then(() => setCopied(true), () => undefined);
	};
	// The previous failure's output is stale while the workspace is prepared again.
	const detail = rerunning ? undefined : failureDetail;
	const retryError = retry.isError
		? retry.error instanceof CloudCpError && retry.error.code === "startup_retry_unavailable"
			? t("cloud.startupError.retryUnavailable")
			: t("cloud.startupError.retryFailed", { message: retry.error instanceof Error ? retry.error.message : String(retry.error) })
		: undefined;
	return (
		<div
			className="absolute inset-0 z-chrome grid place-items-center bg-background p-4"
			data-testid="cloud-session-startup-error"
		>
			<div className="flex w-96 max-w-full flex-col items-center gap-3 text-center" role="alert">
				<h2 className="text-heading-sm font-semibold text-foreground">
					{title}
				</h2>
				<p className="text-sm leading-relaxed text-muted-foreground">
					{rerunning ? t("cloud.startupError.workspaceRerunning") : problem.message ?? t("cloud.startupError.unavailableDescription")}
				</p>
				{failed ? (
					<Button
						type="button"
						variant="outline"
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
									<Button type="button" variant="ghost" size="sm" onClick={() => setShowDetail((open) => !open)}>
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
		</div>
	);
}
