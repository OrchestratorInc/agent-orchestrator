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
	formatCPU,
	formatMemory,
	memoryPressure,
	memoryTone,
	sessionMemoryQueryRoot,
	useAppMemory,
	useSessionMemory,
	useSystemMemory,
	type SessionMemoryReading,
	type SystemMemoryReading,
} from "../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { AgentPausePopover } from "./AgentPausePopover";
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

/**
 * Shown beside the light only while it is red and there is something cheap
 * to do about it: idle, unpaused workers. One click runs the same sweep the
 * panel offers. AO's pause exits the agent process (the session, worktree and
 * conversation stay), so unlike a SIGSTOP it really does give the memory
 * back. It subscribes to sessions only while mounted, so the bar stays quiet
 * in the normal case.
 */
function MemoryPressureSuggestion() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaces = useWorkspaceQuery().data ?? [];
	const readings = useSessionMemory().data;
	const idle = workspaces
		.flatMap((workspace) => workspace.sessions)
		.filter((session) => canPauseAgent(session) && !session.pausedAt && session.activity?.state === "idle");
	const frees = idle.reduce((sum, session) => sum + (readings?.get(session.id)?.rssBytes ?? 0), 0);
	const pauseIdle = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/sessions/pause-idle", { params: { query: {} } });
			if (error) throw new Error(apiErrorMessage(error, t("shell.memoryPauseIdleFailed")));
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		},
	});
	if (idle.length === 0) return null;
	return (
		<button
			className="mr-3 inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-2xs text-warning transition-colors hover:bg-warning/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:opacity-60"
			data-testid="memory-pressure-suggestion"
			disabled={pauseIdle.isPending}
			onClick={() => pauseIdle.mutate()}
			type="button"
		>
			{pauseIdle.isPending
				? t("shell.memoryPauseIdleRunning")
				: t("shell.memoryPressureSuggest", { count: idle.length, size: formatMemory(frees) })}
		</button>
	);
}

/** True once the daemon has produced an app-wide reading; gates the archive bar. */
export function useHasAppMemory(): boolean {
	const memory = useAppMemory();
	return !memory.isError && (memory.data?.app?.rssBytes ?? 0) > 0;
}

/** Rate of swapping, for the bar: whole megabytes per second. */
function formatRate(bytesPerSec: number): string {
	return `${formatMemory(bytesPerSec)}/s`;
}

/**
 * Archive-bar light: a dot, free host RAM, and the live session count. The
 * swap rate appears only while swapping and the load only while the cores
 * are pinned, so the bar says more only when there is more to say; the
 * tooltip and the panel carry the rest. It reads only the memory query so the
 * board keeps its identity while sessions stream updates; the panel
 * subscribes to sessions only while open. Where the host can't be read the
 * dot is grey and only AO's own size shows.
 */
export function AppMemoryIndicator() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useAppMemory();
	const app = memory.data?.app;
	const system = memory.data?.system;
	const reserve = memory.data?.reserve;
	if (memory.isError || !app || app.rssBytes === 0) {
		return null;
	}
	const pressure = system ? memoryPressure(system, reserve?.bytes) : undefined;
	const headline = system ? t("shell.memoryBarFree", { free: formatMemory(system.availableBytes) }) : formatMemory(app.rssBytes);
	const detail = system && pressure
		? t("shell.memoryBarDetail", {
			free: formatMemory(system.availableBytes),
			total: formatMemory(system.totalBytes),
			freePct: pressure.freePct,
			used: formatMemory(app.rssBytes),
			load: pressure.cpuLoad.toFixed(2),
		})
		: t("shell.memoryAppUsageNoTotal", { used: formatMemory(app.rssBytes) });
	const extras: string[] = [];
	if (system && pressure?.swapping) extras.push(t("shell.memoryBarSwap", { rate: formatRate(system.swapBytesPerSec) }));
	if (pressure && pressure.cpuLoad >= 1) extras.push(t("shell.memoryBarLoad", { load: pressure.cpuLoad.toFixed(1) }));
	if (pressure?.belowReserve && reserve) extras.push(t("shell.memoryBarBelowReserve", { size: formatMemory(reserve.bytes) }));
	const count = memory.data?.liveCount ?? 0;
	return (
		<>
			{pressure?.tone === "critical" ? <MemoryPressureSuggestion /> : null}
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={detail}
						className="inline-flex items-center gap-2 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-tone={pressure?.tone ?? "unknown"}
						data-memory-reason={pressure?.reason}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span
							aria-hidden="true"
							className={cn(
								"size-1.5 shrink-0 rounded-full",
								pressure?.tone === "critical" && "animate-status-pulse bg-destructive",
								pressure?.tone === "warning" && "bg-warning",
								pressure?.tone === "default" && "bg-success",
								!pressure && "bg-passive",
							)}
						/>
						<span className={cn(pressure?.tone === "critical" && "text-destructive", pressure?.tone === "warning" && "text-warning")}>
							{headline}
						</span>
						{extras.map((extra) => (
							<span className="text-passive" key={extra}>· {extra}</span>
						))}
						<span className="text-passive">· {t("shell.memoryBarSessions", { count })}</span>
					</button>
				</TooltipTrigger>
				<TooltipContent side="top">{detail}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}

/** btop's mem widget: one bar for the whole host, not one per row. The tick
 * marks the user's reserve: below it auto-started spawns wait. */
export function SystemMemoryBar({ reserveBytes, system }: { reserveBytes?: number; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const used = system.totalBytes - system.availableBytes;
	const usedPct = system.totalBytes > 0 ? Math.min(100, Math.round((used / system.totalBytes) * 100)) : 0;
	const reservePct = reserveBytes && system.totalBytes > 0 ? Math.min(100, (reserveBytes / system.totalBytes) * 100) : undefined;
	const swap = system.swapTotalBytes > 0
		? t("shell.memorySystemSwap", { used: formatMemory(system.swapUsedBytes), total: formatMemory(system.swapTotalBytes) })
		: undefined;
	return (
		<div className="flex items-center gap-3 border-b border-border px-4 py-2">
			<div className="relative h-1.5 flex-1 rounded-sm bg-foreground/[0.06]">
				<div
					className={cn("h-full rounded-sm", usedPct > 90 ? "bg-destructive" : usedPct > 75 ? "bg-warning" : "bg-accent-strong")}
					style={{ width: `${usedPct}%` }}
				/>
				{reservePct !== undefined ? (
					<span
						aria-hidden="true"
						className="absolute -top-0.5 h-2.5 w-px bg-foreground/50"
						data-testid="session-memory-reserve-tick"
						style={{ right: `${reservePct}%` }}
						title={t("shell.memoryReserveTick", { size: formatMemory(reserveBytes ?? 0) })}
					/>
				) : null}
			</div>
			<span className="whitespace-nowrap font-mono text-2xs tabular-nums text-muted-foreground">
				{t("shell.memorySystemBar", { used: formatMemory(used), total: formatMemory(system.totalBytes), free: formatMemory(system.availableBytes) })}
				{swap ? ` · ${swap}` : null}
				{system.cpuCount > 0 ? ` · ${t("shell.memorySystemLoad", { load: system.load1.toFixed(1), cores: system.cpuCount })}` : null}
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
	const appMemory = useAppMemory().data;
	const app = appMemory?.app;
	const pressure = appMemory?.system ? memoryPressure(appMemory.system, appMemory.reserve?.bytes) : undefined;
	const underPressure = pressure !== undefined && pressure.tone !== "default";
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
	// Each row's bar is its share of everything AO holds, AO's own processes
	// included: it answers "which one is the pig", nothing else.
	const shareOf = app?.rssBytes && app.rssBytes > 0 ? app.rssBytes : total;
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
	// Bulk pause: every idle, unpaused worker in scope. Freed is summed from the
	// readings held at click time, since the daemon only names who it paused.
	const [pauseIdleResult, setPauseIdleResult] = useState<{ count: number; freedBytes: number } | undefined>();
	const pauseIdle = useMutation({
		mutationFn: async () => {
			const before = new Map(rows.map((row) => [row.session.id, row.reading?.rssBytes ?? 0] as const));
			const { data, error } = await apiClient.POST("/api/v1/sessions/pause-idle", {
				params: { query: projectId ? { project: projectId } : {} },
			});
			if (error) throw new Error(apiErrorMessage(error, t("shell.memoryPauseIdleFailed")));
			const paused = data?.paused ?? [];
			return { count: paused.length, freedBytes: paused.reduce((sum, id) => sum + (before.get(id) ?? 0), 0) };
		},
		onSuccess: (result) => setPauseIdleResult(result),
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		},
	});
	const idleCount = rows.filter((row) => canPauseAgent(row.session) && !row.session.pausedAt && row.session.activity?.state === "idle").length;
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
						{underPressure && rows[0]?.reading ? (
							<p className="mt-0.5 text-2xs text-warning" data-testid="session-memory-hint">
								{t("shell.memoryHint", { title: rows[0].session.title, size: formatMemory(rows[0].reading.rssBytes) })}
							</p>
						) : null}
					</div>
					<Button
						data-testid="session-memory-pause-idle"
						disabled={pauseIdle.isPending || idleCount === 0}
						onClick={() => pauseIdle.mutate()}
						size="sm"
						title={idleCount === 0 ? t("shell.memoryPauseIdleNone") : undefined}
						variant="outline"
					>
						{pauseIdle.isPending ? t("shell.memoryPauseIdleRunning") : t("shell.memoryPauseIdle")}
					</Button>
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
				{system ? <SystemMemoryBar reserveBytes={appMemory?.reserve?.bytes} system={system} /> : null}
				<div className={cn(settingsDialogBodyClass, "gap-0 p-0")}>
					{cleanup.isError || pauseIdle.isError ? (
						<p className="border-b border-border px-4 py-2 text-2xs text-destructive" role="alert">
							{cleanup.error?.message ?? pauseIdle.error?.message}
						</p>
					) : pauseIdleResult ? (
						<p className="border-b border-border px-4 py-2 text-2xs text-muted-foreground" role="status">
							{t("shell.memoryPauseIdleDone", { count: pauseIdleResult.count, size: formatMemory(pauseIdleResult.freedBytes) })}
						</p>
					) : null}
					<MemoryTable
						emptyLabel={t("shell.memoryEmpty")}
						expandedSessionId={expandedSessionId}
						highlightSessionId={underPressure ? rows[0]?.session.id : undefined}
						onTerminate={(session) => terminate.mutate(session)}
						onToggle={(sessionId) =>
							setExpandedSessionId((current) => (current === sessionId ? undefined : sessionId))
						}
						own={app?.own}
						rows={rows}
						shareOfBytes={shareOf}
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
	highlightSessionId,
	onTerminate,
	onToggle,
	own,
	rows,
	shareOfBytes,
}: {
	emptyLabel: string;
	expandedSessionId?: string;
	/** The row the pressure hint points at. */
	highlightSessionId?: string;
	onTerminate: (session: WorkspaceSession) => void;
	onToggle: (sessionId: string) => void;
	/** AO's own daemon and shell, pinned last and not pausable, so the totals add up. */
	own?: SessionMemoryReading;
	rows: MemoryRow[];
	/** Denominator for each row's share bar; defaults to the rows' sum. */
	shareOfBytes?: number;
}) {
	const { t } = useTranslation();
	if (rows.length === 0 && !own) {
		return <p className="px-4 py-6 text-center text-xs text-passive">{emptyLabel}</p>;
	}
	const denominator = shareOfBytes ?? rows.reduce((sum, row) => sum + (row.reading?.rssBytes ?? 0), 0);
	// The orchestrator manages tasks, it isn't one: mixed into one sorted-by-size
	// list it reads as a peer task the user never started. Group it apart.
	const taskRows = rows.filter((row) => !isOrchestratorSession(row.session));
	const orchestratorRows = rows.filter((row) => isOrchestratorSession(row.session));
	const showGroupLabels = taskRows.length > 0 && orchestratorRows.length > 0;
	const renderRow = (row: MemoryRow) => (
		<MemoryTableRow
			isExpanded={expandedSessionId === row.session.id}
			isHighlighted={highlightSessionId === row.session.id}
			key={row.session.id}
			onTerminate={() => onTerminate(row.session)}
			onToggle={() => onToggle(row.session.id)}
			row={row}
			shareOfBytes={denominator}
		/>
	);
	return (
		<table className="w-full border-collapse text-xs" data-testid="session-memory-table">
			<thead>
				<tr className="text-2xs text-passive">
					<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnSession")}</th>
					<th className="px-4 py-2 text-left font-medium">{t("shell.memoryColumnState")}</th>
					<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnRss")}</th>
					<th className="px-4 py-2 text-right font-medium">{t("shell.memoryColumnCpu")}</th>
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
				{own ? (
					<>
						<MemoryGroupRow label={t("shell.memoryGroupApp")} />
						<OwnMemoryRow reading={own} shareOfBytes={denominator} />
					</>
				) : null}
			</tbody>
		</table>
	);
}

const columnCount = 7;

function MemoryGroupRow({ label }: { label: string }) {
	return (
		<tr className="border-t border-border">
			<td className="px-4 pb-1 pt-3 text-2xs font-medium text-passive" colSpan={columnCount}>{label}</td>
		</tr>
	);
}

/** The thin bar under a name: this row's slice of what AO holds. */
function ShareBar({ rssBytes, shareOfBytes }: { rssBytes: number; shareOfBytes: number }) {
	const pct = shareOfBytes > 0 ? Math.min(100, (rssBytes / shareOfBytes) * 100) : 0;
	const tone = memoryTone(rssBytes);
	return (
		<div className="mt-1 h-0.5 w-full max-w-[12rem] rounded-sm bg-foreground/[0.06]" data-testid="session-memory-share">
			<div
				className={cn("h-full rounded-sm", tone === "critical" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-accent-strong")}
				style={{ width: `${pct}%` }}
			/>
		</div>
	);
}

/** AO's daemon and desktop shell: real cost, but not a session, so no pause or kill. */
function OwnMemoryRow({ reading, shareOfBytes }: { reading: SessionMemoryReading; shareOfBytes: number }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const canExpand = reading.processes.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? expanded : undefined}
				className={cn("border-t border-border", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-testid="session-memory-own-row"
				onClick={canExpand ? () => setExpanded((open) => !open) : undefined}
			>
				<td className="max-w-0 px-4 py-2 align-middle">
					<div className="flex items-center gap-1.5">
						<ChevronRight
							aria-hidden="true"
							className={cn("size-icon-2xs shrink-0 text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", expanded && "rotate-90")}
						/>
						<div className="min-w-0">
							<div className="truncate font-medium">{t("shell.memoryOwnRow")}</div>
							<ShareBar rssBytes={reading.rssBytes} shareOfBytes={shareOfBytes} />
						</div>
					</div>
				</td>
				<td className="whitespace-nowrap px-4 py-2 align-middle text-2xs text-muted-foreground">—</td>
				<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs font-medium tabular-nums">{formatMemory(reading.rssBytes)}</td>
				<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">{formatCPU(reading.cpuPercent)}</td>
				<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">{reading.processCount}</td>
				<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">—</td>
				<td className="px-2 py-2" />
			</tr>
			{expanded ? <ProcessBreakdownRow processes={reading.processes} /> : null}
		</>
	);
}

function MemoryTableRow({
	isExpanded,
	isHighlighted,
	onTerminate,
	onToggle,
	row,
	shareOfBytes,
}: {
	isExpanded: boolean;
	isHighlighted?: boolean;
	onTerminate: () => void;
	onToggle: () => void;
	row: MemoryRow;
	shareOfBytes: number;
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
			className={cn("border-t border-border", canExpand && "cursor-pointer hover:bg-interactive-hover", isHighlighted && "bg-warning/10")}
			data-highlighted={isHighlighted ? "true" : undefined}
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
						{reading ? <ShareBar rssBytes={reading.rssBytes} shareOfBytes={shareOfBytes} /> : null}
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
				{reading ? formatCPU(reading.cpuPercent) : "—"}
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">
				{reading?.processCount ?? "—"}
			</td>
			<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-2xs tabular-nums text-muted-foreground">{idle}</td>
			<td className="whitespace-nowrap px-2 py-2 text-right align-middle" onClick={(event) => event.stopPropagation()}>
				{canPauseAgent(session) ? (
					<PauseRowButton pause={pause} session={session} />
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

/** Pause or play for one row; mid-turn it asks drain vs interrupt like the card. */
function PauseRowButton({ pause, session }: { pause: ReturnType<typeof useAgentPause>; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const needsPolicy = !pause.paused && (session.activity?.state === "active" || pause.drainBlocked);
	const button = (
		<Button
			aria-label={pause.paused ? t("shell.resumeAgentNamed", { title: session.title }) : t("shell.pauseAgentNamed", { title: session.title })}
			className="text-muted-foreground hover:text-foreground"
			disabled={pause.isPending}
			onClick={() => (needsPolicy ? setOpen(true) : pause.toggle())}
			size="icon-sm"
			title={pause.isDraining ? t("shell.pausingAfterTurn") : pause.paused ? t("shell.resumeAgent") : t("shell.pauseAgent")}
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
	);
	if (!needsPolicy) return button;
	return (
		<AgentPausePopover
			blocked={pause.drainBlocked}
			onChoose={(policy) => {
				setOpen(false);
				pause.toggle(policy);
			}}
			onOpenChange={setOpen}
			open={open}
			session={session}
			trigger={button}
		/>
	);
}

/** btop's core affordance: expand a session to see exactly what is holding its memory. */
function ProcessBreakdownRow({ processes }: { processes: SessionMemoryReading["processes"] }) {
	const { t } = useTranslation();
	const sorted = [...processes].sort((a, b) => b.rssBytes - a.rssBytes);
	return (
		<tr className="border-t border-border bg-foreground/[0.02]" data-testid="session-memory-process-row">
			<td className="p-0" colSpan={columnCount}>
				<table className="w-full border-collapse text-2xs">
					<thead>
						<tr className="text-passive">
							<th className="py-1.5 pl-11 pr-2 text-left font-medium">{t("shell.memoryColumnProcess")}</th>
							<th className="px-2 py-1.5 text-right font-medium">{t("shell.memoryColumnRss")}</th>
							<th className="px-2 py-1.5 text-right font-medium">{t("shell.memoryColumnCpu")}</th>
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
								<td className="whitespace-nowrap px-2 py-1.5 text-right font-mono tabular-nums text-muted-foreground">
									{formatCPU(process.cpuPercent)}
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
