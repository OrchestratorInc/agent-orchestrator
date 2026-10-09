import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudSessionsQueryKey } from "../hooks/useWorkspaceQuery";
import { CloudCpError } from "../lib/cloud-cp";
import type { CloudStartupProblem } from "../lib/cloud-lifecycle";
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
}: {
	orgId: string;
	sessionId: string;
	problem: Exclude<CloudStartupProblem, { phase: "retrying" }>;
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
					{failed ? t("cloud.startupError.title") : t("cloud.startupError.unavailableTitle")}
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
			</div>
		</div>
	);
}
