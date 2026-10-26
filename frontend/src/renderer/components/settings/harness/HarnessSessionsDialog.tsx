import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cancelHarnessUpdate, IDLE_WAIT_DETAIL_MS, installJobsQueryKeyForHost, upsertInstallJob, type HarnessInUse, type InstallJob } from "../../../hooks/useHarnessUpdates";
import { Button } from "../../ui/button";
import { workspaceQueryKeyForHost } from "../../../hooks/useWorkspaceQuery";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { useNavigateToSession } from "../../../lib/navigate-to-session";
import type { WorkspaceSummary } from "../../../types/workspace";
import { useUiStore } from "../../../stores/ui-store";
import { ConfirmDialog } from "../../ConfirmDialog";

/** Explains why an update must wait and lists the sessions it waits for, each one link away. */
export function HarnessSessionsDialog({ agentId, inUse, hostId, onOpenChange }: {
	agentId: AgentId | null;
	inUse?: HarnessInUse;
	hostId?: string;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const closeSettings = useUiStore((state) => state.closeSettings);
	const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) ?? [];
	const ids = new Set(inUse?.sessionIds ?? []);
	const sessions = workspaces.flatMap((workspace) => workspace.sessions
		.filter((session) => ids.has(session.id))
		.map((session) => ({ session, projectId: workspace.id, projectName: workspace.name })));
	const agent = agentId ? agentLabel(agentId) : "";
	const open = (projectId: string, sessionId: string) => {
		onOpenChange(false);
		closeSettings();
		navigateToSession(projectId, sessionId, hostId);
	};
	return <ConfirmDialog
		open={agentId !== null && inUse !== undefined}
		title={t("settings.harness.sessionsUsingTitle", { agent })}
		description={inUse?.starting ? t("settings.harness.inUseStarting", { agent })
			: inUse?.level === "idle" ? t("settings.harness.waitingSessionsDescription", { agent })
			: t("settings.harness.inUseBlocked", { count: inUse?.count ?? 0, agent })}
		confirmLabel={t("settings.harness.sessionsUsingDone")}
		cancelLabel={t("confirm.cancel")}
		onConfirm={() => onOpenChange(false)}
		onOpenChange={onOpenChange}
	>
		{inUse?.starting ? null : sessions.length === 0
			? <p className="text-control text-settings-muted">{t("settings.harness.sessionsUsingUnavailable")}</p>
			: <ul className="max-h-48 space-y-1 overflow-y-auto">
				{sessions.map(({ session, projectId, projectName }) => <li key={session.id}>
					<button type="button" className="flex w-full items-baseline justify-between gap-3 rounded-md px-2 py-1 text-left text-control hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => open(projectId, session.id)}>
						<span className="min-w-0 truncate font-medium text-settings-label">{session.title || session.id}</span>
						<span className="shrink-0 text-caption text-settings-muted">{projectName}</span>
					</button>
				</li>)}
			</ul>}
	</ConfirmDialog>;
}

const WAIT_REASON_KEYS = {
	working: "settings.harness.waitReason.working",
	command: "settings.harness.waitReason.command",
	approval: "settings.harness.waitReason.approval",
	starting: "settings.harness.waitReason.starting",
	reviewer: "settings.harness.waitReason.reviewer",
	resume: "settings.harness.waitReason.resume",
} as const satisfies Record<NonNullable<InstallJob["waitingOn"]>[number]["reason"], string>;

/**
 * An update queued until the harness's sessions are idle. It reads as routine
 * at first; once it has waited a while it names the session it waits for, so
 * the user can finish or pause that work, or cancel the update.
 */
export function IdleUpdateStatus({ agentId, job, hostId, now = Date.now(), onViewSessions }: {
	agentId: AgentId;
	job: InstallJob;
	hostId?: string;
	now?: number;
	onViewSessions: (inUse: HarnessInUse) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [cancelling, setCancelling] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const waitingOn = job.waitingOn ?? [];
	const waitedLong = Boolean(job.startedAt && now - Date.parse(job.startedAt) >= IDLE_WAIT_DETAIL_MS);
	const first = waitingOn[0];
	const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) ?? [];
	const firstTitle = first ? workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === first.sessionId)?.title || first.sessionId : "";
	const cancel = async () => {
		setCancelling(true);
		setError(null);
		try {
			const next = await cancelHarnessUpdate(agentId, hostId);
			queryClient.setQueryData<InstallJob[]>(installJobsQueryKeyForHost(hostId), (current) => upsertInstallJob(current, next));
		} catch (cancelError) {
			setError(cancelError instanceof Error ? cancelError.message : t("settings.harness.cancelUpdateFailed"));
		} finally {
			setCancelling(false);
		}
	};
	return <div role="status" className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-settings-muted">
		<span className="min-w-0 flex-1 break-words">
			{waitedLong && first
				? t("settings.harness.waitingFor", { session: firstTitle, reason: t(WAIT_REASON_KEYS[first.reason]) })
				: t("settings.harness.waitingForIdle")}
		</span>
		{waitedLong && waitingOn.length > 0 ? <Button type="button" size="sm" variant="ghost" className="h-6 px-2 text-caption"
			onClick={() => onViewSessions({ count: waitingOn.length, sessionIds: waitingOn.map((wait) => wait.sessionId), level: "idle", starting: false })}>
			{t("settings.harness.viewWaitingSessions")}
		</Button> : null}
		<Button type="button" size="sm" variant="outline" className="h-6 px-2 text-caption" disabled={cancelling} onClick={() => void cancel()}>
			{t("settings.harness.cancelUpdate")}
		</Button>
		{error ? <p role="alert" className="w-full text-error">{error}</p> : null}
	</div>;
}
