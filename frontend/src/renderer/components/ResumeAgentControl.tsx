import { useIsMutating, useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Play } from "lucide-react";
import { aoBridge } from "../lib/bridge";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCanResumeAgent } from "../hooks/useCanResumeAgent";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
import { sessionAgentExited, type WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

/**
 * Relaunches an agent that exited inside a still-live session. Distinct from
 * restore, which revives a TERMINATED row: this keeps the worktree, terminal
 * identity, and native conversation. Self-gating, so the inspector Summary and
 * the terminal strip can both mount it unconditionally.
 */
export function ResumeAgentControl({
	className,
	containerClassName,
	session,
	hostId,
}: {
	className?: string;
	containerClassName?: string;
	session: WorkspaceSession;
	hostId?: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const canResume = useCanResumeAgent(session, hostId);
	// A Cloud session's exited agent starts again in its live worker, through
	// the control plane rather than this machine's daemon.
	const canResumeCloud = Boolean(session.cloud) && sessionAgentExited(session);
	const { client: cloudClient } = useCloudCp(Boolean(session.cloud));
	const resuming = useIsMutating({ mutationKey: ["resume-agent", hostId ?? "local", session.id] }) > 0;
	const resume = useMutation({
		mutationKey: ["resume-agent", hostId ?? "local", session.id],
		mutationFn: async () => {
			if (usePreviewData) return;
			if (session.cloud) {
				const restarted = await cloudClient.restartSessionAgent(session.cloud.orgId, session.id);
				if (!restarted.requested) throw new Error(t("inspector.resumeAgentAlreadyRunning"));
				return undefined;
			}
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/resume-agent", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, `Failed to resume agent (${response.status})`));
			return data;
		},
		onSuccess: async (data) => {
			await queryClient.invalidateQueries({ queryKey: session.cloud ? cloudSessionsQueryKey : workspaceQueryKeyForHost(hostId) });
			if (data?.resumeMode === "saved_prompt") {
				void aoBridge.notifications
					.show({
						id: `resume-agent-fallback:${hostId ?? "local"}:${session.id}:${Date.now()}`,
						title: t("inspector.startedFromPrompt"),
						body: t("inspector.resumeFallbackBody"),
					})
					.catch((err) => {
						console.warn("Unable to show resume fallback notification", err);
					});
			}
		},
	});

	// A deleted Cloud session re-provisions through the control plane
	// (useRestoreSession); one whose agent exited restarts it there, never
	// through this machine's daemon, which has never heard of it.
	if (!canResume && !canResumeCloud) return null;

	const error = resume.error instanceof Error ? resume.error.message : null;
	const control = (
		<>
			<Button
				className={cn("shrink-0", className)}
				disabled={resuming}
				onClick={() => resume.mutate()}
				size="sm"
				type="button"
				variant="outline"
			>
				<Play className="size-icon-sm" aria-hidden="true" />
				{t("inspector.resumeAgent")}
			</Button>
			{error ? (
				<p className="mt-2 text-xs leading-normal text-error" role="status">
					{error}
				</p>
			) : null}
		</>
	);
	return containerClassName ? <div className={containerClassName}>{control}</div> : control;
}
