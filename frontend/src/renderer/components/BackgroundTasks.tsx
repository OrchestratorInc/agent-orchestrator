import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { create } from "zustand";
import { ChevronDown, ListTodo, SquareTerminal } from "@/components/app-icons";
import {
	useBackgroundTasks,
	useStopBackgroundTask,
	type BackgroundTask,
	type BackgroundTaskReading,
} from "../hooks/useBackgroundTasks";
import { sessionUiKey } from "../lib/hosts";
import { shellTaskLabel } from "../lib/shell-task-label";
import { cn } from "../lib/utils";
import { failedTaskKey, useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

function taskSummary(tasks: BackgroundTask[], t: TFunction): string | null {
	const live = tasks.filter((task) => task.state === "running");
	if (!live.length) return null;
	if (live.every((task) => task.kind === "shell")) return t("backgroundTasks.shells", { count: live.length });
	if (live.every((task) => task.kind === "agent")) return t("backgroundTasks.agents", { count: live.length });
	return t("backgroundTasks.tasks", { count: live.length });
}

export function supportsLocalBackgroundTasks(session: WorkspaceSession) {
	return session.mode === "chat" && !session.isTerminated && !session.hostId && !session.cloud;
}

/** The board supplies its shared batch reading; cards never create a poller. Plain text: it opens nothing. */
export function BackgroundTasksBadge({ session, reading, stale = false }: {
	session: WorkspaceSession;
	reading?: BackgroundTaskReading;
	stale?: boolean;
}) {
	const { t } = useTranslation();
	if (!supportsLocalBackgroundTasks(session)) return null;
	const summary = reading && reading.availability !== "unavailable" ? taskSummary(reading.tasks, t) : null;
	if (!summary) return null;
	return (
		<span className="inline-flex min-w-0 max-w-full items-center gap-1 px-3 text-xs text-muted-foreground">
			<ListTodo aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate">{summary}{stale ? ` · ${t("backgroundTasks.stale")}` : ""}</span>
		</span>
	);
}

type TaskName = { name: string; dir?: string };

/** A shell task by what it does ("Dev server on port 3000"), not the command line its provider ran. */
function taskName(task: BackgroundTask, t: TFunction): TaskName {
	const description = task.description?.trim();
	if (!description) return { name: t(task.kind === "shell" ? "backgroundTasks.kind.shell" : "backgroundTasks.kind.other") };
	if (task.kind !== "shell") return { name: description };
	const label = shellTaskLabel(description);
	const named = (name: string) => ({ name, dir: label.dir });
	switch (label.kind) {
		case "devServer": return named(label.port ? t("backgroundTasks.label.devServerPort", { port: label.port }) : t("backgroundTasks.label.devServer"));
		case "tests": return named(t("backgroundTasks.label.tests"));
		case "build": return named(t("backgroundTasks.label.build"));
		case "lint": return named(t("backgroundTasks.label.lint"));
		case "install": return named(t("backgroundTasks.label.install"));
		case "command": return named(label.text);
	}
}

/** One task's name on its own, as the side panel shows it. */
export function backgroundTaskName(task: BackgroundTask, t: TFunction): string {
	return taskName(task, t).name;
}

/** How a row was told apart from a like one: by its folder, or by its command. */
export type TaskNameSuffixes = Map<string, "dir" | "command">;

const rowKey = (task: BackgroundTask) => `${task.id}:${task.runId ?? ""}`;

/**
 * Each task's name, told apart from the others: two "Dev server" rows become
 * "Dev server · frontend" and "Dev server · backend", or their commands when
 * no folder separates them. A row keeps the name it was given in `suffixes`
 * for as long as it shows, so stopping one server doesn't rename the other.
 */
export function backgroundTaskNames(tasks: BackgroundTask[], t: TFunction, suffixes: TaskNameSuffixes = new Map()): Map<BackgroundTask, string> {
	const named = tasks.map((task) => ({ task, ...taskName(task, t) }));
	const count = (key: string, keys: string[]) => keys.filter((other) => other === key).length;
	const names = named.map(({ name }) => name);
	const withDirs = named.map(({ name, dir }) => (dir ? `${name} · ${dir}` : name));
	const shown = new Set(tasks.map(rowKey));
	for (const key of [...suffixes.keys()]) if (!shown.has(key)) suffixes.delete(key);
	return new Map(named.map(({ task, name, dir }, i) => {
		const key = rowKey(task);
		let suffix = suffixes.get(key);
		if (!suffix && count(name, names) > 1) suffix = dir && count(withDirs[i], withDirs) < 2 ? "dir" : "command";
		if (!suffix) return [task, name];
		suffixes.set(key, suffix);
		if (suffix === "dir" && dir) return [task, withDirs[i]];
		return [task, task.description?.trim() || name];
	}));
}

export const isLive = (task: BackgroundTask) => task.state === "running" || task.state === "paused";

/**
 * What the composer tab shows: live work and failures not yet looked at. Agents
 * have their own Subagents view; work that completed or was stopped needs no
 * attention, and a failure leaves once its tab has shown it (`seenFailures`).
 */
export function composerBackgroundTasks(reading?: BackgroundTaskReading, seenFailures?: readonly string[]): BackgroundTask[] {
	if (!reading || reading.availability === "unavailable") return [];
	const tasks = reading.tasks.filter((task) => task.kind !== "agent"
		&& (isLive(task) || (task.state === "failed" && !seenFailures?.includes(failedTaskKey(task)))));
	// Failures need attention and must remain visible above the five-row cap.
	return [...tasks.filter((task) => !isLive(task)), ...tasks.filter(isLive)];
}

/** Seconds under a minute, minutes under an hour, then hours: "38s", "12m", "1h 5m". */
export function formatElapsed(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m`;
	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function elapsed(task: BackgroundTask, now: number): string | null {
	const start = task.startedAt ? Date.parse(task.startedAt) : Number.NaN;
	const end = isLive(task) ? now : task.endedAt ? Date.parse(task.endedAt) : Number.NaN;
	if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
	return formatElapsed(end - start);
}

/** Ticks only while something runs: every second in a task's first minute, then every ten. */
export function useNow(tasks: BackgroundTask[]): number {
	const [now, setNow] = useState(() => Date.now());
	const live = tasks.filter(isLive);
	const young = live.some((task) => task.startedAt && now - Date.parse(task.startedAt) < 60_000);
	const ticking = live.some((task) => !!task.startedAt);
	useEffect(() => {
		if (!ticking) return;
		const timer = window.setInterval(() => setNow(Date.now()), young ? 1_000 : 10_000);
		return () => window.clearInterval(timer);
	}, [ticking, young]);
	return now;
}

const STOP_SETTLE_MS = 10_000;
const STOP_ERROR_MS = 4_000;

/**
 * Stops in flight for every control that can stop a task (the composer strip
 * and its side-panel tab), keyed by session and task, so a stop started in one
 * disables the other. Each holds the request that took it: only that request
 * releases it, and it outlives the control that started it, so leaving a task's
 * detail mid-request never re-enables Stop elsewhere.
 */
const useStopsInFlight = create<{ keys: ReadonlyMap<string, number> }>(() => ({ keys: new Map() }));
const stopKey = (sessionId: string, taskId: string) => `${sessionId}\n${taskId}`;
const settleTimers = new Map<number, number>();
let lastStopRequest = 0;

function takeStop(key: string): number {
	const request = ++lastStopRequest;
	useStopsInFlight.setState((state) => ({ keys: new Map(state.keys).set(key, request) }));
	// A task that never leaves the live set gets its button back.
	settleTimers.set(request, window.setTimeout(() => releaseStop(key, request), STOP_SETTLE_MS));
	return request;
}

function releaseStop(key: string, request: number) {
	window.clearTimeout(settleTimers.get(request));
	settleTimers.delete(request);
	useStopsInFlight.setState((state) => {
		if (state.keys.get(key) !== request) return state;
		const keys = new Map(state.keys);
		keys.delete(key);
		return { keys };
	});
}

export function resetStopsInFlightForTest(): void {
	for (const timer of settleTimers.values()) window.clearTimeout(timer);
	settleTimers.clear();
	useStopsInFlight.setState({ keys: new Map() });
}

/** Stop requests in flight, per task (shared by every control for this session), and the one that last failed here. */
export function useTaskStops(sessionId: string, liveIds: string) {
	const mutation = useStopBackgroundTask(sessionId);
	const keys = useStopsInFlight((state) => state.keys);
	const stopping = useMemo(() => {
		const prefix = stopKey(sessionId, "");
		return new Set([...keys.keys()].filter((key) => key.startsWith(prefix)).map((key) => key.slice(prefix.length)));
	}, [keys, sessionId]);
	const [failed, setFailed] = useState<string | null>(null);
	// The stops this control started, by task: a stopped task leaves the live set and lets its stop go.
	const started = useRef(new Map<string, number>());
	useEffect(() => {
		const live = new Set(liveIds ? liveIds.split("\n") : []);
		for (const [id, request] of [...started.current]) {
			if (live.has(id)) continue;
			started.current.delete(id);
			releaseStop(stopKey(sessionId, id), request);
		}
	}, [liveIds, sessionId]);
	useEffect(() => {
		if (!failed) return;
		const timer = window.setTimeout(() => setFailed(null), STOP_ERROR_MS);
		return () => window.clearTimeout(timer);
	}, [failed]);
	const stop = (id: string) => {
		if (stopping.has(id)) return;
		setFailed(null);
		const key = stopKey(sessionId, id);
		const request = takeStop(key);
		started.current.set(id, request);
		void mutation.mutateAsync(id).catch(() => {
			releaseStop(key, request);
			setFailed(id);
		});
	};
	return { stopping, failed, stop };
}

/** Chat entry point: a tab on the composer's top edge. Renders nothing while no work shows. */
export function SessionBackgroundTasks({ session, onOpenTask }: { session: WorkspaceSession; onOpenTask?: (task: BackgroundTask) => void }) {
	const enabled = supportsLocalBackgroundTasks(session);
	const query = useBackgroundTasks(session.id, enabled);
	const seenFailures = useUiStore((state) => state.inspectorSessions[sessionUiKey(session.id, session.hostId)]?.seenFailedTasks);
	if (!enabled) return null;
	return <ComposerBackgroundTasks sessionId={session.id} reading={query.isError ? undefined : query.data} onOpenTask={onOpenTask} seenFailures={seenFailures} />;
}

/** `onOpenTask` opens a task in the side panel; without it, tasks are not clickable. */
export function ComposerBackgroundTasks({ sessionId, reading, onOpenTask, seenFailures }: {
	sessionId: string;
	reading?: BackgroundTaskReading;
	onOpenTask?: (task: BackgroundTask) => void;
	/** Failed runs already shown in their tab (`failedTaskKey`), left out of the strip. */
	seenFailures?: readonly string[];
}) {
	const { t } = useTranslation();
	const listId = useId();
	const tasks = useMemo(() => composerBackgroundTasks(reading, seenFailures), [reading, seenFailures]);
	// Parsed once per reading, not on every elapsed-time tick.
	const suffixes = useRef<TaskNameSuffixes>(new Map());
	const names = useMemo(() => backgroundTaskNames(tasks, t, suffixes.current), [tasks, t]);
	const live = tasks.filter(isLive);
	const failedCount = tasks.length - live.length;
	const now = useNow(tasks);
	const { stopping, failed, stop } = useTaskStops(sessionId, live.map((task) => task.id).join("\n"));
	const [open, setOpen] = useState(false);
	const several = tasks.length > 1;
	// Back to one line, the list has nothing to hold: start closed next time.
	const [wasSeveral, setWasSeveral] = useState(several);
	if (several !== wasSeveral) {
		setWasSeveral(several);
		if (!several) setOpen(false);
	}
	const region = useRef<HTMLElement>(null);
	const scope = useRef<HTMLElement | null>(null);
	const focusedElement = useRef<HTMLElement | null>(null);
	useEffect(() => {
		const clearFocus = () => { focusedElement.current = null; };
		const clearOutside = (event: PointerEvent) => {
			if (!(event.target instanceof Node) || !region.current?.contains(event.target)) clearFocus();
		};
		// Clicking non-focusable transcript text blurs to body with no relatedTarget.
		// Forget ownership at the gesture, before a later poll removes the row.
		document.addEventListener("pointerdown", clearOutside, true);
		window.addEventListener("blur", clearFocus);
		return () => {
			document.removeEventListener("pointerdown", clearOutside, true);
			window.removeEventListener("blur", clearFocus);
		};
	}, []);
	useLayoutEffect(() => {
		if (region.current) scope.current = region.current.parentElement;
		// A poll must not move focus that still belongs to a mounted control.
		if (!focusedElement.current || focusedElement.current.isConnected || document.activeElement !== document.body) return;
		focusedElement.current = null;
		// The next row's Stop first, so repeated keyboard stops keep going.
		const next =
			region.current?.querySelector<HTMLElement>("[data-task-id] [data-task-stop]:not(:disabled)") ??
			region.current?.querySelector<HTMLElement>("[data-task-id] button:not(:disabled)") ??
			region.current?.querySelector<HTMLElement>("button:not(:disabled)");
		const editor = scope.current?.querySelector<HTMLElement>('form.cursor-chat-composer [contenteditable="true"], form.cursor-chat-composer textarea');
		(next ?? editor)?.focus();
	}, [tasks]);
	if (!tasks.length) return null;

	const name = (task: BackgroundTask) => names.get(task) ?? "";
	const time = (task: BackgroundTask) => {
		if (failed === task.id) {
			return (
				<span role="status" className="shrink-0 text-caption text-destructive">
					{t("backgroundTasks.stopFailed")}
				</span>
			);
		}
		const text = elapsed(task, now);
		return text ? <span className="shrink-0 text-caption tabular-nums text-muted-foreground">{text}</span> : null;
	};
	// The end of a row: a failure's red dot sits where the stop square would, so
	// times line up. Background work is meant to keep running (a dev server never
	// finishes), so a live task shows no mark; one its provider can't stop ends flush.
	const trailing = (task: BackgroundTask) => {
		if (!isLive(task)) {
			return (
				<span className="grid size-control-sm shrink-0 place-items-center">
					<span role="img" aria-label={t("backgroundTasks.state.failed")} className="size-1.5 rounded-full bg-destructive" />
				</span>
			);
		}
		if (!task.stoppable) return null;
		return (
			<Button
				type="button"
				variant="quiet"
				size="icon-xs"
				className="shrink-0"
				aria-label={t("backgroundTasks.stop", { name: name(task) })}
				data-task-stop=""
				disabled={stopping.has(task.id)}
				onClick={() => stop(task.id)}
			>
				<span aria-hidden="true" className="size-2 rounded-[2px] bg-current" />
			</Button>
		);
	};

	// The task's name and time open it in the side panel; the stop square stays its own control.
	const opener = (task: BackgroundTask, content: ReactNode) => onOpenTask ? (
		<Pressable
			className="flex h-7 min-w-0 flex-1 items-center gap-2 rounded-sm focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
			onClick={() => onOpenTask(task)}
		>
			{content}
		</Pressable>
	) : content;

	return (
		<section
			ref={region}
			aria-label={t("backgroundTasks.title")}
			className="chat-composer-tasks"
			onFocusCapture={(event) => {
				focusedElement.current = event.target as HTMLElement;
			}}
			onBlurCapture={(event) => {
				if (event.relatedTarget && event.currentTarget.contains(event.relatedTarget as Node)) return;
				// Disabling/removing Stop can blur it before settlement. Outside
				// gestures already relinquish ownership through pointerdown above.
				if (!event.relatedTarget && (!event.target.isConnected || event.target.matches(":disabled"))) return;
				focusedElement.current = null;
			}}
		>
			{several ? (
				<Pressable
					aria-expanded={open}
					aria-controls={open ? listId : undefined}
					onClick={() => setOpen((value) => !value)}
					className="flex h-8 w-full min-w-0 items-center gap-2 rounded-sm focus-visible:ring-2 focus-visible:ring-ring"
				>
					{/* Leads the row like the queued-messages toggle above it. */}
					<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground", open ? "rotate-0" : "-rotate-90")} />
					<SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
					{live.length ? (
						<span className="shrink-0 text-xs font-medium text-foreground/90">{t("backgroundTasks.runningCount", { count: live.length })}</span>
					) : null}
					{live.length > 0 && failedCount > 0 ? (
						<span aria-hidden="true" className="text-caption text-muted-foreground">
							·
						</span>
					) : null}
					{failedCount ? (
						<span className="min-w-0 truncate text-caption text-destructive">{t("backgroundTasks.failedCount", { count: failedCount })}</span>
					) : null}
				</Pressable>
			) : null}
			<ul key="task-rows" id={listId} role={several ? "list" : "presentation"} className={several ? "chat-composer-tasks-list" : undefined}>
				{!several || open
					? tasks.map((task) => (
							<li key={`${task.id}:${task.runId ?? ""}`} data-task-id={task.id} className="flex h-8 min-w-0 items-center gap-2">
								{!several ? <SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" /> : null}
								{opener(task, <>
									<span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground/90" title={task.description?.trim() || name(task)}>
										{name(task)}
									</span>
									{/* A live task shows no mark; say its state to screen readers, as failures say theirs. */}
									{task.state === "running" || task.state === "paused" ? (
										<span className="sr-only">{t(task.state === "paused" ? "backgroundTasks.state.paused" : "backgroundTasks.state.running")}</span>
									) : null}
									{time(task)}
								</>)}
								{trailing(task)}
							</li>
						))
					: null}
			</ul>
		</section>
	);
}
