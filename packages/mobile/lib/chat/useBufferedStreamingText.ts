import { useCallback, useEffect, useRef, useState } from "react";
import { AppState } from "react-native";
import { graphemeSegments } from "unicode-segmenter/grapheme";
import {
	reconciledStreamPrefix,
	streamGapMs,
	streamRevealCount,
	streamSettleMs,
	STREAM_LONG_UPDATE_MS,
	STREAM_MAX_FRAME_DELTA_MS,
	STREAM_MIN_FRAME_MS,
	STREAM_SETTLE_MS,
	STREAM_SMOOTH_MAX_CHARS,
	STREAM_STALL_MS,
} from "../../../shared/chat/streamPacing";

type Buffer = {
	visible: string;
	target: string;
	streaming: boolean;
	finishing: boolean;
	graphemes: string[];
	visibleCount: number;
	credit: number;
	frame?: number;
	lastFrameAt?: number;
	snapshotTimer?: ReturnType<typeof setTimeout>;
	lastGrowthAt?: number;
	gapMs?: number;
};

const MIN_SETTLE_MS = 260;
const FINISH_SETTLE_MS = 110;
const FINISH_MAX_BACKLOG = 480;
const BASE_GRAPHEMES_PER_SECOND = 50;
const MAX_GRAPHEMES_PER_SECOND = 140;

function canFinish(visible: string, target: string): boolean {
	const remaining = target.length - visible.length;
	return remaining > 0 && remaining <= FINISH_MAX_BACKLOG && target.length <= STREAM_SMOOTH_MAX_CHARS && target.startsWith(visible);
}

/** Pace new snapshots and drain a short final burst, as desktop Cloud chat does. */
export function useBufferedStreamingText(text: string, streaming: boolean, reducedMotion: boolean): string {
	const [visible, setVisible] = useState(text);
	const buffer = useRef<Buffer>({ visible: text, target: text, streaming, finishing: false, graphemes: [], visibleCount: 0, credit: 0 });
	const publish = useCallback((next: string) => {
		buffer.current.visible = next;
		setVisible(next);
	}, []);
	const cancel = useCallback(() => {
		const current = buffer.current;
		if (current.frame !== undefined) cancelAnimationFrame(current.frame);
		if (current.snapshotTimer !== undefined) clearTimeout(current.snapshotTimer);
		current.frame = undefined;
		current.snapshotTimer = undefined;
		current.lastFrameAt = undefined;
		current.credit = 0;
	}, []);
	useEffect(() => cancel, [cancel]);

	useEffect(() => {
		const current = buffer.current;
		const previousTarget = current.target;
		current.finishing = !streaming && (current.streaming || current.finishing) && canFinish(current.visible, text);
		current.streaming = streaming;
		current.target = text;
		if ((!streaming && !current.finishing) || reducedMotion || AppState.currentState === "background" || !text.startsWith(previousTarget)) {
			cancel();
			current.finishing = false;
			current.lastGrowthAt = undefined;
			current.gapMs = undefined;
			publish(text);
			return;
		}
		if (text === previousTarget && text === current.visible) return;

		const now = performance.now();
		if (streaming && text !== previousTarget) {
			if (current.lastGrowthAt !== undefined) current.gapMs = streamGapMs(current.gapMs, now - current.lastGrowthAt);
			current.lastGrowthAt = now;
		}
		if (text.length > STREAM_SMOOTH_MAX_CHARS) {
			if (current.frame !== undefined) cancel();
			current.graphemes = [];
			if (current.visible.length <= STREAM_SMOOTH_MAX_CHARS) publish(text);
			else if (current.snapshotTimer === undefined) {
				current.snapshotTimer = setTimeout(() => {
					current.snapshotTimer = undefined;
					publish(current.target);
				}, STREAM_LONG_UPDATE_MS);
			}
			return;
		}

		current.graphemes = Array.from(graphemeSegments(text), ({ segment }) => segment);
		const prefix = reconciledStreamPrefix(current.visible, current.graphemes);
		current.visibleCount = prefix.count;
		if (prefix.text !== current.visible) publish(prefix.text);
		if (current.frame !== undefined || current.visibleCount === current.graphemes.length) return;

		const tick = () => {
			current.frame = undefined;
			const frameAt = performance.now();
			if (AppState.currentState === "background" || frameAt - (current.lastFrameAt ?? now) > STREAM_STALL_MS) {
				cancel();
				current.finishing = false;
				publish(current.target);
				return;
			}
			const elapsed = current.lastFrameAt === undefined ? STREAM_MIN_FRAME_MS : frameAt - current.lastFrameAt;
			if (elapsed >= STREAM_MIN_FRAME_MS) {
				current.lastFrameAt = frameAt;
				const backlog = current.graphemes.length - current.visibleCount;
				const settle = current.finishing ? FINISH_SETTLE_MS : Math.max(MIN_SETTLE_MS, current.gapMs === undefined ? STREAM_SETTLE_MS : streamSettleMs(current.gapMs));
				const delta = Math.min(elapsed, STREAM_MAX_FRAME_DELTA_MS);
				const reveal = streamRevealCount(backlog, delta, settle);
				// Keep large snapshots from revealing a whole line in the first frame.
				// A bigger backlog catches up faster while the cap keeps it readable.
				const rate = Math.min(MAX_GRAPHEMES_PER_SECOND, Math.max(BASE_GRAPHEMES_PER_SECOND, backlog));
				const budget = current.credit + rate * delta / 1000;
				const count = current.finishing ? reveal : Math.min(reveal, Math.floor(budget));
				current.credit = budget % 1;
				const nextCount = Math.min(current.graphemes.length, current.visibleCount + count);
				const next = current.visible + current.graphemes.slice(current.visibleCount, nextCount).join("");
				current.visibleCount = nextCount;
				publish(next);
			}
			if (current.visibleCount < current.graphemes.length) current.frame = requestAnimationFrame(tick);
			else {
				current.lastFrameAt = undefined;
				current.finishing = false;
			}
		};
		current.frame = requestAnimationFrame(tick);
	}, [cancel, publish, reducedMotion, streaming, text]);

	const finishing = (buffer.current.streaming || buffer.current.finishing) && canFinish(visible, text);
	return !reducedMotion && (streaming || finishing) && text.startsWith(visible) ? visible : text;
}
