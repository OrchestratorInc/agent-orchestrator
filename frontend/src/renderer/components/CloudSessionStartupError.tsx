import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronRight, Copy, Loader2, RotateCcw } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { CloudCpError } from "../lib/cloud-cp";
import type { CloudStartupProblem } from "../lib/cloud-lifecycle";
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
	const retry = useMutation({
		mutationFn: () => client.retrySessionStartup(orgId, sessionId),
		onSuccess: async () => {
			await Promise.allSettled([
				queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["cloud-session"] }),
			]);
		},
	});
	const failed = problem.phase === "failed";
	const detail = problem.phase === "failed" ? problem.detail : undefined;
	// The workspace's ready check never passed: the server message names the
	// check, and the details carry its last output.
	const setupNotReady = problem.phase === "failed" && problem.code === "workspace_setup_not_ready";
	const title = setupNotReady
		? t("cloud.startupError.workspaceSetupNotReadyTitle")
		: failed ? t("cloud.startupError.title") : t("cloud.startupError.unavailableTitle");
	const [showDetail, setShowDetail] = useState(false);
	const [copied, setCopied] = useState(false);
	const copyDetail = () => {
		if (problem.phase !== "failed") return;
		const text = [problem.code, problem.message, detail].filter(Boolean).join("\n\n");
		void navigator.clipboard?.writeText(text).then(() => setCopied(true), () => undefined);
	};
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
					{problem.message ?? t("cloud.startupError.unavailableDescription")}
				</p>
				{failed ? (
					<Button
						type="button"
						variant="outline"
						className="mt-1"
						disabled={retry.isPending}
						onClick={() => retry.mutate()}
					>
						{retry.isPending
							? <Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
							: <RotateCcw aria-hidden="true" className="size-3.5" />}
						{retry.isPending ? t("cloud.startupError.retrying") : t("cloud.startupError.retry")}
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
