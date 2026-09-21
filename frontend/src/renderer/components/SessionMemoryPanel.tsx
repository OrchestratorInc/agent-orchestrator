import { useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, Loader2, Pause, Play, Square, X } from "lucide-react";
import {
	chipTone,
	largestSession,
	pressureState,
	pressureStateFromRaw,
	resourceSuggestion,
	stableResourceOrder,
	type ChipTone,
	type PressureState,
	type ResourceSessionFacts,
	type ResourceSuggestion,
} from "@aoagents/product-ui";
import { cn } from "@/lib/utils";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { canPauseAgent, isAgentPaused, useAgentPause } from "../hooks/useAgentPause";
import {
	formatCPU,
	formatMemory,
	sessionMemoryQueryRoot,
	useAppMemory,
	useFastMemorySampling,
	usePressureHistory,
	useSessionMemory,
	type SessionMemoryReading,
	type SystemMemoryReading,
} from "../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { AgentPausePopover } from "./AgentPausePopover";
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

/** True once the daemon has produced an app-wide reading; gates the archive bar. */
export function useHasAppMemory(): boolean {
	const memory = useAppMemory();
	return !memory.isError && (memory.data?.app?.rssBytes ?? 0) > 0;
}

/** What the monitor knows about a session, from the board plus its reading. */
export function toSessionFacts(session: WorkspaceSession, reading: SessionMemoryReading | undefined, now: number): ResourceSessionFacts {
	const lastActivity = session.activity?.lastActivityAt ? Date.parse(session.activity.lastActivityAt) : Number.NaN;
	return {
		id: session.id,
		title: session.title,
		rssBytes: reading?.rssBytes ?? 0,
		working: session.activity?.state === "active",
		idleSeconds: Number.isNaN(lastActivity) ? undefined : Math.max(0, (now - lastActivity) / 1000),
		paused: isAgentPaused(session),
		pausable: canPauseAgent(session),
	};
}

const stateDot: Record<PressureState, string> = {
	fine: "bg-success",
	tight_soon: "bg-warning",
	tight: "animate-status-pulse bg-destructive",
};
const stateText: Record<PressureState, string> = {
	fine: "",
	tight_soon: "text-warning",
	tight: "text-destructive",
};

/** Idle over this long is what the "stop idle" suggestion sweeps; sent to the daemon in minutes. */
const IDLE_SUGGESTION_MINUTES = 30;

/**
 * The single fix the monitor offers, and the mutation behind its button.
 * Stopping idle sessions runs the daemon's own sweep so the two never
 * disagree on who counts as idle.
 */
function useSuggestion(projectId?: string) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaces = useWorkspaceQuery().data ?? [];
	const readings = useSessionMemory().data;
	const memory = useAppMemory().data;
	const now = Date.now();
	const facts = workspaces
		.filter((workspace) => !projectId || workspace.id === projectId)
		.flatMap((workspace) => workspace.sessions)
		.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session))
		.map((session) => toSessionFacts(session, readings?.get(session.id), now));
	const state = memory?.system ? pressureState(memory.system) : undefined;
	const suggestion: ResourceSuggestion =
		state && memory?.system && memory.app ? resourceSuggestion(state, memory.system, memory.app.rssBytes, facts) : { kind: "none" };
	const stopIdle = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/sessions/pause-idle", {
				params: { query: { ...(projectId ? { project: projectId } : {}), idleMinutes: IDLE_SUGGESTION_MINUTES } },
			});
			if (error) throw new Error(apiErrorMessage(error, t("shell.memoryStopIdleFailed")));
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		},
	});
	return { state, suggestion, facts, stopIdle };
}

/** Bar phrase: the state word, AO's size, and the fix if there is one. */
function suggestionLabel(suggestion: ResourceSuggestion, t: TFunction): string | undefined {
	switch (suggestion.kind) {
		case "other_apps":
			return t("shell.memoryFixNotAO");
		case "stop_idle":
			return t("shell.memoryFixStopIdle", { count: suggestion.count, size: formatMemory(suggestion.freesBytes) });
		case "pause_largest":
			return t("shell.memoryFixPause", { title: suggestion.title });
		default:
			return undefined;
	}
}

/**
 * Archive-bar light: a dot, the state word, AO's size and the fix. Colour
 * means something needs doing; grey means nothing does. The tooltip holds
 * the machine figures, the window behind it everything else.
 */
export function AppMemoryIndicator() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useAppMemory();
	const { state, suggestion } = useSuggestion();
	const app = memory.data?.app;
	const system = memory.data?.system;
	if (memory.isError || !app || app.rssBytes === 0) {
		return null;
	}
	const word = state ? t(`shell.memoryState.${state}`) : t("shell.memoryState.unknown");
	const fix = suggestionLabel(suggestion, t);
	const detail = system
		? t("shell.memoryBarDetail", {
			free: formatMemory(system.availableBytes),
			total: formatMemory(system.totalBytes),
			used: formatMemory(app.rssBytes),
			pressure: system.pressureRaw.toFixed(1),
		})
		: t("shell.memoryAppUsageNoTotal", { used: formatMemory(app.rssBytes) });
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label={detail}
						className="inline-flex items-center gap-2 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-state={state ?? "unknown"}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", state ? stateDot[state] : "bg-passive")} />
						<span className={state ? stateText[state] : undefined}>{word}</span>
						<span className="text-passive">· {t("shell.memoryBarAO", { size: formatMemory(app.rssBytes) })}</span>
						{fix ? <span className={state ? stateText[state] : undefined}>· {fix}</span> : null}
					</button>
				</TooltipTrigger>
				<TooltipContent side="top">{detail}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}

/** One stacked bar for the whole machine: AO sessions, AO app, everyone else, free. */
function StackedMemoryBar({ appBytes, sessionsBytes, system }: { appBytes: number; sessionsBytes: number; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const total = system.totalBytes || 1;
	const inUse = Math.max(0, system.totalBytes - system.availableBytes);
	const other = Math.max(0, inUse - sessionsBytes - appBytes);
	const pct = (bytes: number) => `${Math.min(100, (bytes / total) * 100)}%`;
	const legend = [
		{ key: "sessions", label: t("shell.memoryLegendSessions"), bytes: sessionsBytes, className: "bg-accent-strong" },
		{ key: "app", label: t("shell.memoryLegendApp"), bytes: appBytes, className: "bg-accent-strong/50" },
		{ key: "other", label: t("shell.memoryLegendOther"), bytes: other, className: "bg-foreground/25" },
		{ key: "free", label: t("shell.memoryLegendFree"), bytes: system.availableBytes, className: "bg-foreground/[0.06]" },
	];
	return (
		<div className="settings-row-bar h-auto flex-col items-stretch gap-2 py-3" data-testid="session-memory-stacked">
			<div className="flex h-2 w-full overflow-hidden rounded-sm bg-foreground/[0.06]">
				{legend.slice(0, 3).map((part) => (
					<div className={cn("h-full transition-[width] duration-500", part.className)} key={part.key} style={{ width: pct(part.bytes) }} />
				))}
			</div>
			<div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs tabular-nums text-settings-muted">
				{legend.map((part) => (
					<span className="inline-flex items-center gap-1.5" key={part.key}>
						<span aria-hidden="true" className={cn("size-1.5 rounded-full", part.className)} />
						{part.label} <span className="text-settings-label">{formatMemory(part.bytes)}</span>
					</span>
				))}
			</div>
		</div>
	);
}

/** The last minute of pressure, one bar per sample, coloured by the same rule as the dot. */
function PressureGraph({ history, source }: { history: number[]; source: string }) {
	const bars = [...Array.from({ length: Math.max(0, 60 - history.length) }, () => undefined), ...history];
	return (
		<div className="min-w-0 flex-1">
			<div aria-hidden="true" className="flex h-12 items-end gap-px" data-testid="session-memory-graph">
				{bars.map((value, index) => {
					const state = value === undefined ? undefined : pressureStateFromRaw(value, source);
					return (
						<div
							className={cn(
								"flex-1 rounded-t-[1px] transition-[height] duration-500",
								state === "tight" ? "bg-destructive" : state === "tight_soon" ? "bg-warning" : state === "fine" ? "bg-success" : "bg-transparent",
							)}
							key={index}
							style={{ height: `${Math.max(4, Math.min(100, value ?? 0))}%` }}
						/>
					);
				})}
			</div>
		</div>
	);
}

/** The lone line under the bar: what to do, and the one button that does it. */
function SuggestionLine({
	onPauseLargest,
	pending,
	state,
	stopIdle,
	suggestion,
}: {
	onPauseLargest: (sessionId: string) => void;
	pending: boolean;
	state: PressureState;
	stopIdle: () => void;
	suggestion: ResourceSuggestion;
}) {
	const { t } = useTranslation();
	if (suggestion.kind === "none") return null;
	const text =
		suggestion.kind === "other_apps"
			? t("shell.memorySuggestOtherApps", { size: formatMemory(suggestion.aoBytes) })
			: suggestion.kind === "stop_idle"
				? t("shell.memorySuggestIdle", { count: suggestion.count })
				: t("shell.memorySuggestLargest", { title: suggestion.title });
	return (
		<div className="settings-row-bar gap-3 text-sm" data-testid="session-memory-suggestion">
			<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", stateDot[state])} />
			<span className={cn("min-w-0 flex-1 truncate font-medium", stateText[state] || "text-settings-label")}>{text}</span>
			{suggestion.kind === "stop_idle" ? (
				<Button data-testid="session-memory-fix" disabled={pending} onClick={stopIdle} size="sm">
					{pending ? t("shell.memoryStopIdleRunning") : t("shell.memoryFixStopIdle", { count: suggestion.count, size: formatMemory(suggestion.freesBytes) })}
				</Button>
			) : suggestion.kind === "pause_largest" ? (
				<Button data-testid="session-memory-fix" onClick={() => onPauseLargest(suggestion.sessionId)} size="sm">
					{t("shell.memoryFixPause", { title: suggestion.title })}
				</Button>
			) : null}
		</div>
	);
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
	useFastMemorySampling();
	const workspaces = useWorkspaceQuery().data ?? [];
	const readings = useSessionMemory(projectId).data;
	const appMemory = useAppMemory().data;
	const app = appMemory?.app;
	const system = appMemory?.system;
	const history = usePressureHistory(system, app?.own?.sampledAt);
	const { state, suggestion, facts, stopIdle } = useSuggestion(projectId);
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workspace.sessions)
				.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session)),
		[workspaces, projectId],
	);
	// Rows with a reading are the live ones; a paused agent has no process
	// tree, so it is listed apart and greyed, and never counted as running.
	const orderRef = useRef<string[]>([]);
	const live = useMemo(() => {
		const rows = sessions
			.map((session) => ({ id: session.id, session, reading: readings?.get(session.id) }))
			.filter((row): row is { id: string; session: WorkspaceSession; reading: SessionMemoryReading } => row.reading !== undefined)
			.map((row) => ({ ...row, rssBytes: row.reading.rssBytes }));
		const ordered = stableResourceOrder(orderRef.current, rows);
		orderRef.current = ordered.map((row) => row.id);
		return ordered;
	}, [sessions, readings]);
	const paused = sessions.filter((session) => isAgentPaused(session));
	const sessionsBytes = live.reduce((sum, row) => sum + row.rssBytes, 0);
	const largest = largestSession(facts);
	const maxBytes = Math.max(app?.own?.rssBytes ?? 0, ...live.map((row) => row.rssBytes), 1);
	const [expanded, setExpanded] = useState<string | undefined>();
	const [pauseTarget, setPauseTarget] = useState<string | undefined>();
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={cn(settingsDialogContentClass, "w-[min(52rem,calc(100vw-var(--space-8)))]")} showCloseButton={false}>
				<div className={cn(settingsDialogHeaderClass, "flex h-auto flex-row items-center justify-between border-b-0 pb-3")}>
					<div className="min-w-0 flex-1">
						<DialogTitle className="text-lg font-semibold leading-6 text-settings-label">{t("shell.memoryPanelTitle")}</DialogTitle>
						<DialogDescription className="sr-only">{t("shell.memoryPanelDescription")}</DialogDescription>
					</div>
					<DialogClose asChild>
						<Button aria-label={t("common.close")} size="icon" variant="ghost">
							<X className="size-icon-md" aria-hidden="true" />
						</Button>
					</DialogClose>
				</div>
				<div className={cn(settingsDialogBodyClass, "settings-dialog-body flex-1 gap-(--size-settings-section-gap,1.5rem) px-(--size-modal-padding) pt-0")}>
					<section className="flex w-full flex-col items-stretch gap-(--size-settings-section-inner-gap)">
						<h2 className="text-xs font-medium leading-4 text-settings-muted">{t("shell.memorySectionMachine")}</h2>
						<div className="settings-grouped-rows flex w-full flex-col">
							{system && app ? <StackedMemoryBar appBytes={app.own?.rssBytes ?? 0} sessionsBytes={sessionsBytes} system={system} /> : null}
							{state ? (
								<SuggestionLine
									onPauseLargest={setPauseTarget}
									pending={stopIdle.isPending}
									state={state}
									stopIdle={() => stopIdle.mutate()}
									suggestion={suggestion}
								/>
							) : null}
							{stopIdle.isError ? (
								<p className="settings-row-bar text-xs text-error" role="alert">{stopIdle.error.message}</p>
							) : null}
						</div>
					</section>
					{live.length === 0 && paused.length === 0 && !app?.own ? (
						<p className="py-6 text-center text-xs text-settings-muted">{t("shell.memoryEmpty")}</p>
					) : (
						<table className="w-full border-collapse text-xs" data-testid="session-memory-table">
							<tbody>
								{live.length > 0 || paused.length > 0 ? <GroupRow label={t("shell.memoryGroupSessions")} /> : null}
								{live.map((row) => (
									<SessionRow
										chip={chipTone(state ?? "fine", facts.find((f) => f.id === row.id) ?? toSessionFacts(row.session, row.reading, Date.now()), largest)}
										isExpanded={expanded === row.id}
										key={row.id}
										maxBytes={maxBytes}
										onToggle={() => setExpanded((current) => (current === row.id ? undefined : row.id))}
										pauseOpen={pauseTarget === row.id}
										reading={row.reading}
										session={row.session}
										setPauseOpen={(next) => setPauseTarget(next ? row.id : undefined)}
									/>
								))}
								{paused.map((session) => (
									<PausedRow key={session.id} session={session} />
								))}
								{app?.own ? (
									<>
										<GroupRow label={t("shell.memoryGroupApp")} />
										<OwnRow
											isExpanded={expanded === "ao"}
											maxBytes={maxBytes}
											onToggle={() => setExpanded((current) => (current === "ao" ? undefined : "ao"))}
											reading={app.own}
										/>
									</>
								) : null}
							</tbody>
						</table>
					)}
					{system ? (
						<section className="flex w-full flex-col items-stretch gap-(--size-settings-section-inner-gap)">
							<h2 className="text-xs font-medium leading-4 text-settings-muted">{t("shell.memoryPressureGraph")}</h2>
							<div className="settings-grouped-rows flex w-full flex-col">
								<div className="settings-row-bar h-auto items-start gap-6 py-3">
									<PressureGraph history={history} source={system.pressureSource} />
									<dl className="grid shrink-0 grid-cols-[auto_auto] gap-x-4 gap-y-0.5 font-mono text-xs tabular-nums text-settings-muted">
										<dt>{t("shell.memoryMachineTotal")}</dt>
										<dd className="text-right text-settings-label">{formatMemory(system.totalBytes)}</dd>
										<dt>{t("shell.memoryMachineInUse")}</dt>
										<dd className="text-right text-settings-label">{formatMemory(system.totalBytes - system.availableBytes)}</dd>
										<dt>{t("shell.memoryMachineAO")}</dt>
										<dd className="text-right text-settings-label">{app ? formatMemory(app.rssBytes) : "—"}</dd>
									</dl>
								</div>
							</div>
						</section>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function GroupRow({ label }: { label: string }) {
	return (
		<tr>
			<td className="pb-2 pt-6 text-xs font-medium leading-4 text-settings-muted first:pt-0" colSpan={4}>{label}</td>
		</tr>
	);
}

/** Memory cell: the number over a bar scaled to the biggest row, so "which one is the pig" reads at a glance. */
function MemoryCell({ bytes, maxBytes, tone }: { bytes: number; maxBytes: number; tone: ChipTone }) {
	return (
		<td className="whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-xs tabular-nums">
			<span className={cn("font-medium", tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-settings-label")}>
				{formatMemory(bytes)}
			</span>
			<div className="ml-auto mt-1 h-0.5 w-24 rounded-sm bg-foreground/[0.06]" data-testid="session-memory-share">
				<div
					className={cn("h-full rounded-sm transition-[width] duration-500", tone === "critical" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-accent-strong")}
					style={{ width: `${Math.min(100, (bytes / maxBytes) * 100)}%` }}
				/>
			</div>
		</td>
	);
}

function SessionRow({
	chip,
	isExpanded,
	maxBytes,
	onToggle,
	pauseOpen,
	reading,
	session,
	setPauseOpen,
}: {
	chip: ChipTone;
	isExpanded: boolean;
	maxBytes: number;
	onToggle: () => void;
	pauseOpen: boolean;
	reading: SessionMemoryReading;
	session: WorkspaceSession;
	setPauseOpen: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const pause = useAgentPause(session, reading.rssBytes);
	const working = session.activity?.state === "active";
	const canExpand = reading.processes.length > 0;
	// Working: pause needs a decision (finish the turn, or stop now). Idle:
	// stop straight away. Either way the agent exits and its memory is freed;
	// the session, branch and conversation stay.
	const needsPolicy = working || pause.drainBlocked;
	const action = (
		<Button
			aria-label={working ? t("shell.pauseAgentNamed", { title: session.title }) : t("shell.stopAgentNamed", { title: session.title })}
			disabled={pause.isPending}
			onClick={() => (needsPolicy ? setPauseOpen(true) : pause.toggle())}
			size="sm"
			title={t("shell.stopAgentHelp")}
		>
			{pause.isPending ? (
				<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
			) : working ? (
				<Pause className="size-icon-sm" aria-hidden="true" />
			) : (
				<Square className="size-icon-sm" aria-hidden="true" />
			)}
			<span>{working ? t("shell.pauseAgent") : t("shell.stopAgentFrees", { size: formatMemory(reading.rssBytes) })}</span>
		</Button>
	);
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("border-t border-(--color-border-settings-dialog-header)", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-chip-tone={chip}
				data-testid="session-memory-row"
				onClick={canExpand ? onToggle : undefined}
			>
				<td className="max-w-0 px-4 py-2 align-middle">
					<div className="flex items-center gap-1.5">
						<ChevronRight
							aria-hidden="true"
							className={cn("size-icon-2xs shrink-0 text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", isExpanded && "rotate-90")}
						/>
						<div className="min-w-0">
							<div className="truncate text-sm font-medium text-settings-label" title={session.title}>{session.title}</div>
							<div className="truncate text-xs text-settings-muted">{working ? t("shell.memoryRowWorking") : t("shell.memoryRowIdle")}</div>
						</div>
					</div>
				</td>
				<MemoryCell bytes={reading.rssBytes} maxBytes={maxBytes} tone={chip} />
				<td className="w-16 whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-xs tabular-nums text-settings-muted">
					{working ? formatCPU(reading.cpuPercent) : "·"}
				</td>
				<td className="whitespace-nowrap px-2 py-2 text-right align-middle" onClick={(event) => event.stopPropagation()}>
					{canPauseAgent(session) ? (
						needsPolicy ? (
							<AgentPausePopover
								blocked={pause.drainBlocked}
								onChoose={(policy) => {
									setPauseOpen(false);
									pause.toggle(policy);
								}}
								onOpenChange={setPauseOpen}
								open={pauseOpen}
								session={session}
								trigger={action}
							/>
						) : (
							action
						)
					) : null}
				</td>
			</tr>
			{isExpanded ? <ProcessRows processes={reading.processes} /> : null}
		</>
	);
}

/** A paused agent holds nothing: listed apart, greyed, with the way back. */
function PausedRow({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const pause = useAgentPause(session);
	return (
		<tr className="border-t border-(--color-border-settings-dialog-header)" data-testid="session-memory-paused-row">
			<td className="max-w-0 px-4 py-2 align-middle">
				<div className="min-w-0 pl-[calc(var(--space-2)+0.75rem)] opacity-60">
					<div className="truncate text-sm font-medium text-settings-label" title={session.title}>{session.title}</div>
					<div className="truncate text-xs text-settings-muted">{t("shell.memoryRowPaused")}</div>
				</div>
			</td>
			<td className="px-4 py-2 text-right align-middle font-mono text-xs text-settings-muted">·</td>
			<td className="px-4 py-2 text-right align-middle font-mono text-xs text-settings-muted">·</td>
			<td className="whitespace-nowrap px-2 py-2 text-right align-middle">
				<Button
					aria-label={t("shell.resumeAgentNamed", { title: session.title })}
					disabled={pause.isPending}
					onClick={() => pause.toggle()}
					size="sm"
				>
					{pause.isPending ? <Loader2 className="size-icon-sm animate-spin" aria-hidden="true" /> : <Play className="size-icon-sm" aria-hidden="true" />}
					<span>{t("shell.resumeAgent")}</span>
				</Button>
			</td>
		</tr>
	);
}

/** AO's daemon and desktop shell: real cost, but not a session, so no action. */
function OwnRow({ isExpanded, maxBytes, onToggle, reading }: { isExpanded: boolean; maxBytes: number; onToggle: () => void; reading: SessionMemoryReading }) {
	const { t } = useTranslation();
	const canExpand = reading.processes.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("border-t border-(--color-border-settings-dialog-header)", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-testid="session-memory-own-row"
				onClick={canExpand ? onToggle : undefined}
			>
				<td className="max-w-0 px-4 py-2 align-middle">
					<div className="flex items-center gap-1.5">
						<ChevronRight
							aria-hidden="true"
							className={cn("size-icon-2xs shrink-0 text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", isExpanded && "rotate-90")}
						/>
						<div className="truncate text-sm font-medium text-settings-label">{t("shell.memoryOwnRow")}</div>
					</div>
				</td>
				<MemoryCell bytes={reading.rssBytes} maxBytes={maxBytes} tone="neutral" />
				<td className="w-16 whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-xs tabular-nums text-settings-muted">{formatCPU(reading.cpuPercent)}</td>
				<td className="w-28 px-2 py-2" />
			</tr>
			{isExpanded ? <ProcessRows processes={reading.processes} /> : null}
		</>
	);
}

/** btop's tree: the processes under a row, largest first, drawn as children. */
function ProcessRows({ processes }: { processes: SessionMemoryReading["processes"] }) {
	const sorted = [...processes].sort((a, b) => b.rssBytes - a.rssBytes);
	return (
		<>
			{sorted.map((process, index) => (
				<tr className="text-xs" data-testid="session-memory-process-row" key={process.pid}>
					<td className="max-w-0 py-1 pl-11 pr-4 font-mono text-settings-muted">
						<span aria-hidden="true" className="text-passive">{index === sorted.length - 1 ? "└─ " : "├─ "}</span>
						<span className="truncate" title={`${process.command} (${process.pid})`}>{process.command || "?"}</span>
					</td>
					<td className="whitespace-nowrap px-4 py-1 text-right font-mono tabular-nums text-settings-muted">{formatMemory(process.rssBytes)}</td>
					<td className="whitespace-nowrap px-4 py-1 text-right font-mono tabular-nums text-passive">
						{process.cpuPercent >= 1 ? formatCPU(process.cpuPercent) : "·"}
					</td>
					<td />
				</tr>
			))}
		</>
	);
}
