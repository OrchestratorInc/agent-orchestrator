import { useCallback, useEffect, useRef, useState } from "react";
import { AppState } from "react-native";
import { graphemeSegments } from "unicode-segmenter/grapheme";
import {
	reconciledStreamPrefix,
	streamGapMs,
	STREAM_MIN_FRAME_MS,
	STREAM_SMOOTH_MAX_CHARS,
	STREAM_STALL_MS,
} from "../../../shared/chat/streamPacing";

type Buffer = {
	visible: string;
	target: string;
	graphemes: string[];
	visibleCount: number;
	deadline: number;
	frame?: number;
	lastFrameAt?: number;
	lastGrowthAt?: number;
	gapMs?: number;
};

const LONG_FRAME_MS = 48;
const FIRST_WINDOW_MS = 110;
const MIN_WINDOW_MS = 55;
const MAX_WINDOW_MS = 180;
const SPACE = /\s/u;

function revealEnd(graphemes: string[], start: number, count: number): number {
	const end = Math.min(start + count, graphemes.length);
	if (end === graphemes.length) return end;

	// Prefer a nearby word ending. Code and scripts without spaces fall back to
	// the earned grapheme boundary so a long token never holds up the stream.
	for (let distance = 0; distance <= 4; distance++) {
		const earlier = end - distance;
		if (earlier > start && SPACE.test(graphemes[earlier - 1])) return earlier;
		const later = end + distance;
		if (later <= graphemes.length && SPACE.test(graphemes[later - 1])) return later;
	}
	return end;
}

/** Pace only live snapshots. The terminal snapshot is always displayed whole. */
export function useBufferedStreamingText(text: string, streaming: boolean, reducedMotion: boolean, playFromStart = false): string {
	const [visible, setVisible] = useState(() => playFromStart && streaming && !reducedMotion && AppState.currentState === "active" ? "" : text);
	const buffer = useRef<Buffer>({ visible, target: text, graphemes: [], visibleCount: 0, deadline: 0 });
	const publish = useCallback((next: string) => {
		buffer.current.visible = next;
		setVisible(next);
	}, []);
	const cancel = useCallback(() => {
		const current = buffer.current;
		if (current.frame !== undefined) cancelAnimationFrame(current.frame);
		current.frame = undefined;
		current.lastFrameAt = undefined;
	}, []);
	useEffect(() => cancel, [cancel]);

	useEffect(() => {
		const current = buffer.current;
		const previousTarget = current.target;
		current.target = text;
		if (!streaming || reducedMotion || AppState.currentState === "background" || !text.startsWith(current.visible)) {
			cancel();
			current.lastGrowthAt = undefined;
			current.gapMs = undefined;
			publish(text);
			return;
		}
		if (text === previousTarget && text === current.visible) return;

		const now = performance.now();
		if (text !== previousTarget || current.lastGrowthAt === undefined) {
			if (current.lastGrowthAt !== undefined) current.gapMs = streamGapMs(current.gapMs, now - current.lastGrowthAt);
			current.lastGrowthAt = now;
		}
		const windowMs = current.gapMs === undefined ? FIRST_WINDOW_MS : Math.min(MAX_WINDOW_MS, Math.max(MIN_WINDOW_MS, current.gapMs * 0.7));
		// Shorten the remaining window as snapshots stack up. Keeping the earliest
		// deadline lets new output raise the speed without deferring older text.
		const newDeadline = now + windowMs;
		current.deadline = current.frame === undefined ? newDeadline : Math.min(current.deadline, newDeadline);
		current.graphemes = Array.from(graphemeSegments(text), ({ segment }) => segment);
		const prefix = reconciledStreamPrefix(current.visible, current.graphemes);
		current.visibleCount = prefix.count;
		if (prefix.text !== current.visible) publish(prefix.text);
		if (current.frame !== undefined || current.visibleCount >= current.graphemes.length) return;

		const tick = () => {
			current.frame = undefined;
			const nowAtFrame = performance.now();
			const previousFrame = current.lastFrameAt;
			if (AppState.currentState === "background" || nowAtFrame - (previousFrame ?? now) > STREAM_STALL_MS) {
				cancel();
				publish(current.target);
				return;
			}
			const interval = current.visible.length < STREAM_SMOOTH_MAX_CHARS ? STREAM_MIN_FRAME_MS : LONG_FRAME_MS;
			const elapsed = previousFrame === undefined ? interval : nowAtFrame - previousFrame;
			if (elapsed >= interval) {
				current.lastFrameAt = nowAtFrame;
				const backlog = current.graphemes.length - current.visibleCount;
				const timeLeft = current.deadline - nowAtFrame;
				const framesLeft = Math.max(1, 1 + Math.ceil(timeLeft / interval));
				const count = Math.max(1, Math.ceil(backlog / framesLeft));
				const nextCount = revealEnd(current.graphemes, current.visibleCount, count);
				const next = current.visible + current.graphemes.slice(current.visibleCount, nextCount).join("");
				current.visibleCount = nextCount;
				publish(next);
			}
			if (current.visibleCount < current.graphemes.length) current.frame = requestAnimationFrame(tick);
			else current.lastFrameAt = undefined;
		};
		current.frame = requestAnimationFrame(tick);
	}, [cancel, publish, reducedMotion, streaming, text]);

	return streaming && !reducedMotion && text.startsWith(visible) ? visible : text;
}
