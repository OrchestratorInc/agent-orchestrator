import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../../lib/bridge";

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

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
	const active = useRef(false);
	const targetRef = useRef(target);
	targetRef.current = target;

	const end = useCallback((keep: boolean) => {
		active.current = false;
		setPhase("idle");
		targetRef.current.onEnd(keep);
	}, []);

	useEffect(() => aoBridge.dictation.onEvent((event) => {
		// Every composer hears the events; only the one that started acts on them.
		if (!active.current) return;
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
	}), [end]);

	useEffect(() => {
		if (!error) return;
		const timer = window.setTimeout(() => setError(undefined), 6000);
		return () => window.clearTimeout(timer);
	}, [error]);

	/** Stops listening at once, keeping or dropping the dictated text. */
	const halt = useCallback((keep: boolean) => {
		if (!active.current) return;
		end(keep);
		void aoBridge.dictation.cancel();
	}, [end]);

	useEffect(() => () => {
		if (active.current) void aoBridge.dictation.cancel();
	}, []);

	const start = useCallback(() => {
		active.current = true;
		setError(undefined);
		setPhase("starting");
		void aoBridge.dictation.start();
	}, []);

	/** Ends the audio and waits for the recognizer's final wording. */
	const stop = useCallback(() => {
		if (!active.current) return;
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
		if (!active.current) start();
		else if (phase !== "finishing") stop();
	}, [phase, start, stop]);

	return {
		phase,
		error,
		toggle,
		detach: useCallback(() => halt(true), [halt]),
	};
}
