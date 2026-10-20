import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { apiClient } from "../lib/api-client";
import { formatBytes } from "../lib/format-bytes";
import {
	processInventoryQueryKey,
	useProcessInventoryQuery,
	isStoppableOwnedTree,
	type ProcessKillTarget,
} from "../hooks/useProcessInventoryQuery";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { Badge } from "./ui/badge";
import { ConfirmDialog } from "./ConfirmDialog";
import { MemoryPopover } from "./MemoryPopover";

// Bottom strip of the content area (mounted inside <main>, so it always sits
// right of the fixed sidebar at any width or collapse state), kept minimal:
// host RAM/Swap (the MemoryPopover segment) plus the actionable counts —
// orphaned trees and idle workers — with the batch stop/kill entry point.
// Renders nothing while the inventory is unavailable, and hides entirely when
// the daemon provides neither a host snapshot nor stoppable trees.
export function StatusBar() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [busy, setBusy] = useState(false);
	const [killError, setKillError] = useState<string | null>(null);
	const [stopError, setStopError] = useState<string | null>(null);
	const [pendingKillSessionId, setPendingKillSessionId] = useState<string | undefined>();
	const [pendingStopSessionId, setPendingStopSessionId] = useState<string | undefined>();
	const { data } = useProcessInventoryQuery();

	// AO_NO_PROCINVENTORY (the daemon reports disabled): the feature is off —
	// hide the bar entirely; the shared query stops refetching on its own.
	if (data?.disabled) return null;

	const orphans = (data?.trees ?? []).filter((tree) => tree.state === "orphan");
	const idleOwned = (data?.trees ?? []).filter(isStoppableOwnedTree);
	const stoppableBytes =
		orphans.reduce((sum, tree) => sum + tree.rssBytes, 0) + idleOwned.reduce((sum, tree) => sum + tree.rssBytes, 0);

	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: processInventoryQueryKey });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	};

	const killOrphans = async (targets: ProcessKillTarget[]) => {
		const { error } = await apiClient.POST("/api/v1/system/processes/kill", { body: { targets } });
		if (error) throw new Error("kill failed");
	};

	const stopSessions = async (sessionIds: string[]) => {
		for (const sessionId of sessionIds) {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(`stop ${sessionId} failed`);
		}
	};

	// The bar-level Stop covers everything stoppable in one confirm: true
	// orphans go through the process-group kill, idle owned sessions through
	// the daemon's own session kill (terminate + resumable later).
	const runStopAll = async () => {
		setBusy(true);
		try {
			if (orphans.length > 0) {
				await killOrphans(orphans.map((tree) => toTarget(tree)));
			}
			await stopSessions(idleOwned.map((tree) => tree.sessionId));
			refresh();
			setKillError(null);
			setStopError(null);
			setConfirmOpen(false);
		} catch {
			setStopError(t("statusBar.stopFailed"));
		} finally {
			setBusy(false);
		}
	};

	if (!data || (!data.host && orphans.length === 0 && idleOwned.length === 0)) return null;

	return (
		<>
			<div
				data-testid="status-bar"
				className="flex h-7 shrink-0 items-center gap-3 border-t border-border/60 bg-sidebar px-3 text-caption text-muted-foreground"
			>
				{data.host && (
					<MemoryPopover
						host={data.host}
						trees={data.trees}
						totals={data.totals}
						pendingKillSessionId={pendingKillSessionId}
						pendingStopSessionId={pendingStopSessionId}
						killError={killError}
						stopError={stopError}
						onKillTrees={(targets) => {
							setKillError(null);
							setPendingKillSessionId(targets[0]?.sessionId);
							void killOrphans(targets)
								.then(() => refresh())
								.catch(() => setKillError(t("statusBar.killFailed")))
								.finally(() => setPendingKillSessionId(undefined));
						}}
						onStopSessions={(sessionIds) => {
							setStopError(null);
							setPendingStopSessionId(sessionIds[0]);
							void stopSessions(sessionIds)
								.then(() => refresh())
								.catch(() => setStopError(t("statusBar.stopFailed")))
								.finally(() => setPendingStopSessionId(undefined));
						}}
						onBatchKill={() => {
							setKillError(null);
							setConfirmOpen(true);
						}}
					/>
				)}
				{idleOwned.length > 0 && (
					<Badge variant="warning">
						{t("statusBar.idleBadge")}
						<span className="tabular-nums">{idleOwned.length}</span>
					</Badge>
				)}
				<span>
					{t("statusBar.agentsCount")} <span className="tabular-nums">{data.trees.length}</span>
				</span>
				{orphans.length > 0 && (
					<Badge variant="warning">
						{t("statusBar.orphans")}
						<span className="tabular-nums">{orphans.length}</span>
						<AlertTriangle aria-hidden="true" />
					</Badge>
				)}
				{orphans.length + idleOwned.length > 0 && (
					<button
						type="button"
						data-testid="status-bar-stop"
						className="text-caption text-destructive hover:underline focus-visible:underline disabled:pointer-events-none disabled:opacity-50"
						onClick={() => {
							setKillError(null);
							setStopError(null);
							setConfirmOpen(true);
						}}
					>
						{t("statusBar.stopAll")}
					</button>
				)}
			</div>
			<ConfirmDialog
				open={confirmOpen}
				title={t("statusBar.confirmStopTitle")}
				description={
					<>
						<p>
							{t("statusBar.confirmStopLead", {
								count: orphans.length + idleOwned.length,
								rss: formatBytes(stoppableBytes),
							})}
						</p>
						<ul className="mt-2 space-y-0.5">
							{orphans.map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
								</li>
							))}
							{idleOwned.slice(0, Math.max(0, 8 - orphans.length)).map((tree) => (
								<li key={tree.sessionId} className="tabular-nums">
									{t("statusBar.treeRow", { session: tree.sessionId, pid: tree.rootPid, rss: formatBytes(tree.rssBytes) })}
								</li>
							))}
							{orphans.length + idleOwned.length > 8 && (
								<li>{t("statusBar.moreTrees", { count: orphans.length + idleOwned.length - 8 })}</li>
							)}
						</ul>
					</>
				}
				confirmLabel={t("statusBar.confirmStop")}
				destructive
				busy={busy}
				error={killError ?? stopError}
				onConfirm={() => {
					void runStopAll();
				}}
				onOpenChange={(open) => {
					setConfirmOpen(open);
					if (!open) {
						setKillError(null);
						setStopError(null);
					}
				}}
			/>
		</>
	);
}

function toTarget(tree: { sessionId: string; rootPid: number; rootLstart: string }): ProcessKillTarget {
	return { sessionId: tree.sessionId, rootPid: tree.rootPid, rootLstart: tree.rootLstart };
}
