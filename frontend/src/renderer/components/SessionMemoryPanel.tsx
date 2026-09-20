import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronRight, Loader2, Pause, Play, Trash2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { formatTimeTerse } from "../lib/format-time";
import { getAgentActivityView } from "../lib/session-presentation";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useTerminateSession, useTerminateSessionState } from "../hooks/useTerminateSession";
import { canPauseAgent, useAgentPause } from "../hooks/useAgentPause";
import {
	formatMemory,
	memoryPressure,
	memoryTone,
	useAppMemory,
	useSessionMemory,
	useSystemMemory,
	type SessionMemoryReading,
	type SystemMemoryReading,
} from "../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { SessionTerminationPopover } from "./SessionTerminationPopover";
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

export type MemoryRow = {
	session: WorkspaceSession;
	reading?: SessionMemoryReading;
	/** Set when the panel spans projects, so a row says where it lives. */
	projectName?: string;
};

/** True once the daemon has produced an app-wide reading; gates the archive bar. */
export function useHasAppMemory(): boolean {
	const memory = useAppMemory();
	return !memory.isError && (memory.data?.app?.rssBytes ?? 0) > 0;
}

/**
 * Archive-bar indicator: a status dot plus AO's share of host RAM as a
 * percent, colored by pressure, with the btop-style panel behind it. It
 * reads only the memory query so the board keeps its identity while sessions
 * stream updates; the panel subscribes to sessions only while open. Where
 * host RAM can't be read it falls back to the absolute size with no color.
 */
export function AppMemoryIndicator() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useAppMemory();
	const app = memory.data?.app;
	const system = memory.data?.system;
	if (memory.isError || !app || app.rssBytes === 0) {
		return null;
	}
	const pressure = system ? memoryPressure(app.rssBytes, system.totalBytes) : undefined;
	const label = system
		? t("shell.memoryAppUsage", { used: formatMemory(app.rssBytes), total: formatMemory(system.totalBytes), pct: pressure?.pct ?? 0 })
		: t("shell.memoryAppUsageNoTotal", { used: formatMemory(app.rssBytes) });
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={label}
						className="inline-flex items-center gap-1.5 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-tone={pressure?.tone ?? "unknown"}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span
							aria-hidden="true"
							className={cn(
								"size-1.5 shrink-0 rounded-full",
								pressure?.tone === "critical" && "bg-destructive",
								pressure?.tone === "warning" && "bg-warning",
								pressure?.tone === "default" && "bg-success",
								!pressure && "bg-passive",
							)}
						/>
						<span>{pressure ? `${pressure.pct}%` : formatMemory(app.rssBytes)}</span>
					</button>
				</TooltipTrigger>
				<TooltipContent side="top">{label}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}

/** btop's mem widget: one bar for the whole host, not one per row. */
export function SystemMemoryBar({ system }: { system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const used = system.totalBytes - system.availableBytes;
	const usedPct = system.totalBytes > 0 ? Math.min(100, Math.round((used / system.totalBytes) * 100)) : 0;
	return (
		<div className="flex items-center gap-3 border-b border-border px-4 py-2">
			<div className="h-1.5 flex-1 overflow-hidden rounded-sm bg-foreground/[0.06]">
				<div
					className={cn("h-full rounded-sm", usedPct >= 90 ? "bg-destructive" : usedPct >= 75 ? "bg-warning" : "bg-accent-strong")}
					style={{ width: `${usedPct}%` }}
				/>
			</div>
			<span className="whitespace-nowrap font-mono text-2xs tabular-nums text-muted-foreground">
				{t("shell.memorySystemBar", { used: formatMemory(used), total: formatMemory(system.totalBytes), free: formatMemory(system.availableBytes) })}
			</span>
		</div>
	);
}

export function toRows(
	sessions: WorkspaceSession[],
	readings?: Map<string, SessionMemoryReading>,
	projectNameOf?: (session: WorkspaceSession) => string | undefined,
): MemoryRow[] {
	return sessions
		.filter((session) => session.isTerminated !== true)
		.map((session) => ({ session, reading: readings?.get(session.id), projectName: projectNameOf?.(session) }))
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
	const workspace = projectId ? workspaces.find((w) => w.id === projectId) : undefined;
	const memory = useSessionMemory(projectId);
	const system = useSystemMemory(projectId).data;
	const app = useAppMemory().data?.app;
	// Includes the project's orchestrator session, not just worker sessions: it
	// has its own process tree and is why the topbar pill can read nonzero
	// with an empty board. Omitting it here made the panel's total silently
	// disagree with the pill it was opened from.
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workspace.sessions),
		[workspaces, projectId],
	);
	const projectNames = useMemo(() => new Map(workspaces.map((w) => [w.id, w.name] as const)), [workspaces]);
	const rows = useMemo(
		() => toRows(sessions, memory.data, projectId ? undefined : (session) => projectNames.get(session.workspaceId)),
		[sessions, memory.data, projectId, projectNames],
	);
	const total = rows.reduce((sum, row) => sum + (row.reading?.rssBytes ?? 0), 0);
	const [expandedSessionId, setExpandedSessionId] = useState<string | undefined>();
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
	const processes = rows.reduce((sum, row) => sum + (row.reading?.processCount ?? 0), 0);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={cn(settingsDialogContentClass, "w-[min(56rem,calc(100vw-var(--space-8)))]")} showCloseButton={false}>
				<div className={cn(settingsDialogHeaderClass, "flex-row items-center gap-3")}>
					<div className="min-w-0 flex-1">
						<DialogTitle className="text-sm font-medium">{workspace?.name ?? t("shell.memoryPanelTitle")}</DialogTitle>
						<DialogDescription className="mt-0.5 font-mono text-2xs tabular-nums text-muted-foreground">
							{t("shell.memoryPanelSummary", { size: formatMemory(total), count: rows.filter((row) => row.reading).length, processes })}
							{app ? ` · ${t("shell.memoryPanelApp", { size: formatMemory(app.rssBytes) })}` : null}
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
				{system ? <SystemMemoryBar system={system} /> : null}
				<div className={cn(settingsDialogBodyClass, "gap-0 p-0")}>
					{cleanup.isError ? (
						<p className="border-b border-border px-4 py-2 text-2xs text-destructive" role="alert">
							{cleanup.error.message}
						</p>
					) : null}
					<MemoryTable
						emptyLabel={t("shell.memoryEmpty")}
						expandedSessionId={expandedSessionId}
						onTerminate={(session) => terminate.mutate(session)}
						onToggle={(sessionId) =>
							setExpandedSessionId((current) => (current === sessionId ? undefined : sessionId))
						}
						rows={rows}
					/>
				</div>
			</DialogContent>
		</Dialog>
	);
}

/** The btop-style rows, shared by the topbar panel and the Settings memory page. */
export function MemoryTable({
	emptyLabel,
	expandedSessionId,
	onTerminate,
	onToggle,
	rows,
}: {
	emptyLabel: string;
	expandedSessionId?: string;
	onTerminate: (session: WorkspaceSession) => void;
	onToggle: (sessionId: string) => void;
	rows: MemoryRow[];
}) {
	const { t } = useTranslation();
	if (rows.length === 0) {
		return <p className="px-4 py-6 text-center text-xs text-passive">{emptyLabel}</p>;
	}
	// The orchestrator manages tasks, it isn't one: mixed into one sorted-by-size
	// list it reads as a peer task the user never started. Group it apart.
	const taskRows = rows.filter((row) => !isOrchestratorSession(row.session));
	const orchestratorRows = rows.filter((row) => isOrchestratorSession(row.session));
	const showGroupLabels = taskRows.length > 0 && orchestratorRows.length > 0;
	const renderRow = (row: MemoryRow) => (
		<MemoryTableRow
			isExpanded={expandedSessionId === row.session.id}
			key={row.session.id}
			onTerminate={() => onTerminate(row.session)}
			onToggle={() => onToggle(row.session.id)}
			row={row}
		/>
	);
	return (
		<table className="w-full border-collapse text-xs" data-testid="session-memory-table">
			<thead>
				<tr className="text-2xs text-passive">
					<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnSession")}</th>
					<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnState")}</th>
					<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnRss")}</th>
					<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnProcs")}</th>
					<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnIdle")}</th>
					<th className="px-4 py-2" />
				</tr>
			</thead>
			<tbody>
				{showGroupLabels ? <MemoryGroupRow label={t("shell.memoryGroupTasks")} /> : null}
				{taskRows.map(renderRow)}
				{showGroupLabels ? <MemoryGroupRow label={t("shell.memoryGroupOrchestrator")} /> : null}
				{orchestratorRows.map(renderRow)}
			</tbody>
		</table>
	);
}

function MemoryGroupRow({ label }: { label: string }) {
	return (
		<tr className="border-t border-border">
			<td className="px-4 pb-1 pt-3 text-2xs font-medium text-passive" colSpan={6}>{label}</td>
		</tr>
	);
}

function MemoryTableRow({
	isExpanded,
	onTerminate,
	onToggle,
	row,
}: {
	isExpanded: boolean;
	onTerminate: () => void;
	onToggle: () => void;
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
	const canExpand = Boolean(reading && reading.processes.length > 0);
	const pause = useAgentPause(session);
	return (
		<>
		<tr
			aria-expanded={canExpand ? isExpanded : undefined}
			className={cn("border-t border-border", canExpand && "cursor-pointer hover:bg-interactive-hover")}
			data-testid="session-memory-row"
			onClick={canExpand ? onToggle : undefined}
		>
			<td className="max-w-0 px-4 py-2 align-middle">
				<div className="flex items-center gap-1.5">
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-icon-2xs shrink-0 text-passive transition-transform",
							canExpand ? "opacity-100" : "opacity-0",
							isExpanded && "rotate-90",
						)}
					/>
					<div className="min-w-0">
						<div className="truncate font-medium" title={session.title}>{session.title}</div>
						<div className="truncate font-mono text-2xs text-passive">
							{row.projectName ? `${row.projectName} · ` : null}
							{session.id}
						</div>
					</div>
				</div>
			</td>
			<td className="whitespace-nowrap px-4 py-2 align-middle text-2xs text-muted-foreground">{activity?.label ?? "—"}</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs font-medium tabular-nums">
				{reading ? (
					<span className={tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-foreground"}>
						{formatMemory(reading.rssBytes)}
					</span>
				) : (
					<span className="text-passive">—</span>
				)}
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">
				{reading?.processCount ?? "—"}
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">{idle}</td>
			<td className="whitespace-nowrap px-2 py-2 text-right align-middle" onClick={(event) => event.stopPropagation()}>
				{canPauseAgent(session) ? (
					<Button
						aria-label={pause.paused ? t("shell.resumeAgentNamed", { title: session.title }) : t("shell.pauseAgentNamed", { title: session.title })}
						className="text-muted-foreground hover:text-foreground"
						disabled={pause.isPending}
						onClick={pause.toggle}
						size="icon-sm"
						title={pause.paused ? t("shell.resumeAgent") : t("shell.pauseAgent")}
						variant="ghost"
					>
						{pause.isPending ? (
							<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
						) : pause.paused ? (
							<Play className="size-icon-sm" aria-hidden="true" />
						) : (
							<Pause className="size-icon-sm" aria-hidden="true" />
						)}
					</Button>
				) : null}
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
							className="text-error/70 hover:bg-error/10 hover:text-error"
							disabled={termination.isPending}
							size="icon-sm"
							title={t("shell.kill")}
							variant="ghost"
						>
							{termination.isPending ? <Loader2 className="size-icon-sm animate-spin" aria-hidden="true" /> : <Trash2 className="size-icon-sm" aria-hidden="true" />}
						</Button>
					}
				/>
			</td>
		</tr>
		{isExpanded && reading ? <ProcessBreakdownRow processes={reading.processes} /> : null}
		</>
	);
}

/** btop's core affordance: expand a session to see exactly what is holding its memory. */
function ProcessBreakdownRow({ processes }: { processes: SessionMemoryReading["processes"] }) {
	const { t } = useTranslation();
	const sorted = [...processes].sort((a, b) => b.rssBytes - a.rssBytes);
	return (
		<tr className="border-t border-border bg-foreground/[0.02]" data-testid="session-memory-process-row">
			<td className="p-0" colSpan={6}>
				<table className="w-full border-collapse text-2xs">
					<thead>
						<tr className="text-passive">
							<th className="py-1.5 pl-11 pr-2 text-left font-medium">{t("shell.memoryColumnProcess")}</th>
							<th className="px-2 py-1.5 text-right font-medium">{t("shell.memoryColumnRss")}</th>
							<th className="px-4 py-1.5 text-right font-medium">{t("shell.memoryColumnPid")}</th>
						</tr>
					</thead>
					<tbody>
						{sorted.map((process) => (
							<tr className="border-t border-border/60" key={process.pid}>
								<td className="truncate py-1.5 pl-11 pr-2 font-mono text-muted-foreground" title={process.command}>
									{process.command || "?"}
								</td>
								<td className="whitespace-nowrap px-2 py-1.5 text-right font-mono tabular-nums text-foreground">
									{formatMemory(process.rssBytes)}
								</td>
								<td className="whitespace-nowrap px-4 py-1.5 text-right font-mono tabular-nums text-passive">{process.pid}</td>
							</tr>
						))}
					</tbody>
				</table>
			</td>
		</tr>
	);
}
