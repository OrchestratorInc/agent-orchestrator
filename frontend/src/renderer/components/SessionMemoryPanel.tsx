import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Check, ChevronDown, ChevronRight, ChevronUp, Copy, Loader2 } from "@/components/app-icons";
import {
	chipTone,
	largestSession,
	processCategories,
	processKind,
	processList,
	stableResourceOrder,
	type ChipTone,
	type PressureState,
	type ProcessRow as ProcessListRow,
	type ResourceSessionFacts,
} from "@aoagents/product-ui";
import { cn } from "@/lib/utils";
import { aoBridge } from "../lib/bridge";
import { formatEstimatedCost } from "../lib/format-cost";
import { formatTimeTerse } from "../lib/format-time";
import { formatTokenCount } from "../lib/format-token-count";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	formatCPU,
	formatMemory,
	useAppMemory,
	useFastMemorySampling,
	usePressureState,
	sampleHistoryLength,
	cpuSampleOf,
	useCPUHistory,
	useSessionMemory,
	type CPUSample,
	type AppMemoryReading,
	type ReviewerMemoryReading,
	type SessionMemoryReading,
	type SessionStepReading,
	type SystemMemoryReading,
} from "../hooks/useSessionMemory";
import { useSessionUsageSummaries, type SessionUsageSummary } from "../hooks/useSessionUsageSummaries";
import { isOrchestratorSession, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Pressable } from "./ui/pressable";
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
import { SettingsSection } from "./settings/SettingsSection";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const EMPTY_WORKSPACES: readonly WorkspaceSummary[] = [];

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

function usePressureFacts(projectId?: string) {
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const readings = useSessionMemory().data;
	const now = Date.now();
	const facts = workspaces
		.filter((workspace) => !projectId || workspace.id === projectId)
		.flatMap((workspace) => workspace.sessions)
		.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session) && readings?.has(session.id))
		.map((session) => toSessionFacts(session, readings?.get(session.id), now));
	const state = usePressureState();
	return { state, facts };
}

export function AppMemoryIndicator() {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const memory = useAppMemory();
	const { state } = usePressureFacts();
	const app = memory.data?.app;
	const system = memory.data?.system;
	if (memory.isError || !app || app.rssBytes === 0) {
		return null;
	}
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
					<Pressable
						aria-label={`${word} · ${detail}`}
						className="inline-flex items-center gap-2 px-2 font-mono text-2xs tabular-nums text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:underline"
						data-memory-state={state ?? "unknown"}
						data-testid="app-memory-indicator"
						onClick={() => setOpen(true)}
						type="button"
					>
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", state ? stateDot[state] : "bg-passive")} />
						<span className={state ? stateText[state] : undefined}>{formatMemory(app.rssBytes)}</span>
					</Pressable>
				</TooltipTrigger>
				<TooltipContent side="bottom">{detail}</TooltipContent>
			</Tooltip>
			{open ? <SessionMemoryPanel onOpenChange={setOpen} open /> : null}
		</>
	);
}

function MetricCard({ children, testId, title, value }: { children: ReactNode; testId: string; title: string; value: string }) {
	return (
		<div className="settings-grouped-rows" data-testid={testId}>
			<div className="settings-content-row flex flex-col gap-3">
				<div className="flex items-baseline justify-between gap-3">
					<h3 className="min-w-0 truncate text-xs font-medium text-settings-muted">{title}</h3>
					<span className="shrink-0 text-subtitle font-medium tabular-nums text-settings-label">{value}</span>
				</div>
				{children}
			</div>
		</div>
	);
}

function LegendItem({ dot, label, value }: { dot: string; label: string; value: string }) {
	return (
		<span className="inline-flex items-center gap-1.5">
			<span aria-hidden="true" className={cn("size-1.5 rounded-full", dot)} />
			{label} <span className="text-settings-label">{value}</span>
		</span>
	);
}

const aoFill = "bg-passive dark:bg-accent-strong";

function MemoryBar({ app, system }: { app: AppMemoryReading; system: SystemMemoryReading }) {
	const { t } = useTranslation();
	const total = app.rssBytes + system.availableBytes || 1;
	const pct = (bytes: number) => `${Math.min(100, (bytes / total) * 100)}%`;
	return (
		<div className="settings-grouped-rows" data-testid="session-memory-stacked">
			<div className="settings-content-row flex flex-col gap-3">
				<div className="flex h-1.5 w-full overflow-hidden rounded-full bg-foreground/[0.06]">
					<div className={cn("h-full transition-[width] duration-500", aoFill)} style={{ width: pct(app.rssBytes) }} />
					<div className="h-full bg-success/70 transition-[width] duration-500" style={{ width: pct(system.availableBytes) }} />
				</div>
				<div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs tabular-nums text-settings-muted">
					<LegendItem dot={aoFill} label={t("shell.memoryLegendAO")} value={formatMemory(app.rssBytes)} />
					<LegendItem dot="bg-success/70" label={t("shell.memoryLegendAvailable")} value={formatMemory(system.availableBytes)} />
				</div>
			</div>
		</div>
	);
}

export function MachineSection({ titleAction }: { titleAction?: ReactNode }) {
	const { t } = useTranslation();
	const appMemory = useAppMemory().data;
	const app = appMemory?.app;
	const system = appMemory?.system;
	return (
		<SettingsSection title={t("shell.memorySectionMachine")} titleAction={titleAction}>
			<div className="flex flex-col">
				{system && app ? (
					<MemoryBar app={app} system={system} />
				) : (
					<div className="settings-grouped-rows">
						<p aria-busy="true" className="settings-row-bar text-sm text-settings-muted" data-testid="session-memory-loading">
							<span className="inline-flex items-center gap-2">
								<Loader2 aria-hidden="true" className="size-icon-sm animate-spin" />
								{t("common.loading")}
							</span>
						</p>
					</div>
				)}
			</div>
		</SettingsSection>
	);
}

const columnWidth = { type: "w-28", pid: "w-20", cpu: "w-20", memory: "w-24", share: "w-24" } as const;
const cell = {
	name: "pl-(--settings-row-inset) pr-3 text-left",
	process: "pl-13 pr-3 text-left",
	type: "px-3 text-left",
	pid: "px-3 text-left",
	cpu: "px-3 text-left",
	memory: "px-3 text-left",
	share: "pl-3 pr-(--settings-row-inset) text-left",
} as const;

const topLine = "py-2.5 align-top leading-5";

const columnCount = 6;

function cpuOrder<T extends { id: string; session: WorkspaceSession; reading: SessionMemoryReading }>(rows: T[], ascending: boolean): T[] {
	const direction = ascending ? 1 : -1;
	return [...rows].sort(
		(a, b) =>
			direction * (a.reading.cpuPercent - b.reading.cpuPercent) ||
			a.session.title.localeCompare(b.session.title) ||
			a.id.localeCompare(b.id),
	);
}

function SortHeader({ active, ascending, className, label, onSort, sortable }: { active: boolean; ascending: boolean; className: string; label: string; onSort: () => void; sortable: boolean }) {
	if (!sortable) {
		return <th className={cn("pb-2 pt-3 font-medium", className)} scope="col">{label}</th>;
	}
	const Arrow = active && ascending ? ChevronUp : ChevronDown;
	return (
		<th aria-sort={active ? (ascending ? "ascending" : "descending") : undefined} className={cn("pb-2 pt-3 font-medium", className)} scope="col">
			<Pressable
				className={cn(
					"inline-flex items-center gap-1 rounded-sm transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
					active && "text-settings-label",
				)}
				data-testid="session-memory-sort"
				onClick={onSort}
				type="button"
			>
				{label}
				<Arrow aria-hidden="true" className={cn("size-icon-2xs", active ? "opacity-100" : "opacity-50")} />
			</Pressable>
		</th>
	);
}

export function SessionsTable({ onRows, pinned, projectId, underToolbar }: { onRows?: (rows: ReportRow[]) => void; pinned?: boolean; projectId?: string; underToolbar?: boolean }) {
	const { t } = useTranslation();
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const readings = useSessionMemory(projectId).data;
	const app = useAppMemory().data?.app;
	const usage = useSessionUsageSummaries(projectId).data;
	const { state, facts } = usePressureFacts(projectId);
	const sessions = useMemo(
		() =>
			workspaces
				.filter((workspace) => !projectId || workspace.id === projectId)
				.flatMap((workspace) => workspace.sessions)
				.filter((session) => session.isTerminated !== true),
		[workspaces, projectId],
	);
	const orderRef = useRef<string[]>([]);
	const [sort, setSort] = useState<{ by: "memory" | "cpu"; ascending: boolean }>({ by: "memory", ascending: false });
	const sortBy = (by: "memory" | "cpu") => setSort((current) => ({ by, ascending: current.by === by ? !current.ascending : false }));
	const live = useMemo(() => {
		const rows = sessions
			.map((session) => ({ id: session.id, session, reading: readings?.get(session.id) }))
			.filter((row): row is { id: string; session: WorkspaceSession; reading: SessionMemoryReading } => row.reading !== undefined)
			.map((row) => ({ ...row, rssBytes: row.reading.rssBytes }));
		if (sort.by === "cpu") return cpuOrder(rows, sort.ascending);
		const largestFirst = stableResourceOrder(orderRef.current, rows);
		orderRef.current = largestFirst.map((row) => row.id);
		return sort.ascending ? [...largestFirst].reverse() : largestFirst;
	}, [sessions, readings, sort]);
	const largest = largestSession(facts);
	const reviewers = useMemo(() => [...(app?.reviewers ?? [])].sort((a, b) => b.memory.rssBytes - a.memory.rssBytes), [app?.reviewers]);
	const titles = useMemo(() => sessionTitles(workspaces), [workspaces]);
	const maxBytes = Math.max(app?.own?.rssBytes ?? 0, ...live.map((row) => row.rssBytes), ...reviewers.map((r) => r.memory.rssBytes), 1);
	useEffect(() => {
		onRows?.(live.map(({ session, reading }) => ({ session, reading, usage: usage?.get(session.id) })));
	}, [live, usage, onRows]);
	const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
	const toggleExpanded = (id: string) =>
		setExpanded((current) => {
			const next = new Set(current);
			if (!next.delete(id)) next.add(id);
			return next;
		});
	if (live.length === 0 && reviewers.length === 0 && !app?.own) {
		return (
			<SettingsSection title={t("shell.memoryGroupSessions")} grouped>
				<p className="settings-row-bar text-sm text-settings-muted">{t("shell.memoryEmpty")}</p>
			</SettingsSection>
		);
	}
	return (
		<SettingsSection title={t("shell.memoryGroupSessions")}>
		<div className="settings-grouped-rows overflow-clip!">
		<table className="w-full table-fixed border-collapse text-xs" data-testid="session-memory-table">
			<colgroup>
				<col />
				<col className={columnWidth.type} />
				<col className={columnWidth.pid} />
				<col className={columnWidth.cpu} />
				<col className={columnWidth.memory} />
				<col className={columnWidth.share} />
			</colgroup>
			<thead className={cn("sticky z-10", pinned && "bg-popover", underToolbar ? "top-[calc(var(--spacing-toolbar)+--spacing(8))]" : "top-8")}>
				<tr className="border-b border-(--color-border-settings-dialog-header) text-xs text-settings-muted">
					<th className={cn("pb-2 pt-3 font-medium", cell.name)} scope="col">{t("shell.memoryColumnName")}</th>
					<th className={cn("pb-2 pt-3 font-medium", cell.type)} scope="col">{t("shell.memoryColumnType")}</th>
					<th className={cn("pb-2 pt-3 font-medium", cell.pid)} scope="col">{t("shell.memoryColumnPid")}</th>
					<SortHeader active={sort.by === "cpu"} ascending={sort.ascending} className={cell.cpu} label={t("shell.memoryColumnCpu")} onSort={() => sortBy("cpu")} sortable={live.length > 1} />
					<SortHeader active={sort.by === "memory"} ascending={sort.ascending} className={cell.memory} label={t("shell.memoryColumnRss")} onSort={() => sortBy("memory")} sortable={live.length > 1} />
					<th className={cn("pb-2 pt-3 font-medium", cell.share)} scope="col">{t("shell.memoryColumnShare")}</th>
				</tr>
			</thead>
			<tbody className="[&_tr+tr.memory-group]:border-t [&_tr:not(.memory-group)+tr.memory-row]:border-t [&_tr.memory-group]:border-(--color-border-settings-dialog-header) [&_tr.memory-row]:border-(--color-border-settings-dialog-header)">
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
				{reviewers.length > 0 ? <GroupRow label={t("shell.memoryGroupReviewers")} /> : null}
				{reviewers.map((reviewer) => (
					<FixedRow
						isExpanded={expanded.has(`review:${reviewer.reviewId}`)}
						key={reviewer.reviewId}
						label={reviewerLabel(reviewer, titles, t)}
						maxBytes={maxBytes}
						onToggle={() => toggleExpanded(`review:${reviewer.reviewId}`)}
						reading={reviewer.memory}
						subtitle={reviewer.harness}
						testId="session-memory-reviewer-row"
						type={t("shell.memoryCategory.reviewer")}
					/>
				))}
				{app?.own ? (
					<>
						<GroupRow label={t("shell.memoryGroupApp")} />
						<FixedRow
							isExpanded={expanded.has("ao")}
							label={t("shell.memoryOwnRow")}
							maxBytes={maxBytes}
							onToggle={() => toggleExpanded("ao")}
							own
							reading={app.own}
							testId="session-memory-own-row"
							type={t("shell.memoryCategory.ao")}
						/>
					</>
				) : null}
			</tbody>
		</table>
		</div>
		</SettingsSection>
	);
}

export function CpuSection() {
	const { t } = useTranslation();
	const appMemory = useAppMemory().data;
	const system = appMemory?.system;
	const cpuHistory = useCPUHistory();
	const current = cpuSampleOf(system, appMemory?.app) ?? cpuHistory.at(-1);
	if (!system || !current) return null;
	return (
		<SettingsSection
			title={t("shell.cpuBarTitle", { cores: system.cpuCount })}
			testId="session-cpu-graph"
			action={system.load1 >= 0 ? (
				<span className="text-xs tabular-nums text-settings-muted">{t("shell.cpuBarLoad", { load: system.load1.toFixed(2) })}</span>
			) : undefined}
		>
			<CpuGraphs current={current} history={cpuHistory} />
		</SettingsSection>
	);
}

export function DiagnosticsBody({ projectId, scroller, underToolbar }: { projectId?: string; scroller?: RefObject<HTMLElement | null>; underToolbar?: boolean }) {
	const { t } = useTranslation();
	const appMemory = useAppMemory().data;
	const workspaces = useWorkspaceQuery().data ?? EMPTY_WORKSPACES;
	const [rows, setRows] = useState<ReportRow[]>([]);
	const graphs = useRef<HTMLDivElement>(null);
	const scrolledPast = useScrolledPast(graphs, scroller);
	return (
		<>
			<div className="flex flex-col gap-(--size-settings-section-gap,1.5rem)" ref={graphs}>
				<MachineSection
					titleAction={<CopyReportButton value={() => diagnosticsReport({ app: appMemory?.app, system: appMemory?.system }, rows, t, sessionTitles(workspaces))} />}
				/>
				<CpuSection />
			</div>
			<div className="flex w-full flex-col">
			<div className={cn("sticky z-20 h-0 overflow-visible", underToolbar ? "top-toolbar" : "top-0")}>
				<div
					aria-hidden={!scrolledPast}
					className={cn(
						"absolute inset-x-0 top-0 flex h-8 items-center gap-4 border-b border-(--color-border-settings-dialog-header) px-(--settings-row-inset) text-xs tabular-nums text-settings-muted transition-opacity",
						underToolbar ? "bg-(--color-bg-primary)" : "bg-popover",
						scrolledPast ? "opacity-100" : "pointer-events-none opacity-0",
					)}
					data-testid="session-memory-pinned"
				>
					{appMemory?.app ? (
						<span>
							{t("shell.memoryLegendAO")} <span className="text-settings-label">{formatMemory(appMemory.app.rssBytes)}</span>
							{appMemory.system ? ` · ${formatMemory(appMemory.system.availableBytes)} ${t("shell.memoryLegendAvailable").toLowerCase()}` : null}
						</span>
					) : null}
					{appMemory?.system ? (
						<span>
							{t("shell.memoryColumnCpu")} <span className="text-settings-label">{formatCPU(appMemory.system.cpuPercent)}</span>
						</span>
					) : null}
				</div>
			</div>
			<SessionsTable onRows={setRows} pinned={scrolledPast} projectId={projectId} underToolbar={underToolbar} />
			</div>
		</>
	);
}

function useScrolledPast(target: RefObject<HTMLElement | null>, scroller?: RefObject<HTMLElement | null>): boolean {
	const [past, setPast] = useState(false);
	useEffect(() => {
		const element = target.current;
		if (!element || typeof IntersectionObserver !== "function") return;
		const observer = new IntersectionObserver(([entry]) => setPast(!entry.isIntersecting), {
			root: scroller?.current ?? null,
			threshold: 0,
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [target, scroller]);
	return past;
}

export function MemoryDiagnostics() {
	useFastMemorySampling();
	return (
		<div className="settings-dialog-body flex w-full flex-col gap-(--size-settings-section-gap,1.5rem)" data-testid="memory-diagnostics">
			<DiagnosticsBody underToolbar />
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
	const scroller = useRef<HTMLDivElement>(null);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={cn(settingsDialogContentClass, "w-[min(52rem,calc(100vw-var(--space-8)))]")} showCloseButton={false}>
				<div className={cn(settingsDialogHeaderClass, "flex h-auto flex-row items-center justify-between border-b-0 pb-3")}>
					<div className="min-w-0 flex-1">
						<DialogTitle className="text-lg font-semibold leading-6 text-settings-label">{t("shell.memoryPanelTitle")}</DialogTitle>
						<DialogDescription className="sr-only">{t("shell.memoryPanelDescription")}</DialogDescription>
					</div>
					<DialogClose asChild>
						<CloseButton aria-label={t("common.close")} />
					</DialogClose>
				</div>
				<div
					className={cn(settingsDialogBodyClass, "settings-dialog-body min-h-0 flex-1 gap-(--size-settings-section-gap,1.5rem) overscroll-contain px-(--size-modal-padding) pt-0")}
					ref={scroller}
				>
					<DiagnosticsBody projectId={projectId} scroller={scroller} />
				</div>
			</DialogContent>
		</Dialog>
	);
}

function GroupRow({ label }: { label: string }) {
	return (
		<tr className="memory-group">
			<td className={cn("pb-1 pt-4 text-xs font-medium leading-4 text-settings-muted in-[tr:first-child]:pt-3", cell.name)} colSpan={columnCount}>{label}</td>
		</tr>
	);
}

function RowTitle({ canExpand, children, isExpanded, title }: { canExpand: boolean; children?: ReactNode; isExpanded: boolean; title: string }) {
	return (
		<td className={cn("py-2.5 align-top", cell.name)}>
			<div className="flex gap-1.5">
				<span className="flex h-5 shrink-0 items-center">
					<ChevronRight
						aria-hidden="true"
						className={cn("size-icon-2xs text-passive transition-transform", canExpand ? "opacity-100" : "opacity-0", isExpanded && "rotate-90")}
					/>
				</span>
				<div className="min-w-0">
					<div className="truncate text-sm font-medium leading-5 text-settings-label" title={title}>{title}</div>
					{children}
				</div>
			</div>
		</td>
	);
}

function MemoryCell({ bytes, tone }: { bytes: number; tone: ChipTone }) {
	return (
		<td
			className={cn(
				"whitespace-nowrap text-xs font-medium tabular-nums",
				topLine,
				tone === "critical" ? "text-destructive" : tone === "warning" ? "text-warning" : "text-settings-label",
				cell.memory,
			)}
		>
			{formatMemory(bytes)}
		</td>
	);
}

function ShareCell({ bytes, maxBytes, tone }: { bytes: number; maxBytes: number; tone?: ChipTone }) {
	return (
		<td className={cn(tone ? topLine : "py-1 align-middle", cell.share)}>
			<div className={cn("flex items-center", tone ? "h-5" : "h-4")}>
				<div className="h-1 w-full overflow-hidden rounded-full bg-foreground/[0.06]" data-testid={tone ? "session-memory-share" : undefined}>
					<div
						className={cn(
							"h-full rounded-full transition-[width] duration-500",
							!tone ? "bg-passive" : tone === "critical" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-accent-strong",
						)}
						style={{ width: `${Math.min(100, (bytes / maxBytes) * 100)}%` }}
					/>
				</div>
			</div>
		</td>
	);
}

function toggleOnKeyDown(onToggle: () => void) {
	return (event: KeyboardEvent<HTMLTableRowElement>) => {
		if (event.key === "Enter" || event.key === " ") {
			event.preventDefault();
			onToggle();
		}
	};
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
	const recent = reading.activity?.recent ?? [];
	const canExpand = reading.processes.length > 0 || recent.length > 0;
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("group/row memory-row", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-chip-tone={chip}
				data-testid="session-memory-row"
				onClick={canExpand ? onToggle : undefined}
				onKeyDown={canExpand ? toggleOnKeyDown(onToggle) : undefined}
				tabIndex={canExpand ? 0 : undefined}
			>
				<RowTitle canExpand={canExpand} isExpanded={isExpanded} title={session.title}>
					<StatusLine current={reading.activity?.current} processes={reading.processes.length} session={session} working={working} />
				</RowTitle>
				<td className={cn("whitespace-nowrap text-xs text-passive", topLine, cell.type)} data-testid="session-memory-type">
					{isOrchestratorSession(session) ? t("settings.models.orchestratorRole") : t("settings.models.workerRole")}
				</td>
				<td className={cell.pid} />
				<td className={cn("whitespace-nowrap text-xs tabular-nums text-settings-muted", topLine, cell.cpu)}>
					{formatCPU(reading.cpuPercent)}
				</td>
				<MemoryCell bytes={reading.rssBytes} tone={chip} />
				<ShareCell bytes={reading.rssBytes} maxBytes={maxBytes} tone={chip} />
			</tr>
			{isExpanded ? <ProcessRows maxBytes={maxBytes} processes={reading.processes} /> : null}
			{isExpanded && recent.length > 0 ? <RecentSteps steps={recent} /> : null}
			{isExpanded ? <SpacerRow /> : null}
		</>
	);
}

export function formatDuration(ms: number): string {
	const total = Math.max(0, Math.round(ms / 1000));
	if (total < 60) return `${total}s`;
	const minutes = Math.floor(total / 60);
	if (minutes < 60) return `${minutes}m ${total % 60}s`;
	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

function statusText(current: SessionStepReading | undefined, session: WorkspaceSession, working: boolean, t: TFunction): string {
	if (isOrchestratorSession(session) && !current) return t("shell.memoryRowOrchestrator");
	if (current) return `${current.tool} · ${formatDuration(Date.now() - Date.parse(current.startedAt))}`;
	if (working) return t("shell.memoryRowWorking");
	const since = formatTimeTerse(session.activity?.lastActivityAt);
	return since === "now" ? t("shell.memoryRowIdle") : t("shell.memoryRowIdleFor", { time: since });
}

function StatusLine({ current, processes, session, working }: { current?: SessionStepReading; processes: number; session: WorkspaceSession; working: boolean }) {
	const { t } = useTranslation();
	return (
		<div className="truncate text-xs text-settings-muted">
			{session.provider ? <span data-testid="session-memory-agent">{session.provider} · </span> : null}
			<span data-testid="session-memory-status">{statusText(current, session, working, t)}</span>
			{processes > 0 ? <span data-testid="session-memory-process-count"> · {t("shell.memoryProcessCount", { count: processes })}</span> : null}
		</div>
	);
}

export function sessionReport(
	session: WorkspaceSession,
	reading: SessionMemoryReading,
	usage: SessionUsageSummary | undefined,
	t: TFunction,
): string {
	const working = session.activity?.state === "active";
	const identity = [session.workspaceName, session.kind ?? "worker", session.provider, session.mode].filter(Boolean).join(" · ");
	const lines = [session.title, identity];
	const lane = session.displayStatus ?? session.status;
	const doing = statusText(reading.activity?.current, session, working, t);
	lines.push(`Status   ${doing.toLowerCase().startsWith(lane.toLowerCase()) ? doing : `${lane} · ${doing}`}`);
	if (session.branch) lines.push(`Branch   ${session.branch}`);
	for (const pr of session.prs ?? []) {
		lines.push(`PR       #${pr.number} · ${pr.state}${pr.ci ? ` · CI ${pr.ci}` : ""}${pr.review ? ` · ${pr.review}` : ""}`);
		lines.push(`         ${pr.url}`);
	}
	const cost = formatEstimatedCost(usage?.estimatedCost);
	const tokens = usage?.processedTokens != null ? formatTokenCount(usage.processedTokens) : undefined;
	if (cost || tokens) lines.push(`Usage    ${[cost, tokens].filter(Boolean).join(" · ")}`);
	lines.push(`Memory   ${formatMemory(reading.rssBytes)} · CPU ${formatCPU(reading.cpuPercent)} · ${reading.sampledAt}`);

	const tree = processTree(reading.processes);
	if (tree.length > 0) {
		const names = tree.map(({ process, depth }) => `${"  ".repeat(depth)}${processKind(process.command)}`);
		const width = Math.max(...names.map((name) => name.length));
		lines.push("", t("shell.memoryGroupProcesses"));
		const categories = processCategories(reading.processes);
		tree.forEach(({ process }, i) => {
			const category = categories.get(process.pid);
			const type = category ? `  ${t(`shell.memoryCategory.${category}`)}` : "";
			lines.push(`  ${names[i].padEnd(width)}  ${formatMemory(process.rssBytes).padStart(8)}  ${formatCPU(process.cpuPercent).padStart(4)}${type}`);
		});
	}
	const recent = reading.activity?.recent ?? [];
	if (recent.length > 0) {
		lines.push("", t("shell.memoryRecent"));
		for (const step of recent) {
			const started = new Date(step.startedAt);
			const duration = step.endedAt ? formatDuration(Date.parse(step.endedAt) - started.getTime()) : "";
			lines.push(
				`  ${started.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}  ${step.tool.padEnd(12)} ${duration}${step.failed ? ` ${t("shell.memoryStepFailed")}` : ""}`.trimEnd(),
			);
		}
	}
	return `${lines.join("\n")}\n`;
}

function sessionTitles(workspaces: readonly WorkspaceSummary[]): Map<string, string> {
	return new Map(workspaces.flatMap((workspace) => workspace.sessions.map((session) => [session.id, session.title] as const)));
}

function reviewerLabel(reviewer: ReviewerMemoryReading, titles: Map<string, string>, t: TFunction): string {
	return t("shell.memoryReviewerRow", { session: titles.get(reviewer.sessionId) ?? reviewer.sessionId });
}

export type ReportRow = { session: WorkspaceSession; reading: SessionMemoryReading; usage?: SessionUsageSummary };

export function diagnosticsReport(
	machine: { app?: AppMemoryReading; system?: SystemMemoryReading },
	rows: ReportRow[],
	t: TFunction,
	titles: Map<string, string>,
): string {
	const { app, system } = machine;
	const lines = [t("shell.memorySectionMachine")];
	if (app && system) {
		lines.push(`Memory   Orchestrator ${formatMemory(app.rssBytes)} · ${formatMemory(system.availableBytes)} free of ${formatMemory(system.totalBytes)}`);
	} else if (app) {
		lines.push(`Memory   Orchestrator ${formatMemory(app.rssBytes)}`);
	}
	if (system) {
		const load = system.load1 >= 0 ? ` · load ${system.load1.toFixed(2)}` : "";
		lines.push(`CPU      ${formatCPU(system.cpuPercent)} of ${system.cpuCount} cores · Orchestrator ${formatCPU(Math.min(100, (app?.cpuPercent ?? 0) / Math.max(1, system.cpuCount)))}${load}`);
		if (system.swapBytesPerSec > 0) lines.push(`Swapping ${formatMemory(system.swapBytesPerSec)}/s`);
	}
	lines.push(`Sessions ${rows.length}`);
	return [`${lines.join("\n")}\n`, ...rows.map((row) => sessionReport(row.session, row.reading, row.usage, t)), ...aoReport(app, titles, t)].join("\n");
}

function aoReport(app: AppMemoryReading | undefined, titles: Map<string, string>, t: TFunction): string[] {
	if (!app) return [];
	const line = (label: string, reading: SessionMemoryReading) => `${label}\nMemory   ${formatMemory(reading.rssBytes)} · CPU ${formatCPU(reading.cpuPercent)}\n`;
	const reviewers = [...(app.reviewers ?? [])].sort((a, b) => b.memory.rssBytes - a.memory.rssBytes);
	return [
		...reviewers.map((reviewer) => line(`${reviewerLabel(reviewer, titles, t)} · ${reviewer.harness}`, reviewer.memory)),
		...(app.own ? [line(t("shell.memoryOwnRow"), app.own)] : []),
	];
}

function RecentSteps({ steps }: { steps: SessionStepReading[] }) {
	const { t } = useTranslation();
	return (
		<>
			<tr>
				<td className={cn("pb-1 pt-2 text-xs font-medium text-settings-muted", cell.process)} colSpan={columnCount}>{t("shell.memoryRecent")}</td>
			</tr>
			{steps.map((step) => {
				const started = new Date(step.startedAt);
				const duration = step.endedAt ? Date.parse(step.endedAt) - started.getTime() : undefined;
				return (
					<tr className="text-xs" data-testid="session-memory-step-row" key={`${step.startedAt}-${step.tool}`}>
						<td className={cn("py-1", cell.process)}>
							<div className="flex min-w-0 items-baseline gap-3">
								<span className="shrink-0 tabular-nums text-passive">{started.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
								<span className={cn("min-w-0 truncate font-mono", step.failed ? "text-error" : "text-settings-label")}>{step.tool}</span>
								{step.failed ? <span className="shrink-0 text-error">{t("shell.memoryStepFailed")}</span> : null}
							</div>
						</td>
						<td />
						<td />
						<td className={cn("whitespace-nowrap py-1 tabular-nums text-passive", cell.cpu)}>
							{duration !== undefined && duration >= 1000 ? formatDuration(duration) : "·"}
						</td>
						<td />
						<td />
					</tr>
				);
			})}
		</>
	);
}

function FixedRow({ isExpanded, label, maxBytes, onToggle, own = false, reading, subtitle, testId, type }: {
	isExpanded: boolean;
	label: string;
	maxBytes: number;
	onToggle: () => void;
	own?: boolean;
	reading: SessionMemoryReading;
	subtitle?: string;
	testId: string;
	type: string;
}) {
	const { t } = useTranslation();
	const canExpand = reading.processes.length > 0;
	const detail = [subtitle, canExpand ? t("shell.memoryProcessCount", { count: reading.processes.length }) : undefined].filter(Boolean).join(" · ");
	return (
		<>
			<tr
				aria-expanded={canExpand ? isExpanded : undefined}
				className={cn("memory-row", canExpand && "cursor-pointer hover:bg-interactive-hover")}
				data-testid={testId}
				onClick={canExpand ? onToggle : undefined}
				onKeyDown={canExpand ? toggleOnKeyDown(onToggle) : undefined}
				tabIndex={canExpand ? 0 : undefined}
			>
				<RowTitle canExpand={canExpand} isExpanded={isExpanded} title={label}>
					{detail ? <div className="truncate text-xs text-settings-muted">{detail}</div> : null}
				</RowTitle>
				<td className={cn("whitespace-nowrap text-xs text-passive", topLine, cell.type)} data-testid="session-memory-type">
					{type}
				</td>
				<td className={cell.pid} />
				<td className={cn("whitespace-nowrap text-xs tabular-nums text-settings-muted", topLine, cell.cpu)}>{formatCPU(reading.cpuPercent)}</td>
				<MemoryCell bytes={reading.rssBytes} tone="neutral" />
				<ShareCell bytes={reading.rssBytes} maxBytes={maxBytes} tone="neutral" />
			</tr>
			{isExpanded ? <ProcessRows maxBytes={maxBytes} own={own} processes={reading.processes} /> : null}
			{isExpanded ? <SpacerRow /> : null}
		</>
	);
}

function SpacerRow() {
	return (
		<tr aria-hidden="true">
			<td className="h-2 p-0" colSpan={columnCount} />
		</tr>
	);
}

export { processKind };

export function processTree(processes: SessionMemoryReading["processes"]): { process: SessionMemoryReading["processes"][number]; depth: number; last: boolean }[] {
	const pids = new Set(processes.map((p) => p.pid));
	const children = new Map<number, SessionMemoryReading["processes"]>();
	for (const p of processes) {
		const parent = pids.has(p.ppid) && p.ppid !== p.pid ? p.ppid : -1;
		children.set(parent, [...(children.get(parent) ?? []), p]);
	}
	const rows: { process: SessionMemoryReading["processes"][number]; depth: number; last: boolean }[] = [];
	const walk = (parent: number, depth: number) => {
		const kids = [...(children.get(parent) ?? [])].sort((a, b) => b.rssBytes - a.rssBytes);
		kids.forEach((kid, index) => {
			rows.push({ process: kid, depth, last: index === kids.length - 1 });
			walk(kid.pid, depth + 1);
		});
	};
	walk(-1, 0);
	return rows;
}

function ProcessRows({ maxBytes, processes, own = false }: { maxBytes: number; processes: SessionMemoryReading["processes"]; own?: boolean }) {
	const { t } = useTranslation();
	const { rows, other } = useMemo(() => processList(processes, { own }), [processes, own]);
	const [showAll, setShowAll] = useState(false);
	const toggle = () => setShowAll((current) => !current);
	return (
		<>
			{[...rows, ...(showAll && other ? other.rows : [])].map((row) => (
				<ProcessRow key={row.pid} maxBytes={maxBytes} row={row} />
			))}
			{other ? (
				<tr
					aria-expanded={showAll}
					className="cursor-pointer text-xs hover:bg-interactive-hover"
					data-testid="session-memory-process-other"
					onClick={toggle}
					onKeyDown={toggleOnKeyDown(toggle)}
					tabIndex={0}
				>
					<td className={cn("py-1 text-passive", cell.process)}>
						<span className="flex min-w-0 items-center gap-1" title={showAll ? undefined : other.commands.join("\n")}>
							<span className="truncate">{showAll ? t("shell.memoryProcessFewer") : t("shell.memoryProcessOther", { count: other.count })}</span>
							<ChevronRight aria-hidden="true" className={cn("size-icon-2xs shrink-0 transition-transform", showAll ? "-rotate-90" : "rotate-90")} />
						</span>
					</td>
					<td />
					<td />
					<td className={cn("whitespace-nowrap py-1 tabular-nums text-passive", cell.cpu)}>
						{showAll ? null : other.cpu >= 1 ? formatCPU(other.cpu) : "·"}
					</td>
					<td className={cn("whitespace-nowrap py-1 tabular-nums text-settings-muted", cell.memory)}>{showAll ? null : formatMemory(other.bytes)}</td>
					{showAll ? <td /> : <ShareCell bytes={other.bytes} maxBytes={maxBytes} />}
				</tr>
			) : null}
		</>
	);
}

function ProcessRow({ maxBytes, row }: { maxBytes: number; row: ProcessListRow }) {
	const { t } = useTranslation();
	return (
		<tr className="text-xs" data-testid="session-memory-process-row">
			<td className={cn("py-1 text-settings-muted", cell.process)}>
				<span className="block truncate" title={row.commands.join("\n")}>{row.kind}</span>
			</td>
			<td className={cn("whitespace-nowrap py-1 text-passive", cell.type)} data-testid="session-memory-process-type">
				<span title={t(`shell.memoryCategoryHint.${row.category}`)}>{t(`shell.memoryCategory.${row.category}`)}</span>
			</td>
			<td className={cn("whitespace-nowrap py-1 align-middle tabular-nums", cell.pid)}>
				<CopyControl
					copiedLabel={t("shell.memoryPidCopied", { pid: row.pid })}
					label={t("shell.memoryCopyPid", { pid: row.pid })}
					testId="session-memory-pid"
					value={() => String(row.pid)}
				>
					<span>{row.pid}</span>
				</CopyControl>
			</td>
			<td className={cn("whitespace-nowrap py-1 tabular-nums text-passive", cell.cpu)}>{row.cpu >= 1 ? formatCPU(row.cpu) : "·"}</td>
			<td className={cn("whitespace-nowrap py-1 tabular-nums text-settings-muted", cell.memory)}>{formatMemory(row.bytes)}</td>
			<ShareCell bytes={row.bytes} maxBytes={maxBytes} />
		</tr>
	);
}

function useCopy(value: () => string) {
	const [copied, setCopied] = useState(false);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(() => () => {
		if (timer.current !== null) clearTimeout(timer.current);
	}, []);
	const copy = async () => {
		try {
			await aoBridge.clipboard.writeText(value());
		} catch {
			return;
		}
		setCopied(true);
		if (timer.current !== null) clearTimeout(timer.current);
		timer.current = setTimeout(() => setCopied(false), 1_500);
	};
	return { copied, copy };
}

function CopyReportButton({ value }: { value: () => string }) {
	const { t } = useTranslation();
	const { copied, copy } = useCopy(value);
	const label = copied ? t("shell.memoryReportCopied") : t("shell.memoryCopyReport");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					aria-label={label}
					data-testid="session-memory-copy"
					onClick={() => void copy()}
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					{copied ? <Check aria-hidden="true" className="size-icon-sm text-success" /> : <Copy aria-hidden="true" className="size-icon-sm" />}
				</Button>
			</TooltipTrigger>
			<TooltipContent side="top">{label}</TooltipContent>
		</Tooltip>
	);
}

function CopyControl({
	children,
	copiedLabel,
	label,
	testId,
	value,
}: {
	children: ReactNode;
	copiedLabel: string;
	label: string;
	testId: string;
	value: () => string;
}) {
	const { copied, copy } = useCopy(value);
	return (
		<Pressable
			aria-label={copied ? copiedLabel : label}
			className="group relative inline-flex shrink-0 items-center rounded-sm text-settings-muted transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
			data-testid={testId}
			onClick={(event) => {
				event.stopPropagation();
				void copy();
			}}
			type="button"
		>
			{children}
			{copied ? (
				<Check aria-hidden="true" className="absolute left-full ml-1 size-icon-2xs text-success" />
			) : (
				<Copy
					aria-hidden="true"
					className="absolute left-full ml-1 size-icon-2xs opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100"
				/>
			)}
		</Pressable>
	);
}

const graphWidth = 600;
const graphHeight = 56;

function graphX0(count: number): number {
	return graphWidth - (graphWidth / (sampleHistoryLength - 1)) * (count - 1);
}

export function graphCeiling(values: number[]): number {
	const peak = Math.max(0, ...values);
	return [10, 25, 50].find((step) => peak <= step) ?? 100;
}

function graphPoints(values: number[], ceiling: number): string[] {
	const step = graphWidth / (sampleHistoryLength - 1);
	const x0 = graphX0(values.length);
	return values.map((v, i) => `${(x0 + i * step).toFixed(1)},${(graphHeight - (Math.min(ceiling, Math.max(0, v)) / ceiling) * graphHeight).toFixed(1)}`);
}

export function linePath(values: number[], ceiling = 100): string {
	if (values.length === 0) return "";
	const points = graphPoints(values, ceiling);
	return points.length > 1 ? `M${points[0]} L${points.slice(1).join(" L")}` : `M${points[0]}`;
}

function areaPoints(values: number[], ceiling: number): string {
	if (values.length < 2) return "";
	return [...graphPoints(values, ceiling), `${graphWidth},${graphHeight}`, `${graphX0(values.length).toFixed(1)},${graphHeight}`].join(" ");
}

function CpuGraphs({ current, history }: { current: CPUSample; history: CPUSample[] }) {
	const { t } = useTranslation();
	return (
		<div className="grid gap-3 sm:grid-cols-2">
			<CpuGraph
				fill="fill-warning/15"
				stroke="stroke-warning/80"
				testId="session-cpu-machine"
				title={t("shell.cpuGraphMachine")}
				value={current.host}
				values={history.map((s) => s.host)}
			/>
			<CpuGraph
				fill="fill-accent-strong/10"
				stroke="stroke-accent-strong"
				testId="session-cpu-ao"
				title={t("shell.cpuGraphAO")}
				value={current.ao}
				values={history.map((s) => s.ao)}
			/>
		</div>
	);
}

function CpuGraph({ fill, stroke, testId, title, value, values }: { fill: string; stroke: string; testId: string; title: string; value: number; values: number[] }) {
	const ceiling = graphCeiling([...values, value]);
	return (
		<MetricCard testId={testId} title={title} value={formatCPU(value)}>
			<div className="relative">
				<svg aria-hidden="true" className="h-16 w-full overflow-visible" preserveAspectRatio="none" viewBox={`0 0 ${graphWidth} ${graphHeight}`}>
					{[0, graphHeight / 2].map((y) => (
						<line className="stroke-foreground/[0.06]" key={y} strokeWidth={1} vectorEffect="non-scaling-stroke" x1={0} x2={graphWidth} y1={y} y2={y} />
					))}
					<polygon className={fill} points={areaPoints(values, ceiling)} />
					<path className={cn("fill-none", stroke)} d={linePath(values, ceiling)} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
				</svg>
				<span className="absolute left-0 top-0 -translate-y-1/2 bg-(--color-bg-secondary) pr-1.5 text-2xs leading-none tabular-nums text-passive" data-testid="session-cpu-ceiling">
					{formatCPU(ceiling)}
				</span>
			</div>
		</MetricCard>
	);
}
