import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { versionLabel, type HarnessInUse } from "../../../hooks/useHarnessUpdates";
import { workspaceQueryKeyForHost } from "../../../hooks/useWorkspaceQuery";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { useNavigateToSession } from "../../../lib/navigate-to-session";
import type { WorkspaceSummary } from "../../../types/workspace";
import { useUiStore } from "../../../stores/ui-store";
import { ConfirmDialog } from "../../ConfirmDialog";

export type InUseUpdate = { agentId: AgentId; inUse: HarnessInUse; latestVersion?: string };

/**
 * Asks before updating harnesses that open sessions still run. A safe update
 * keeps the running release; a warn update may disturb it, so its wording says so.
 */
export function UpdateWhileRunningDialog({ updates, busy, onConfirm, onOpenChange }: {
	updates: InUseUpdate[];
	busy?: boolean;
	onConfirm: () => void;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const single = updates.length === 1 ? updates[0] : undefined;
	const describe = ({ agentId, inUse, latestVersion }: InUseUpdate) => inUse.level === "warn"
		? t("settings.harness.inUseWarn", { count: inUse.count, agent: agentLabel(agentId) })
		: t("settings.harness.inUseSafe", { count: inUse.count, version: latestVersion ? versionLabel(latestVersion) : t("settings.harness.newVersion") });
	return <ConfirmDialog
		open={updates.length > 0}
		title={single ? t("settings.harness.inUseConfirmTitle", { agent: agentLabel(single.agentId) }) : t("settings.harness.updateAllConfirmTitle")}
		description={single ? describe(single) : t("settings.harness.updateAllConfirmDescription")}
		confirmLabel={t("settings.harness.inUseConfirm")}
		busy={busy}
		onConfirm={onConfirm}
		onOpenChange={onOpenChange}
	>
		{single ? null : <ul className="space-y-1.5 text-control text-settings-muted">
			{updates.map(({ agentId, inUse }) => <li key={agentId} className="flex items-baseline justify-between gap-3">
				<span className="font-medium text-settings-label">{agentLabel(agentId)}</span>
				<span className={inUse.level === "warn" ? "text-warning" : undefined}>{t(inUse.level === "warn" ? "settings.harness.inUseRowWarn" : "settings.harness.inUseRowSafe", { count: inUse.count })}</span>
			</li>)}
		</ul>}
	</ConfirmDialog>;
}

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
		description={inUse?.starting ? t("settings.harness.inUseStarting", { agent }) : t("settings.harness.inUseBlocked", { count: inUse?.count ?? 0, agent })}
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
