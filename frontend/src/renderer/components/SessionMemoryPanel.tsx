import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, X } from "lucide-react";
import {
	chipTone,
	largestSession,
	pressureState,
	resourceSuggestion,
	stableResourceOrder,
	type ChipTone,
	type PressureState,
	type ResourceSessionFacts,
	type ResourceSuggestion,
} from "@aoagents/product-ui";
import { cn } from "@/lib/utils";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	formatCPU,
	formatMemory,
	useAppMemory,
	useFastMemorySampling,
	useSessionMemory,
	type SessionMemoryReading,
	type SystemMemoryReading,
} from "../hooks/useSessionMemory";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
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

/** The single fix the monitor offers. */
function useSuggestion(projectId?: string) {
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
	return { state, suggestion, facts };
}

/** Bar phrase after AO's size: only the "not AO" case; the largest session is named in the window. */
function suggestionLabel(suggestion: ResourceSuggestion, t: TFunction): string | undefined {
	return suggestion.kind === "other_apps" ? t("shell.memoryFixNotAO") : undefined;
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
	const fix = suggestionLabel(suggestion, t);
	const word = state ? t(`shell.memoryState.${state}`) : t("shell.memoryState.unknown");
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
						aria-label={`${word} · ${detail}`}
						className="inline-flex items-center gap-2 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-state={state ?? "unknown"}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", state ? stateDot[state] : "bg-passive")} />
						<span className={state ? stateText[state] : undefined}>{formatMemory(app.rssBytes)}</span>
						{fix ? <span className={state ? stateText[state] : undefined}>· {fix}</span> : null}
					</button>
				</TooltipTrigger>
				<TooltipContent side="top">{detail}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}

/** One bar: AO against free, and nothing else. The bar is scaled to the two
 * of them, so there is no gap standing in for other apps. */
function MachineBar({ appBytes, system }: { appBytes: number; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const total = appBytes + system.availableBytes || 1;
	const pct = (bytes: number) => `${Math.min(100, (bytes / total) * 100)}%`;
	const legend = [
		{ key: "ao", label: t("shell.memoryLegendAO"), bytes: appBytes, className: "bg-accent-strong" },
		{ key: "free", label: t("shell.memoryLegendAvailable"), bytes: system.availableBytes, className: "bg-success/70" },
	];
	return (
		<div className="settings-row-bar h-auto flex-col items-stretch gap-2 py-3" data-testid="session-memory-stacked">
			<div className="flex h-2 w-full overflow-hidden rounded-sm bg-foreground/[0.06]">
				<div className="h-full bg-accent-strong transition-[width] duration-500" style={{ width: pct(appBytes) }} />
				<div className="h-full bg-success/70 transition-[width] duration-500" style={{ width: pct(system.availableBytes) }} />
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

/** The lone line under the bar: what is going on. It only informs; nothing here ends a session. */
function SuggestionLine({ state, suggestion }: { state: PressureState; suggestion: ResourceSuggestion }) {
	const { t } = useTranslation();
	if (suggestion.kind === "none") return null;
	const text =
		suggestion.kind === "other_apps"
			? t("shell.memorySuggestOtherApps", { size: formatMemory(suggestion.aoBytes) })
			: t("shell.memorySuggestLargest", { title: suggestion.title });
	return (
		<div className="settings-row-bar gap-3 text-sm" data-testid="session-memory-suggestion">
			<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", stateDot[state])} />
			<span className={cn("min-w-0 flex-1 truncate font-medium", stateText[state] || "text-settings-label")}>{text}</span>
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
	const { state, suggestion, facts } = useSuggestion(projectId);
	// Orchestrators are listed too: they hold memory like any session, and
	// leaving them out made the rows add up to less than AO's total.
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workspace.sessions)
				.filter((session) => session.isTerminated !== true),
		[workspaces, projectId],
	);
	// Rows with a reading are the live ones; a session without a process tree
	// is not listed, never shown as 0 MB.
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
	const largest = largestSession(facts);
	const maxBytes = Math.max(app?.own?.rssBytes ?? 0, ...live.map((row) => row.rssBytes), 1);
	// Any number of rows open at once: comparing two trees is the point.
	const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
	const toggleExpanded = (id: string) =>
		setExpanded((current) => {
			const next = new Set(current);
			if (!next.delete(id)) next.add(id);
			return next;
		});
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
							{system && app ? <MachineBar appBytes={app.rssBytes} system={system} /> : null}
							{state ? (
								<SuggestionLine state={state} suggestion={suggestion} />
							) : null}
						</div>
					</section>
					{live.length === 0 && !app?.own ? (
						<p className="py-6 text-center text-xs text-settings-muted">{t("shell.memoryEmpty")}</p>
					) : (
						<table className="w-full border-collapse text-xs" data-testid="session-memory-table">
							<tbody>
								{live.length > 0 ? <GroupRow label={t("shell.memoryGroupSessions")} /> : null}
								{live.map((row) => (
									<SessionRow
										chip={chipTone(state ?? "fine", facts.find((f) => f.id === row.id) ?? toSessionFacts(row.session, row.reading, Date.now()), largest)}
										isExpanded={expanded.has(row.id)}
										key={row.id}
										maxBytes={maxBytes}
										onToggle={() => toggleExpanded(row.id)}
										reading={row.reading}
										session={row.session}
									/>
								))}
								{app?.own ? (
									<>
										<GroupRow label={t("shell.memoryGroupApp")} />
										<OwnRow
											isExpanded={expanded.has("ao")}
											maxBytes={maxBytes}
											onToggle={() => toggleExpanded("ao")}
											reading={app.own}
										/>
									</>
								) : null}
							</tbody>
						</table>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

function GroupRow({ label }: { label: string }) {
	return (
		<tr>
			<td className="pb-2 pt-6 text-xs font-medium leading-4 text-settings-muted first:pt-0" colSpan={3}>{label}</td>
		</tr>
	);
}

/** Memory cell: the number over a bar scaled to the biggest row, so "which one is the pig" reads at a glance. */
function MemoryCell({ bytes, maxBytes, tone }: { bytes: number; maxBytes: number; tone: ChipTone }) {
	return (
		<td className="whitespace-nowrap px-3 py-2 text-right align-middle font-mono text-xs tabular-nums">
			<span className={cn("font-medium", tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-settings-label")}>
				{formatMemory(bytes)}
			</span>
			<div className="ml-auto mt-1 h-0.5 w-16 rounded-sm bg-foreground/[0.06]" data-testid="session-memory-share">
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
	reading,
	session,
}: {
	chip: ChipTone;
	isExpanded: boolean;
	maxBytes: number;
	onToggle: () => void;
	reading: SessionMemoryReading;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const working = session.activity?.state === "active";
	const canExpand = reading.processes.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("border-t border-(--color-border-settings-dialog-header)", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-chip-tone={chip}
				data-testid="session-memory-row"
				onClick={canExpand ? onToggle : undefined}
			>
				<td className="w-full max-w-0 px-4 py-2 align-middle">
					<div className="flex items-center gap-1.5">
						<ChevronRight
							aria-hidden="true"
							className={cn("size-icon-2xs shrink-0 text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", isExpanded && "rotate-90")}
						/>
						<div className="min-w-0">
							<div className="truncate text-sm font-medium text-settings-label" title={session.title}>{session.title}</div>
							<div className="truncate text-xs text-settings-muted">
								{isOrchestratorSession(session) ? t("shell.memoryRowOrchestrator") : working ? t("shell.memoryRowWorking") : t("shell.memoryRowIdle")}
							</div>
						</div>
					</div>
				</td>
				<MemoryCell bytes={reading.rssBytes} maxBytes={maxBytes} tone={chip} />
				<td className="w-16 whitespace-nowrap px-4 py-2 text-right align-middle font-mono text-xs tabular-nums text-settings-muted">
					{working ? formatCPU(reading.cpuPercent) : "·"}
				</td>
			</tr>
			{isExpanded ? <ProcessRows processes={reading.processes} /> : null}
		</>
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
				</tr>
			))}
		</>
	);
}
