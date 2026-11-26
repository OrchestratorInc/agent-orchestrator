import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { InspectorSection } from "@aoagents/product-ui";
import { ArrowDown, ArrowLeft, ChevronRight, Square, SquareTerminal } from "@/components/app-icons";
import { useBackgroundTaskOutput } from "../hooks/useBackgroundTasks";
import { useUiStore, type BackgroundTaskTab } from "../stores/ui-store";
import { lineTone, outputLines, outputSummary } from "../lib/output-lines";
import { cn } from "../lib/utils";
import { backgroundTaskName, elapsed, isLive, useNow, useTaskStops } from "./BackgroundTasks";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * The side panel's Background tasks tab, laid out like Subagents: every task the
 * chat ran, running ones first, and one task's output at a time behind a header
 * that leads back to the list.
 */
export function BackgroundTasksPanel({ sessionId, uiKey, tasks, openTask, isVisible, stale = false }: {
	sessionId: string;
	/** The session's side-panel key, where the open task is kept. */
	uiKey: string;
	tasks: readonly BackgroundTaskTab[];
	/** The task whose output shows; the list shows while unset. */
	openTask?: BackgroundTaskTab;
	isVisible: boolean;
	/** The last reading failed to refresh: its rows stay readable, but Stop and live output wait for the next one. */
	stale?: boolean;
}) {
	// The list and a task replace each other, so focus would leave with them.
	// Opening a row moves it to Back, and Back returns it to the row it left.
	const rootRef = useRef<HTMLDivElement>(null);
	const [focusBackFor, setFocusBackFor] = useState<string>();
	const returnFocusTo = useRef<string | undefined>(undefined);
	const openFromList = useCallback((taskId: string) => {
		setFocusBackFor(taskId);
		useUiStore.getState().selectTaskTab(uiKey, taskId);
	}, [uiKey]);
	const back = useCallback(() => {
		returnFocusTo.current = openTask?.task.id;
		setFocusBackFor(undefined);
		useUiStore.getState().showTaskList(uiKey);
	}, [openTask?.task.id, uiKey]);
	useEffect(() => {
		const taskId = returnFocusTo.current;
		if (openTask || !taskId) return;
		returnFocusTo.current = undefined;
		const rows = rootRef.current?.querySelectorAll<HTMLElement>("[data-task-row]") ?? [];
		Array.from(rows).find((row) => row.dataset.taskRow === taskId)?.focus();
	}, [openTask]);

	return (
		<div ref={rootRef} className="flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden">
			{openTask ? (
				<BackgroundTaskPanel
					key={openTask.task.id}
					autoFocusBack={focusBackFor === openTask.task.id}
					isVisible={isVisible}
					onBack={back}
					sessionId={sessionId}
					stale={stale}
					tab={openTask}
				/>
			) : (
				<BackgroundTaskList tasks={tasks} stale={stale} onOpen={openFromList} />
			)}
		</div>
	);
}

/** Running tasks first, then the finished ones; the newest at the top of each. */
function BackgroundTaskList({ tasks, stale, onOpen }: { tasks: readonly BackgroundTaskTab[]; stale: boolean; onOpen: (taskId: string) => void }) {
	const { t } = useTranslation();
	const newestFirst = newestTasksFirst(tasks);
	const running = newestFirst.filter(isRunning);
	const finished = newestFirst.filter((entry) => !isRunning(entry));
	const now = useNow(running.map((entry) => entry.task));
	// The side panel's own body and sections, as Summary and Subagents draw theirs.
	return (
		<div className="board-scrollbar flex min-h-0 flex-1 flex-col overflow-y-auto overflow-x-hidden pt-0.5 pb-3">
			<BackgroundTaskGroup label={t("backgroundTasks.state.running")} note={stale ? t("backgroundTasks.stale") : undefined} tasks={running} now={now} onOpen={onOpen} />
			<BackgroundTaskGroup label={t("backgroundTasks.panel.finished")} tasks={finished} now={now} onOpen={onOpen} />
		</div>
	);
}

/**
 * Newest start first. A task without a start time (a provider may not report
 * one) goes after the timed ones, newest seen first; the list keeps tasks in
 * the order they were first seen.
 */
export function newestTasksFirst(tasks: readonly BackgroundTaskTab[]): BackgroundTaskTab[] {
	const startOf = (entry: BackgroundTaskTab) => (entry.task.startedAt ? Date.parse(entry.task.startedAt) : Number.NaN);
	return tasks.map((entry, seen) => ({ entry, seen, start: startOf(entry) }))
		.sort((a, b) => {
			const aTimed = Number.isFinite(a.start);
			const bTimed = Number.isFinite(b.start);
			if (aTimed !== bTimed) return aTimed ? -1 : 1;
			if (aTimed && a.start !== b.start) return b.start - a.start;
			return b.seen - a.seen;
		})
		.map(({ entry }) => entry);
}

function BackgroundTaskGroup({ label, note, tasks, now, onOpen }: {
	label: string;
	note?: string;
	tasks: readonly BackgroundTaskTab[];
	now: number;
	onOpen: (taskId: string) => void;
}) {
	const { t } = useTranslation();
	if (tasks.length === 0) return null;
	return (
		<div role="group" aria-label={label}>
			<InspectorSection
				surface={false}
				title={note ? t("backgroundTasks.panel.groupWithNote", { label, n: tasks.length, note }) : t("backgroundTasks.panel.group", { label, n: tasks.length })}
			>
				<ul className="flex min-w-0 flex-col">
					{tasks.map((entry) => (
						<li key={entry.task.id}>
							<BackgroundTaskRow entry={entry} now={now} onClick={() => onOpen(entry.task.id)} />
						</li>
					))}
				</ul>
			</InspectorSection>
		</div>
	);
}

/** One task as a line, like a Subagents row: its mark and name, then how it stands and how long it ran. */
function BackgroundTaskRow({ entry, now, onClick }: { entry: BackgroundTaskTab; now: number; onClick: () => void }) {
	const { t } = useTranslation();
	// A task that ended unreported has no end time to measure to.
	const time = entry.ended ? null : elapsed(entry.task, now);
	return (
		<Pressable
			data-task-row={entry.task.id}
			className="group/task flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1 text-left text-sm transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover"
			onClick={onClick}
		>
			<TaskMark entry={entry} />
			<span className="min-w-0 truncate text-foreground/90">{backgroundTaskName(entry.task, t)}</span>
			<span className="flex flex-1 items-center justify-end gap-2">
				<TaskState entry={entry} />
				{time ? <span className="shrink-0 text-caption tabular-nums text-muted-foreground/70">{time}</span> : null}
				<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-muted-foreground/40 group-hover/task:text-muted-foreground" />
			</span>
		</Pressable>
	);
}

const isRunning = (entry: BackgroundTaskTab) => !entry.ended && isLive(entry.task);

/** The terminal mark, with a dot while the task runs or after it failed, as a subagent's avatar carries one. */
function TaskMark({ entry }: { entry: BackgroundTaskTab }) {
	const failed = !entry.ended && entry.task.state === "failed";
	return (
		<span aria-hidden="true" className="relative flex shrink-0">
			<SquareTerminal className="size-4 text-muted-foreground" />
			{isRunning(entry) || failed ? (
				<span className={cn("absolute -bottom-0.5 -right-0.5 size-1.5 rounded-full ring-1 ring-background", failed ? "bg-destructive" : "bg-status-working")} />
			) : null}
		</span>
	);
}

/**
 * A word for anything that did not simply run or finish, so the state never
 * rests on color alone. Running and completed show nothing: the group, the dot
 * and the time already say it, and screen readers still hear it.
 */
function TaskState({ entry }: { entry: BackgroundTaskTab }) {
	const { t } = useTranslation();
	const { task, ended } = entry;
	if (ended) return <span className="shrink-0 text-caption text-muted-foreground/70">{t("backgroundTasks.state.ended")}</span>;
	switch (task.state) {
		case "failed":
			return <span className="shrink-0 text-caption text-destructive">{t("backgroundTasks.state.failed")}</span>;
		case "stopped":
			return (
				<span className="flex shrink-0 items-center gap-1 text-caption text-muted-foreground/70">
					<Square aria-hidden="true" className="size-2.5" />
					{t("backgroundTasks.state.stopped")}
				</span>
			);
		case "paused":
			return <span className="shrink-0 text-caption text-muted-foreground/70">{t("backgroundTasks.state.paused")}</span>;
		default:
			return <span className="sr-only">{stateLabel(task.state, t)}</span>;
	}
}

/**
 * One background task under a header that leads back to the list: what it is,
 * how it stands, the command it runs and its latest output. A task that just
 * failed or left the reading can still be read here.
 */
export function BackgroundTaskPanel({ sessionId, tab, isVisible, onBack, autoFocusBack = false, stale = false }: {
	sessionId: string;
	tab: BackgroundTaskTab;
	isVisible: boolean;
	/** The reading failed to refresh: Stop and live output wait until the provider answers again. */
	stale?: boolean;
	onBack: () => void;
	/** Opened from the list: focus starts on Back, where the reader's row was. */
	autoFocusBack?: boolean;
}) {
	const { t } = useTranslation();
	const { task, ended } = tab;
	const live = !ended && isLive(task);
	const name = backgroundTaskName(task, t);
	const now = useNow(live ? [task] : []);
	const { stopping, failed, stop } = useTaskStops(sessionId, live ? task.id : "");
	const output = useBackgroundTaskOutput(sessionId, task.id, task.runId, { enabled: isVisible, live: live && isVisible && !stale, over: !live });
	// Output the chat no longer reports on a task that is over stays as last read (`lastRead`).
	const shown = output.data?.availability === "ready" ? output.data : undefined;
	// The last lines land after the task ends: read once more when it does.
	const { refetch } = output;
	const wasLive = useRef(live);
	useEffect(() => {
		if (wasLive.current && !live && isVisible) void refetch();
		wasLive.current = live;
	}, [isVisible, live, refetch]);
	const backButton = useRef<HTMLButtonElement>(null);
	// Focus moved here for the reader opens no tooltip, which would cover the
	// side panel's tabs; hovering, or focusing it again, still does.
	const [backTip, setBackTip] = useState(false);
	const quietFocus = useRef(false);
	useEffect(() => {
		if (!autoFocusBack) return;
		quietFocus.current = true;
		backButton.current?.focus();
	}, [autoFocusBack]);

	const backLabel = t("backgroundTasks.panel.back");
	const time = elapsed(task, now);
	const command = task.command && task.command !== name ? task.command : null;
	const text = shown?.output ?? "";
	const summary = useMemo(() => (text ? outputSummary(text) : null), [text]);
	// Lines to follow, rather than a one-line note in their place.
	const tailing = text !== "";

	return (
		// The side panel's frame is the card: a header block (the task and Stop,
		// then its command and counts) over the output, with Jump to latest
		// docked at its bottom.
		// The command shows in full while the panel has room for it. A longer one
		// scrolls in its own box: beside output it keeps at most half the panel,
		// and beside a note it leaves just the note's line.
		<section aria-label={name} className="flex min-h-0 flex-1 flex-col">
			<header className="flex min-h-0 flex-col gap-0.5 border-b border-border pt-1.5 pr-2 pb-2.5 pl-3">
				<div className="flex h-control-md min-w-0 shrink-0 items-center gap-1.5 text-xs">
					<Tooltip open={backTip} onOpenChange={(open) => setBackTip(open && !quietFocus.current)}>
						<TooltipTrigger asChild>
							<Button
								ref={backButton}
								type="button"
								variant="ghost"
								size="icon-sm"
								// Its arrow lines up with the command and the output below.
								className="-ml-2 shrink-0"
								aria-label={backLabel}
								onClick={onBack}
								onBlur={() => { quietFocus.current = false; }}
								onPointerEnter={() => { quietFocus.current = false; }}
							>
								<ArrowLeft aria-hidden="true" className="size-3.5" />
							</Button>
						</TooltipTrigger>
						<TooltipContent>{backLabel}</TooltipContent>
					</Tooltip>
					<TaskMark entry={tab} />
					<h3 className="min-w-0 truncate text-xs font-medium text-foreground">{name}</h3>
					{time ? (
						<>
							<span aria-hidden="true" className="shrink-0 text-caption text-muted-foreground/70">·</span>
							<span className="shrink-0 text-caption tabular-nums text-muted-foreground/70">{time}</span>
						</>
					) : null}
					<span className="ml-auto flex shrink-0 items-center gap-1.5">
						{failed === task.id ? <span role="status" className="shrink-0 text-caption text-destructive">{t("backgroundTasks.stopFailed")}</span> : null}
						<TaskState entry={tab} />
						{stale ? <span className="shrink-0 text-caption text-muted-foreground/70">{t("backgroundTasks.stale")}</span> : null}
						{live && task.stoppable ? (
							<Button
								type="button"
								variant="quiet"
								size="xs"
								className="shrink-0"
								aria-label={t("backgroundTasks.stop", { name })}
								disabled={stale || stopping.has(task.id)}
								onClick={() => stop(task.id)}
							>
								<Square aria-hidden="true" className="size-2.5 fill-current" />
								{t("backgroundTasks.stopAction")}
							</Button>
						) : null}
					</span>
				</div>
				{command || summary ? (
					<div className="flex min-h-0 min-w-0 items-baseline gap-3 pr-1">
						{command ? (
							<code className="board-scrollbar max-h-full min-w-0 flex-1 overflow-y-auto font-mono text-xs leading-[18px] break-all whitespace-pre-wrap text-muted-foreground select-text">
								{command}
							</code>
						) : null}
						{summary ? (
							<span className="ml-auto shrink-0 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
								{t("backgroundTasks.output.lines", { count: summary.lines })}
								{summary.warnings ? (
									<><span aria-hidden="true"> · </span><span className="text-(--color-term-yellow)">{t("backgroundTasks.output.warnings", { count: summary.warnings })}</span></>
								) : null}
								{summary.errors ? (
									<><span aria-hidden="true"> · </span><span className="text-(--color-term-red)">{t("backgroundTasks.output.errors", { count: summary.errors })}</span></>
								) : null}
							</span>
						) : null}
					</div>
				) : null}
			</header>
			<div className={cn("flex flex-col", tailing ? "min-h-0 flex-[1_0_50%]" : "flex-[1_0_auto]")}>
				{/* A failed refresh keeps what was read; only a first read that fails says so in place of output. */}
				{output.isError && !shown ? (
					<p className="px-3 py-2.5 text-xs text-muted-foreground">{t("backgroundTasks.output.error")}</p>
				) : shown ? (
					<>
						{shown.lastRead ? <p className="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">{t("backgroundTasks.output.lastRead")}</p> : null}
						{output.isError ? <p role="status" className="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">{t("backgroundTasks.output.error")}</p> : null}
						{shown.output === "" ? (
							<p className="px-3 py-2.5 text-xs text-muted-foreground">
								{shown.logOutsideWorkspace ? t("backgroundTasks.output.outsideWorkspace") : t("backgroundTasks.output.empty")}
							</p>
						) : (
							<TailedOutput key={task.id} label={t("backgroundTasks.output.label")} text={shown.output} truncated={Boolean(shown.truncated)} />
						)}
					</>
				) : output.data?.availability === "unavailable" ? (
					<p className="px-3 py-2.5 text-xs text-muted-foreground">
						{ended ? t("backgroundTasks.output.endedUnavailable") : t("backgroundTasks.output.unavailable")}
					</p>
				) : null}
			</div>
		</section>
	);
}

function stateLabel(state: BackgroundTaskTab["task"]["state"], t: TFunction): string {
	switch (state) {
		case "running": return t("backgroundTasks.state.running");
		case "paused": return t("backgroundTasks.state.paused");
		case "completed": return t("backgroundTasks.state.completed");
		case "failed": return t("backgroundTasks.state.failed");
		case "stopped": return t("backgroundTasks.state.stopped");
	}
}

// Within this many pixels of the end counts as reading the latest line.
const BOTTOM_SLACK_PX = 8;

/**
 * Output that follows its newest line. Scrolling up, or selecting text, holds
 * the view still: the text being read stays put while new lines arrive, and
 * "Jump to latest" (or scrolling back down) resumes following.
 */
export function TailedOutput({ text, truncated, label }: { text: string; truncated: boolean; label: string }) {
	const { t } = useTranslation();
	const scrollRef = useRef<HTMLDivElement>(null);
	const preRef = useRef<HTMLPreElement>(null);
	const [following, setFollowing] = useState(true);
	const [selecting, setSelecting] = useState(false);
	const [held, setHeld] = useState<{ text: string; truncated: boolean } | null>(null);
	const latest = useRef({ text, truncated });
	latest.current = { text, truncated };
	const live = following && !selecting;
	const shown = live || !held ? { text, truncated } : held;
	const lines = useMemo(() => outputLines(shown.text), [shown.text]);
	const liveRef = useRef(live);
	liveRef.current = live;

	const hold = useCallback(() => setHeld((current) => current ?? latest.current), []);
	const toBottom = useCallback(() => {
		const element = scrollRef.current;
		if (element) element.scrollTop = element.scrollHeight;
	}, []);

	// Following: each new line lands in view before paint, so nothing flickers.
	useLayoutEffect(() => {
		if (live) toBottom();
	}, [live, shown.text, toBottom]);
	useEffect(() => {
		if (live) setHeld(null);
	}, [live]);
	// A resized panel or wrapped lines keep the newest line in view.
	useEffect(() => {
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => {
			if (liveRef.current) toBottom();
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [toBottom]);
	// Selecting text holds the view, so a refresh never drops the selection.
	useEffect(() => {
		const onSelectionChange = () => {
			const selection = document.getSelection();
			const inside = Boolean(selection && !selection.isCollapsed && preRef.current?.contains(selection.anchorNode));
			if (inside) hold();
			setSelecting(inside);
		};
		document.addEventListener("selectionchange", onSelectionChange);
		return () => document.removeEventListener("selectionchange", onSelectionChange);
	}, [hold]);

	const onScroll = () => {
		const element = scrollRef.current;
		if (!element) return;
		const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight <= BOTTOM_SLACK_PX;
		if (atBottom !== following) {
			if (!atBottom) hold();
			setFollowing(atBottom);
		}
	};
	const jumpToLatest = () => {
		setFollowing(true);
		document.getSelection()?.removeAllRanges();
		setSelecting(false);
	};

	// One row per line, so a warning or error tints its whole row and a wrapped
	// line stays one block. The rows are blocks inside one <pre>, so a copied
	// selection keeps its line breaks. No scrollbar, so tinted rows run the
	// full width; the wheel, the keyboard and Jump to latest still scroll it.
	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div
				aria-label={label}
				className="scrollbar-none min-h-0 flex-1 overflow-y-auto overscroll-contain py-1.5 outline-none focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
				onScroll={onScroll}
				ref={scrollRef}
				role="region"
				tabIndex={0}
			>
				{shown.truncated ? <p className="px-3 pb-1 text-xs text-passive">{t("backgroundTasks.output.truncated")}</p> : null}
				<pre className="font-mono text-xs leading-[18px] text-foreground/90 select-text" ref={preRef}>
					{lines.map((line, index) => {
						const tone = lineTone(line);
						return (
							<span
								// Rows hold no state, so reusing one for a shifted line costs only its text.
								key={index}
								className={cn(
									"block min-h-6 px-3 py-[3px] whitespace-pre-wrap [overflow-wrap:anywhere]",
									tone === "ok" && "text-(--color-term-green)",
									// Lighter than the red tint: the light theme's yellow text needs it for 4.5:1.
									tone === "warning" && "bg-(--color-term-yellow)/8 text-(--color-term-yellow)",
									tone === "error" && "bg-(--color-term-red)/12 text-(--color-term-red)",
								)}
							>
								{line}
							</span>
						);
					})}
				</pre>
			</div>
			{!following ? (
				<div className="flex h-8 shrink-0 items-center justify-center border-t border-border bg-card">
					<Button type="button" variant="ghost" size="xs" onClick={jumpToLatest}>
						<ArrowDown aria-hidden="true" className="size-3" />
						{t("backgroundTasks.output.jumpToLatest")}
					</Button>
				</div>
			) : null}
		</div>
	);
}
