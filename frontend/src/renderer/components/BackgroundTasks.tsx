import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, ListTodo, Loader2, SquareTerminal } from "@/components/app-icons";
import {
	useBackgroundTasks,
	useStopBackgroundTask,
	type BackgroundTask,
	type BackgroundTaskReading,
} from "../hooks/useBackgroundTasks";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

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
		<span className="inline-flex min-w-0 max-w-full items-center gap-1 text-xs text-muted-foreground">
			<ListTodo aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate">{summary}{stale ? ` · ${t("backgroundTasks.stale")}` : ""}</span>
		</span>
	);
}

const isLive = (task: BackgroundTask) => task.state === "running" || task.state === "paused";

/**
 * What the composer tab shows: live work and recent failures. Agents have their
 * own Subagents view; work that completed or was stopped needs no attention.
 */
export function composerBackgroundTasks(reading?: BackgroundTaskReading): BackgroundTask[] {
	if (!reading || reading.availability === "unavailable") return [];
	const tasks = reading.tasks.filter((task) => task.kind !== "agent" && (isLive(task) || task.state === "failed"));
	// Live work first; the daemon orders by state, but a failure must never lead.
	return [...tasks.filter(isLive), ...tasks.filter((task) => !isLive(task))];
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
	const ticking = live.length > 0;
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
	return <ComposerBackgroundTasks sessionId={session.id} reading={query.data} />;
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
	if (!tasks.length) return null;

	const name = (task: BackgroundTask) => task.description || t(task.kind === "shell" ? "backgroundTasks.kind.shell" : "backgroundTasks.kind.other");
	const status = (task: BackgroundTask) => isLive(task) ? (
		<span role="img" aria-label={t("backgroundTasks.state.running")} className="grid size-3 shrink-0 place-items-center">
			<Loader2 aria-hidden="true" className="size-3 animate-spin text-muted-foreground/60 motion-reduce:animate-none" />
		</span>
	) : (
		<span role="img" aria-label={t("backgroundTasks.state.failed")} className="size-1.5 shrink-0 rounded-full bg-destructive" />
	);
	const time = (task: BackgroundTask) => {
		if (failed === task.id) {
			return <span role="status" className="shrink-0 text-[11px] text-destructive">{t("backgroundTasks.stopFailed")}</span>;
		}
		const text = elapsed(task, now);
		return text ? <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">{text}</span> : null;
	};
	// Only a live task its provider can stop gets the square; the rest end flush.
	const stopButton = (task: BackgroundTask) => isLive(task) && task.stoppable ? (
		<Button
			type="button"
			variant="quiet"
			size="icon-xs"
			className="-mr-1 shrink-0"
			aria-label={t("backgroundTasks.stop", { name: name(task) })}
			disabled={stopping.has(task.id)}
			onClick={() => stop(task.id)}
		>
			<span aria-hidden="true" className="size-2 rounded-[2px] bg-current" />
		</Button>
	) : null;

	if (!several) {
		const [task] = tasks;
		return (
			<section aria-label={t("backgroundTasks.title")} className="chat-composer-tasks">
				<div className="flex h-8 min-w-0 items-center gap-2">
					<SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
					<span className="min-w-0 truncate text-xs font-medium text-foreground/90" title={name(task)}>{name(task)}</span>
					<span className="ml-auto flex shrink-0 items-center gap-2 pl-2">
						{status(task)}
						{time(task)}
					</span>
					{stopButton(task)}
				</div>
			</section>
		);
	}

	return (
		<section aria-label={t("backgroundTasks.title")} className="chat-composer-tasks">
			<button
				type="button"
				aria-expanded={open}
				aria-controls={open ? listId : undefined}
				onClick={() => setOpen((value) => !value)}
				className="flex h-8 w-full min-w-0 items-center gap-2 rounded-sm text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				<SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
				{live.length ? <span className="shrink-0 text-xs font-medium text-foreground/90">{t("backgroundTasks.runningCount", { count: live.length })}</span> : null}
				{failedCount ? <span className="min-w-0 truncate text-[11px] text-destructive">{t("backgroundTasks.failedCount", { count: failedCount })}</span> : null}
				<ChevronRight aria-hidden="true" className={cn("ml-auto size-3 shrink-0 text-muted-foreground/60 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
			</button>
			{open ? (
				<ul id={listId} className="chat-composer-tasks-list">
					{tasks.map((task) => (
						<li key={`${task.id}:${task.runId ?? ""}`} className="flex h-8 min-w-0 items-center gap-2">
							<span className="grid size-4 shrink-0 place-items-center">{status(task)}</span>
							<span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground/90" title={name(task)}>{name(task)}</span>
							{time(task)}
							{stopButton(task)}
						</li>
					))}
				</ul>
			) : null}
		</section>
	);
}
