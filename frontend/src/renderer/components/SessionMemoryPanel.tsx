import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Activity, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { formatTimeTerse } from "../lib/format-time";
import { getAgentActivityView } from "../lib/session-presentation";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useTerminateSession, useTerminateSessionState } from "../hooks/useTerminateSession";
import {
	formatMemory,
	memoryTone,
	useSessionMemory,
	type SessionMemoryReading,
} from "../hooks/useSessionMemory";
import { workerSessions, type WorkspaceSession } from "../types/workspace";
import { SessionTerminationPopover } from "./SessionTerminationPopover";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type MemoryRow = {
	session: WorkspaceSession;
	reading?: SessionMemoryReading;
};

/**
 * Topbar pill showing memory used by every live session, and the btop-style
 * panel behind it. Each row reuses the existing kill confirmation; the panel
 * adds nothing the board cannot already do, it only makes the biggest
 * session visible. The pill reads only the memory query so the shell topbar
 * keeps its identity while sessions stream updates; the panel subscribes to
 * sessions only while open.
 */
export function SessionMemoryButton({
	projectId,
	style,
}: {
	projectId?: string;
	style?: React.CSSProperties;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useSessionMemory(projectId);
	const readings = memory.data ? [...memory.data.values()] : [];
	const total = readings.reduce((sum, reading) => sum + reading.rssBytes, 0);
	if (memory.isError || readings.length === 0) {
		return null;
	}
	const label = t("shell.memoryTotal", { size: formatMemory(total), count: readings.length });
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex" style={style}>
						<TopbarButton
							aria-label={label}
							className="topbar-control--labeled font-mono tabular-nums"
							data-priority="secondary"
							data-testid="session-memory-button"
							onClick={() => setOpen(true)}
							variant="secondary"
						>
							<Activity className="size-icon-md" aria-hidden="true" />
							<span data-compact-label="">{formatMemory(total)}</span>
						</TopbarButton>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{label}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open projectId={projectId} /> : null}
		</>
	);
}

function toRows(sessions: WorkspaceSession[], readings?: Map<string, SessionMemoryReading>): MemoryRow[] {
	return sessions
		.filter((session) => session.isTerminated !== true)
		.map((session) => ({ session, reading: readings?.get(session.id) }))
		.sort((a, b) => (b.reading?.rssBytes ?? -1) - (a.reading?.rssBytes ?? -1));
}

export function SessionMemoryPanel({
	onOpenChange,
	open,
	projectId,
}: {
	onOpenChange: (open: boolean) => void;
	open: boolean;
	projectId?: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaces = useWorkspaceQuery().data ?? [];
	const memory = useSessionMemory(projectId);
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workerSessions(workspace.sessions)),
		[workspaces, projectId],
	);
	const rows = useMemo(() => toRows(sessions, memory.data), [sessions, memory.data]);
	const total = rows.reduce((sum, row) => sum + (row.reading?.rssBytes ?? 0), 0);
	const terminate = useTerminateSession();
	const cleanup = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/sessions/cleanup", {
				params: { query: projectId ? { project: projectId } : {} },
			});
			if (error) throw new Error(apiErrorMessage(error, t("shell.memoryCleanupFailed")));
		},
		onSettled: () => queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
	});
	const largest = rows[0]?.reading?.rssBytes ?? 0;
	const processes = rows.reduce((sum, row) => sum + (row.reading?.processCount ?? 0), 0);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={cn(settingsDialogContentClass, "w-[min(56rem,calc(100vw-var(--space-8)))]")} showCloseButton={false}>
				<div className={cn(settingsDialogHeaderClass, "flex-row items-center gap-3")}>
					<div className="min-w-0 flex-1">
						<DialogTitle className="text-sm font-medium">{t("shell.memoryPanelTitle")}</DialogTitle>
						<DialogDescription className="mt-0.5 font-mono text-2xs tabular-nums text-muted-foreground">
							{t("shell.memoryPanelSummary", { size: formatMemory(total), count: rows.filter((row) => row.reading).length, processes })}
						</DialogDescription>
					</div>
					<Button
						disabled={cleanup.isPending}
						onClick={() => cleanup.mutate()}
						size="sm"
						variant="outline"
					>
						{cleanup.isPending ? t("shell.memoryCleanupRunning") : t("shell.memoryCleanup")}
					</Button>
					<DialogClose asChild>
						<Button aria-label={t("common.close")} size="icon" variant="ghost">
							<X className="size-icon-md" aria-hidden="true" />
						</Button>
					</DialogClose>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-0 p-0")}>
					{cleanup.isError ? (
						<p className="border-b border-border px-4 py-2 text-2xs text-destructive" role="alert">
							{cleanup.error.message}
						</p>
					) : null}
					<table className="w-full border-collapse text-xs" data-testid="session-memory-table">
						<thead>
							<tr className="text-2xs text-passive">
								<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnSession")}</th>
								<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnState")}</th>
								<th className="w-[28%] px-4 py-2 text-left font-medium">{t("shell.memoryColumnRss")}</th>
								<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnProcs")}</th>
								<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnIdle")}</th>
								<th className="px-4 py-2" />
							</tr>
						</thead>
						<tbody>
							{rows.map((row) => (
								<MemoryTableRow key={row.session.id} largest={largest} onTerminate={() => terminate.mutate(row.session)} row={row} />
							))}
						</tbody>
					</table>
					{rows.length === 0 ? (
						<p className="px-4 py-6 text-center text-xs text-passive">{t("shell.memoryEmpty")}</p>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function MemoryTableRow({
	largest,
	onTerminate,
	row,
}: {
	largest: number;
	onTerminate: () => void;
	row: MemoryRow;
}) {
	const { t } = useTranslation();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const termination = useTerminateSessionState(row.session.id);
	const { session, reading } = row;
	const activity = session.activity ? getAgentActivityView(session.activity, t) : undefined;
	const idle =
		session.activity?.state === "active" || !session.activity?.lastActivityAt
			? "—"
			: formatTimeTerse(session.activity.lastActivityAt);
	const tone = reading ? memoryTone(reading.rssBytes) : "default";
	const width = reading && largest > 0 ? `${Math.max(2, Math.round((reading.rssBytes / largest) * 100))}%` : "0%";
	return (
		<tr className="border-t border-border hover:bg-interactive-hover" data-testid="session-memory-row">
			<td className="max-w-0 px-4 py-2 align-middle">
				<div className="truncate font-medium" title={session.title}>{session.title}</div>
				<div className="truncate font-mono text-2xs text-passive">{session.id}</div>
			</td>
			<td className="whitespace-nowrap px-4 py-2 align-middle text-2xs text-muted-foreground">{activity?.label ?? "—"}</td>
			<td className="px-4 py-2 align-middle">
				<div className="h-1.5 w-full overflow-hidden rounded-sm bg-foreground/[0.06]">
					<div
						className={cn(
							"h-full rounded-sm",
							tone === "critical" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-accent-strong",
						)}
						style={{ width }}
					/>
				</div>
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums">
				{reading ? (
					<>
						<span className={cn("font-medium", tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-foreground")}>
							{formatMemory(reading.rssBytes)}
						</span>
						<span className="ml-2 text-muted-foreground">{reading.processCount}</span>
					</>
				) : (
					<span className="text-passive">—</span>
				)}
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">{idle}</td>
			<td className="whitespace-nowrap px-2 py-2 text-right align-middle">
				<SessionTerminationPopover
					onConfirm={() => {
						setConfirmOpen(false);
						onTerminate();
					}}
					onOpenChange={setConfirmOpen}
					open={confirmOpen}
					session={session}
					trigger={
						<Button
							aria-label={t("shell.terminateNamed", { title: session.title })}
							className="h-6 px-2 text-2xs text-error/80 hover:bg-error/10 hover:text-error"
							disabled={termination.isPending}
							size="sm"
							variant="ghost"
						>
							{termination.isPending ? t("shell.killing") : t("shell.kill")}
						</Button>
					}
				/>
			</td>
		</tr>
	);
}
