import { createContext, useCallback, useContext, useEffect, useReducer, useRef, useState } from "react";
import { AppState } from "react-native";
import { useReducedMotion } from "../useReducedMotion";
import {
	createReplyPacer,
	PACE_FIRST_PLAY_MAX,
	receiveReplyText,
	replyPending,
	replyVisibleText,
	showWholeReply,
	stepReplyPacer,
} from "./replyPacer";

/**
 * What the timeline shares with its replies' pacing. `progress` holds how much
 * of each live reply is shown, so a row the list recycles and draws again
 * carries on rather than replaying. `live` turns on once the conversation has
 * opened: a reply that first appears after that started streaming in front of
 * the reader and plays from its start; one already there when it opened is
 * history and is shown whole.
 */
export type ReplyPacing = { progress: Map<string, number>; live: { current: boolean } };

export const ReplyPacingContext = createContext<ReplyPacing | null>(null);

function initialShown(pacing: ReplyPacing | null, id: string, text: string, streaming: boolean): number {
	const kept = pacing?.progress.get(id);
	if (kept !== undefined && kept <= text.length) return kept;
	return streaming && pacing?.live.current && text.length <= PACE_FIRST_PLAY_MAX ? 0 : text.length;
}

/**
 * The part of a reply to draw this frame (see replyPacer). Only a streaming
 * reply is paced, and only while motion is welcome and the app is in front;
 * otherwise, and for every settled reply, it is the text itself.
 */
export function usePacedReply(id: string, text: string, streaming: boolean): string {
	const pacing = useContext(ReplyPacingContext);
	const reduceMotion = useReducedMotion();
	const [pacer] = useState(() => createReplyPacer(text, initialShown(pacing, id, text, streaming), streaming));
	const pacedId = useRef(id);
	const [, redraw] = useReducer((count: number) => count + 1, 0);
	const frame = useRef<number | undefined>(undefined);
	const cancel = useCallback(() => {
		if (frame.current !== undefined) cancelAnimationFrame(frame.current);
		frame.current = undefined;
	}, []);
	useEffect(() => cancel, [cancel]);

	useEffect(() => {
		const remember = () => {
			if (!pacing) return;
			if (!pacer.streaming && !replyPending(pacer)) pacing.progress.delete(pacedId.current);
			else pacing.progress.set(pacedId.current, pacer.shown);
		};
		const before = replyVisibleText(pacer);
		if (pacedId.current !== id) {
			pacedId.current = id;
			cancel();
			showWholeReply(pacer, text, streaming);
		} else if (reduceMotion || AppState.currentState !== "active" || (!streaming && !pacer.streaming && !replyPending(pacer))) {
			// Settled text that was never being paced (history, a revision) shows as it is.
			cancel();
			showWholeReply(pacer, text, streaming);
		} else {
			receiveReplyText(pacer, text, streaming, performance.now());
		}
		remember();
		if (replyVisibleText(pacer) !== before) redraw();
		// Nothing left to reveal (a final snapshot can replace the text whole): no frames.
		if (!replyPending(pacer)) {
			cancel();
			return;
		}
		if (frame.current !== undefined) return;
		const tick = () => {
			frame.current = undefined;
			// Frames stop while the app is away; what arrived meanwhile is shown, not replayed.
			if (AppState.currentState !== "active") showWholeReply(pacer, pacer.target);
			else if (!stepReplyPacer(pacer, performance.now())) {
				// A frame between paced updates; only while text is left to reveal.
				if (replyPending(pacer)) frame.current = requestAnimationFrame(tick);
				return;
			}
			remember();
			redraw();
			if (replyPending(pacer)) frame.current = requestAnimationFrame(tick);
		};
		frame.current = requestAnimationFrame(tick);
	}, [cancel, id, pacer, pacing, reduceMotion, streaming, text]);

	// Before the effect has heard of new text, the paced part is still its start.
	const visible = replyVisibleText(pacer);
	return text.startsWith(visible) ? visible : text;
}
