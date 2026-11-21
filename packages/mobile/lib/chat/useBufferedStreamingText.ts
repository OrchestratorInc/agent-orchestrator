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
	graphemes: string[];
	visibleCount: number;
	frame?: number;
	lastFrameAt?: number;
	snapshotTimer?: ReturnType<typeof setTimeout>;
	lastGrowthAt?: number;
	gapMs?: number;
};

/** Pace the display of incoming snapshots. The first snapshot and finished replies are shown whole. */
export function useBufferedStreamingText(text: string, streaming: boolean, reducedMotion: boolean): string {
	const [visible, setVisible] = useState(text);
	const buffer = useRef<Buffer>({ visible: text, target: text, graphemes: [], visibleCount: 0 });
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
	}, []);
	useEffect(() => cancel, [cancel]);

	useEffect(() => {
		const current = buffer.current;
		const previousTarget = current.target;
		current.target = text;
		if (!streaming || reducedMotion || AppState.currentState === "background" || !text.startsWith(previousTarget)) {
			cancel();
			current.lastGrowthAt = undefined;
			current.gapMs = undefined;
			publish(text);
			return;
		}
		if (text === previousTarget && text === current.visible) return;

		const now = performance.now();
		if (text !== previousTarget) {
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
				current.lastFrameAt = undefined;
				publish(current.target);
				return;
			}
			const elapsed = current.lastFrameAt === undefined ? STREAM_MIN_FRAME_MS : frameAt - current.lastFrameAt;
			if (elapsed >= STREAM_MIN_FRAME_MS) {
				current.lastFrameAt = frameAt;
				const backlog = current.graphemes.length - current.visibleCount;
				const settle = current.gapMs === undefined ? STREAM_SETTLE_MS : streamSettleMs(current.gapMs);
				const count = streamRevealCount(backlog, Math.min(elapsed, STREAM_MAX_FRAME_DELTA_MS), settle);
				const nextCount = Math.min(current.graphemes.length, current.visibleCount + count);
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
