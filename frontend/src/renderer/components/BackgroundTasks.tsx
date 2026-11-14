import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, ListTodo, SquareTerminal } from "@/components/app-icons";
import {
	useBackgroundTasks,
	useStopBackgroundTask,
	type BackgroundTask,
	type BackgroundTaskReading,
} from "../hooks/useBackgroundTasks";
import { shellTaskLabel } from "../lib/shell-task-label";
import { cn } from "../lib/utils";
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

/** A shell task by what it does ("Dev server on port 3000"), not the command line its provider ran. */
export function backgroundTaskName(task: BackgroundTask, t: TFunction): string {
	if (!task.description) return t(task.kind === "shell" ? "backgroundTasks.kind.shell" : "backgroundTasks.kind.other");
	if (task.kind !== "shell") return task.description;
	const label = shellTaskLabel(task.description);
	switch (label.kind) {
		case "devServer": return label.port ? t("backgroundTasks.label.devServerPort", { port: label.port }) : t("backgroundTasks.label.devServer");
		case "tests": return t("backgroundTasks.label.tests");
		case "build": return t("backgroundTasks.label.build");
		case "lint": return t("backgroundTasks.label.lint");
		case "install": return t("backgroundTasks.label.install");
		case "command": return label.text;
	}
}

const isLive = (task: BackgroundTask) => task.state === "running" || task.state === "paused";

/**
 * What the composer tab shows: live work and recent failures. Agents have their
 * own Subagents view; work that completed or was stopped needs no attention.
 */
export function composerBackgroundTasks(reading?: BackgroundTaskReading): BackgroundTask[] {
	if (!reading || reading.availability === "unavailable") return [];
	const tasks = reading.tasks.filter((task) => task.kind !== "agent" && (isLive(task) || task.state === "failed"));
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

function elapsed(task: BackgroundTask, now: number): string | null {
	const start = task.startedAt ? Date.parse(task.startedAt) : Number.NaN;
	const end = isLive(task) ? now : task.endedAt ? Date.parse(task.endedAt) : Number.NaN;
	if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
	return formatElapsed(end - start);
}

/** Ticks only while something runs: every second in a task's first minute, then every ten. */
function useNow(tasks: BackgroundTask[]): number {
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

/** Stop requests in flight, per task, and the one that last failed. */
function useTaskStops(sessionId: string, liveIds: string) {
	const mutation = useStopBackgroundTask(sessionId);
	const [stopping, setStopping] = useState<ReadonlySet<string>>(() => new Set());
	const [failed, setFailed] = useState<string | null>(null);
	const timers = useRef(new Map<string, number>());
	const release = useCallback((id: string) => {
		window.clearTimeout(timers.current.get(id));
		timers.current.delete(id);
		setStopping((previous) => {
			if (!previous.has(id)) return previous;
			const next = new Set(previous);
			next.delete(id);
			return next;
		});
	}, []);
	// A stopped task leaves the live set; one that never does gets its button back.
	useEffect(() => {
		const live = new Set(liveIds ? liveIds.split("\n") : []);
		for (const id of [...timers.current.keys()]) if (!live.has(id)) release(id);
	}, [liveIds, release]);
	useEffect(() => {
		if (!failed) return;
		const timer = window.setTimeout(() => setFailed(null), STOP_ERROR_MS);
		return () => window.clearTimeout(timer);
	}, [failed]);
	useEffect(() => () => {
		for (const timer of timers.current.values()) window.clearTimeout(timer);
	}, []);
	const stop = (id: string) => {
		if (stopping.has(id)) return;
		setFailed(null);
		setStopping((previous) => new Set(previous).add(id));
		timers.current.set(id, window.setTimeout(() => release(id), STOP_SETTLE_MS));
		void mutation.mutateAsync(id).catch(() => {
			release(id);
			setFailed(id);
		});
	};
	return { stopping, failed, stop };
}

/** Chat entry point: a tab on the composer's top edge. Renders nothing while no work shows. */
export function SessionBackgroundTasks({ session }: { session: WorkspaceSession }) {
	const enabled = supportsLocalBackgroundTasks(session);
	const query = useBackgroundTasks(session.id, enabled);
	if (!enabled) return null;
	return <ComposerBackgroundTasks sessionId={session.id} reading={query.isError ? undefined : query.data} />;
}

export function ComposerBackgroundTasks({ sessionId, reading }: { sessionId: string; reading?: BackgroundTaskReading }) {
	const { t } = useTranslation();
	const listId = useId();
	const tasks = useMemo(() => composerBackgroundTasks(reading), [reading]);
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
	useLayoutEffect(() => {
		if (region.current) scope.current = region.current.parentElement;
		// A poll must not move focus that still belongs to a mounted control.
		if (!focusedElement.current || focusedElement.current.isConnected || document.activeElement !== document.body) return;
		focusedElement.current = null;
		if (document.activeElement === document.body || region.current?.contains(document.activeElement)) {
			const next =
				region.current?.querySelector<HTMLElement>("[data-task-id] button:not(:disabled)") ??
				region.current?.querySelector<HTMLElement>("button:not(:disabled)");
			const editor = scope.current?.querySelector<HTMLElement>('[contenteditable="true"], textarea, [role="textbox"]');
			(next ?? editor)?.focus();
		}
	}, [tasks]);
	if (!tasks.length) return null;

	const name = (task: BackgroundTask) => backgroundTaskName(task, t);
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
				disabled={stopping.has(task.id)}
				onClick={() => stop(task.id)}
			>
				<span aria-hidden="true" className="size-2 rounded-[2px] bg-current" />
			</Button>
		);
	};

	return (
		<section
			ref={region}
			aria-label={t("backgroundTasks.title")}
			className="chat-composer-tasks"
			onFocusCapture={(event) => {
				focusedElement.current = event.target as HTMLElement;
			}}
			onBlurCapture={(event) => {
				if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node)) focusedElement.current = null;
			}}
		>
			{several ? (
				<Pressable
					aria-expanded={open}
					aria-controls={open ? listId : undefined}
					onClick={() => setOpen((value) => !value)}
					className="flex h-8 w-full min-w-0 items-center gap-2 rounded-sm focus-visible:ring-2 focus-visible:ring-ring"
				>
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
					<ChevronRight
						aria-hidden="true"
						className={cn("ml-auto size-3 shrink-0 text-muted-foreground/60 transition-transform motion-reduce:transition-none", open && "rotate-90")}
					/>
				</Pressable>
			) : null}
			<ul key="task-rows" id={listId} role={several ? "list" : "presentation"} className={several ? "chat-composer-tasks-list" : undefined}>
				{!several || open
					? tasks.map((task) => (
							<li key={`${task.id}:${task.runId ?? ""}`} data-task-id={task.id} className="flex h-8 min-w-0 items-center gap-2">
								{!several ? <SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" /> : null}
								<span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground/90" title={name(task)}>
									{name(task)}
								</span>
								{time(task)}
								{trailing(task)}
							</li>
						))
					: null}
			</ul>
		</section>
	);
}
