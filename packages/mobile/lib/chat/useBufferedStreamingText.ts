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
	streaming: boolean;
	finishing: boolean;
	graphemes: string[];
	visibleCount: number;
	credit: number;
	rate: number;
	frame?: number;
	lastFrameAt?: number;
	lastGrowthAt?: number;
	gapMs?: number;
};

const LONG_FRAME_MS = 48;
const FIRST_GAP_MS = 450;
const MIN_SPREAD_MS = 250;
const FINISH_SPREAD_MS = 650;
const MIN_GRAPHEMES_PER_SECOND = 24;
const MAX_GRAPHEMES_PER_SECOND = 140;
const FINISH_MAX_GRAPHEMES_PER_SECOND = 220;

function needsDrain(visible: string, target: string): boolean {
	return target.length > visible.length && target.startsWith(visible);
}

/** Spread a snapshot over its arrival window, with a bounded rate when catching up. */
export function useBufferedStreamingText(text: string, streaming: boolean, reducedMotion: boolean, playFromStart = false): string {
	const [visible, setVisible] = useState(() => playFromStart && !reducedMotion && AppState.currentState === "active" ? "" : text);
	const buffer = useRef<Buffer>({
		visible, target: text, streaming, finishing: !streaming && needsDrain(visible, text),
		graphemes: [], visibleCount: 0, credit: 0, rate: MIN_GRAPHEMES_PER_SECOND,
	});
	const publish = useCallback((next: string) => {
		buffer.current.visible = next;
		setVisible(next);
	}, []);
	const cancel = useCallback(() => {
		const current = buffer.current;
		if (current.frame !== undefined) cancelAnimationFrame(current.frame);
		current.frame = undefined;
		current.lastFrameAt = undefined;
		current.credit = 0;
	}, []);
	useEffect(() => cancel, [cancel]);

	useEffect(() => {
		const current = buffer.current;
		const previousTarget = current.target;
		const wasStreaming = current.streaming;
		current.finishing = !streaming && (wasStreaming || current.finishing) && needsDrain(current.visible, text);
		current.streaming = streaming;
		current.target = text;
		if ((!streaming && !current.finishing) || reducedMotion || AppState.currentState === "background" || !text.startsWith(current.visible)) {
			cancel();
			current.finishing = false;
			current.lastGrowthAt = undefined;
			current.gapMs = undefined;
			publish(text);
			return;
		}
		if (text === previousTarget && text === current.visible) return;

		const now = performance.now();
		if (streaming && (text !== previousTarget || current.lastGrowthAt === undefined)) {
			if (current.lastGrowthAt !== undefined) current.gapMs = streamGapMs(current.gapMs, now - current.lastGrowthAt);
			current.lastGrowthAt = now;
		}
		current.graphemes = Array.from(graphemeSegments(text), ({ segment }) => segment);
		const prefix = reconciledStreamPrefix(current.visible, current.graphemes);
		current.visibleCount = prefix.count;
		if (prefix.text !== current.visible) publish(prefix.text);

		const remaining = current.graphemes.length - current.visibleCount;
		const spread = current.finishing ? FINISH_SPREAD_MS : Math.max(MIN_SPREAD_MS, current.gapMs ?? FIRST_GAP_MS);
		const limit = current.finishing ? FINISH_MAX_GRAPHEMES_PER_SECOND : MAX_GRAPHEMES_PER_SECOND;
		current.rate = Math.min(limit, Math.max(MIN_GRAPHEMES_PER_SECOND, remaining * 1000 / spread));
		if (current.frame !== undefined || remaining <= 0) return;

		// Keep one clock while there is pending text. New snapshots update the
		// target and its rate without restarting the frame or dumping its backlog.
		const tick = () => {
			current.frame = undefined;
			const frameAt = performance.now();
			if (AppState.currentState === "background" || frameAt - (current.lastFrameAt ?? now) > STREAM_STALL_MS) {
				cancel();
				current.finishing = false;
				publish(current.target);
				return;
			}
			const interval = current.visible.length < STREAM_SMOOTH_MAX_CHARS ? STREAM_MIN_FRAME_MS : LONG_FRAME_MS;
			const elapsed = current.lastFrameAt === undefined ? interval : frameAt - current.lastFrameAt;
			if (elapsed >= interval) {
				current.lastFrameAt = frameAt;
				const budget = current.credit + current.rate * interval / 1000;
				const count = Math.floor(budget);
				current.credit = budget - count;
				const nextCount = Math.min(current.graphemes.length, current.visibleCount + count);
				if (nextCount > current.visibleCount) {
					const next = current.visible + current.graphemes.slice(current.visibleCount, nextCount).join("");
					current.visibleCount = nextCount;
					publish(next);
				}
			}
			if (current.visibleCount < current.graphemes.length) current.frame = requestAnimationFrame(tick);
			else {
				current.lastFrameAt = undefined;
				current.finishing = false;
			}
		};
		current.frame = requestAnimationFrame(tick);
	}, [cancel, publish, reducedMotion, streaming, text]);

	const finishing = (buffer.current.streaming || buffer.current.finishing) && needsDrain(visible, text);
	return !reducedMotion && (streaming || finishing) && text.startsWith(visible) ? visible : text;
}
