import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../../lib/bridge";
import type { DictationEvent } from "../../../shared/dictation";

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

/** The composer whose dictation is running. Starting another one finishes it first. */
let owner: { end(keep: boolean): void } | null = null;

type DictationTarget = {
	/** Shows the transcript so far in place of the previous one. */
	onText(text: string): void;
	/** Ends the dictated run: kept as typed text, or removed. */
	onEnd(keep: boolean): void;
};

/**
 * Native macOS dictation for one composer. The transcript streams into the
 * composer while the user speaks; microphone levels go straight to the meter
 * (see DictationMeter) so they never re-render the composer.
 */
export function useDictation(target: DictationTarget) {
	const [phase, setPhase] = useState<DictationPhase>("idle");
	const [error, setError] = useState<string>();
	const targetRef = useRef(target);
	targetRef.current = target;
	const self = useRef<{ end(keep: boolean): void }>(null!);
	const owns = () => owner === self.current;
	// Listening only while this composer owns a dictation.
	const unsubscribe = useRef<(() => void) | null>(null);

	const end = useCallback((keep: boolean) => {
		if (owner === self.current) owner = null;
		unsubscribe.current?.();
		unsubscribe.current = null;
		setPhase("idle");
		targetRef.current.onEnd(keep);
	}, []);
	self.current ??= { end };

	const onEvent = useCallback((event: DictationEvent) => {
		switch (event.type) {
			case "listening":
				setPhase("listening");
				break;
			case "finishing":
				setPhase("finishing");
				break;
			case "text":
				targetRef.current.onText(event.text);
				break;
			case "end":
				targetRef.current.onText(event.text);
				end(true);
				break;
			case "error":
				setError(event.message);
				end(true);
				break;
			case "exit":
				setError("Dictation stopped unexpectedly.");
				end(true);
				break;
		}
	}, [end]);

	useEffect(() => {
		if (!error) return;
		const timer = window.setTimeout(() => setError(undefined), 6000);
		return () => window.clearTimeout(timer);
	}, [error]);

	/** Stops listening at once, keeping or dropping the dictated text. */
	const halt = useCallback((keep: boolean) => {
		if (!owns()) return;
		end(keep);
		void aoBridge.dictation.cancel();
	}, [end]);

	useEffect(() => () => {
		if (!owns()) return;
		owner = null;
		unsubscribe.current?.();
		void aoBridge.dictation.cancel();
	}, []);

	const start = useCallback(() => {
		// The main process replaces the running helper; the composer it belonged to keeps its words.
		owner?.end(true);
		owner = self.current;
		unsubscribe.current = aoBridge.dictation.onEvent(onEvent);
		setError(undefined);
		setPhase("starting");
		void aoBridge.dictation.start();
	}, [onEvent]);

	/** Ends the audio and waits for the recognizer's final wording. */
	const stop = useCallback(() => {
		if (!owns()) return;
		setPhase("finishing");
		void aoBridge.dictation.stop();
	}, []);

	useEffect(() => {
		if (phase === "idle") return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
			// Escape belongs to dictation while it runs, not to menus or the page behind.
			event.preventDefault();
			event.stopPropagation();
			stop();
		};
		window.addEventListener("keydown", onKeyDown, true);
		return () => window.removeEventListener("keydown", onKeyDown, true);
	}, [phase, stop]);

	const toggle = useCallback(() => {
		if (!owns()) start();
		else if (phase !== "finishing") stop();
	}, [phase, start, stop]);

	return {
		phase,
		error,
		toggle,
		detach: useCallback(() => halt(true), [halt]),
	};
}
