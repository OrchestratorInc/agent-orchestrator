import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ArrowDown } from "@/components/app-icons";
import { useBackgroundTaskOutput } from "../hooks/useBackgroundTasks";
import type { BackgroundTaskTab } from "../stores/ui-store";
import { cn } from "../lib/utils";
import { backgroundTaskName, elapsed, isLive, useNow, useTaskStops } from "./BackgroundTasks";
import { Button } from "./ui/button";

/**
 * One background task in its side-panel tab: what it is, how it is doing, the
 * command it runs and its latest output. The tab outlives the task, so a run
 * that just failed can still be read; nothing is kept once the tab closes.
 */
export function BackgroundTaskPanel({ sessionId, tab, isVisible }: { sessionId: string; tab: BackgroundTaskTab; isVisible: boolean }) {
	const { t } = useTranslation();
	const { task, ended } = tab;
	const live = !ended && isLive(task);
	const name = backgroundTaskName(task, t);
	const now = useNow(live ? [task] : []);
	const { stopping, failed, stop } = useTaskStops(sessionId, live ? task.id : "");
	const output = useBackgroundTaskOutput(sessionId, task.id, { enabled: isVisible, live: live && isVisible });
	// The last lines land after the task ends: read once more when it does.
	const { refetch } = output;
	const wasLive = useRef(live);
	useEffect(() => {
		if (wasLive.current && !live && isVisible) void refetch();
		wasLive.current = live;
	}, [isVisible, live, refetch]);

	const status = ended ? t("backgroundTasks.state.ended") : stateLabel(task.state, t);
	const time = elapsed(task, now);
	const command = task.command && task.command !== name ? task.command : null;

	return (
		<section aria-label={name} className="flex h-full min-h-0 flex-col">
			<header className="flex shrink-0 flex-col gap-1.5 px-3 pt-2.5 pb-3">
				<div className="flex min-h-control-sm min-w-0 items-center gap-2">
					<h2 className="min-w-0 flex-1 truncate text-sm font-medium text-foreground" title={name}>{name}</h2>
					{live && task.stoppable ? (
						<Button
							type="button"
							variant="quiet"
							size="xs"
							className="-mr-1 shrink-0"
							aria-label={t("backgroundTasks.stop", { name })}
							disabled={stopping.has(task.id)}
							onClick={() => stop(task.id)}
						>
							<span aria-hidden="true" className="size-2 rounded-[2px] bg-current" />
							{t("backgroundTasks.stopAction")}
						</Button>
					) : null}
				</div>
				<p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
					{task.state === "failed" && !ended ? <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-destructive" /> : null}
					<span className={cn(task.state === "failed" && !ended && "text-destructive")}>{status}</span>
					{time ? <><span aria-hidden="true">·</span><span className="tabular-nums">{time}</span></> : null}
					{failed === task.id ? <span role="status" className="text-destructive">· {t("backgroundTasks.stopFailed")}</span> : null}
				</p>
				{command ? (
					<code className="board-scrollbar max-h-20 overflow-y-auto font-mono text-xs break-all whitespace-pre-wrap text-muted-foreground select-text">
						{command}
					</code>
				) : null}
			</header>
			<div className="flex min-h-0 flex-1 flex-col border-t border-border">
				{output.isError ? (
					<p className="px-3 py-2.5 text-xs text-muted-foreground">{t("backgroundTasks.output.error")}</p>
				) : output.data?.availability === "unavailable" ? (
					<p className="px-3 py-2.5 text-xs text-muted-foreground">{t("backgroundTasks.output.unavailable")}</p>
				) : output.data && output.data.output === "" ? (
					<p className="px-3 py-2.5 text-xs text-muted-foreground">{t("backgroundTasks.output.empty")}</p>
				) : output.data ? (
					<TailedOutput key={task.id} label={t("backgroundTasks.output.label")} text={output.data.output} truncated={Boolean(output.data.truncated)} />
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

	return (
		<div className="relative flex min-h-0 flex-1">
			<div
				aria-label={label}
				className="board-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-2 outline-none focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
				onScroll={onScroll}
				ref={scrollRef}
				role="region"
				tabIndex={0}
			>
				{shown.truncated ? <p className="pb-1 text-xs text-passive">{t("backgroundTasks.output.truncated")}</p> : null}
				<pre className="font-mono text-xs leading-relaxed whitespace-pre-wrap text-foreground/90 select-text [overflow-wrap:anywhere]" ref={preRef}>
					{shown.text}
				</pre>
			</div>
			{!following ? (
				<Button type="button" variant="secondary" size="xs" className="absolute right-3 bottom-3" onClick={jumpToLatest}>
					<ArrowDown aria-hidden="true" className="size-3" />
					{t("backgroundTasks.output.jumpToLatest")}
				</Button>
			) : null}
		</div>
	);
}
