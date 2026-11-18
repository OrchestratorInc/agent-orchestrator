import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ArrowDown } from "@/components/app-icons";
import { useBackgroundTaskOutput } from "../hooks/useBackgroundTasks";
import type { BackgroundTaskTab } from "../stores/ui-store";
import { lineTone, outputLines, outputSummary } from "../lib/output-lines";
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
	const isFailed = task.state === "failed" && !ended;
	const time = elapsed(task, now);
	const command = task.command && task.command !== name ? task.command : null;
	const text = output.data?.availability === "ready" ? output.data.output : "";
	const summary = useMemo(() => (text ? outputSummary(text) : null), [text]);

	// The tab names the task, so the header starts with how it is doing.
	return (
		<section aria-label={name} className="flex h-full min-h-0 flex-col">
			<header className="flex shrink-0 flex-col gap-0.5 border-b border-border pt-1.5 pr-2 pb-2.5 pl-3">
				<div className="flex h-control-md min-w-0 items-center gap-1.5 text-xs">
					{isFailed ? <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-destructive" /> : null}
					<span className={cn("font-medium", isFailed ? "text-destructive" : "text-foreground")}>{status}</span>
					{time ? <><span aria-hidden="true" className="text-muted-foreground">·</span><span className="text-muted-foreground tabular-nums">{time}</span></> : null}
					{failed === task.id ? <span role="status" className="text-destructive">· {t("backgroundTasks.stopFailed")}</span> : null}
					{live && task.stoppable ? (
						<Button
							type="button"
							variant="quiet"
							size="xs"
							className="ml-auto shrink-0"
							aria-label={t("backgroundTasks.stop", { name })}
							disabled={stopping.has(task.id)}
							onClick={() => stop(task.id)}
						>
							<span aria-hidden="true" className="size-2 rounded-[2px] bg-current" />
							{t("backgroundTasks.stopAction")}
						</Button>
					) : null}
				</div>
				{command || summary ? (
					<div className="flex min-w-0 items-baseline gap-3 pr-1">
						{command ? (
							<code className="board-scrollbar max-h-20 min-w-0 flex-1 overflow-y-auto font-mono text-xs leading-[18px] break-all whitespace-pre-wrap text-muted-foreground select-text">
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
			<div className="flex min-h-0 flex-1 flex-col">
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
	// selection keeps its line breaks.
	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div
				aria-label={label}
				className="board-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain py-1.5 outline-none focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
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
									tone === "warning" && "bg-(--color-term-yellow)/12 text-(--color-term-yellow)",
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
				<Button
					type="button"
					variant="ghost"
					size="none"
					className="h-8 w-full gap-1.5 rounded-none border-t border-border bg-card text-xs font-medium"
					onClick={jumpToLatest}
				>
					<ArrowDown aria-hidden="true" className="size-3" />
					{t("backgroundTasks.output.jumpToLatest")}
				</Button>
			) : null}
		</div>
	);
}
