import { useCallback, useEffect, useRef, useState } from "react";
import { dictationClient } from "../../lib/dictation-client";
import { playDictationSound } from "../../lib/dictation-sound";
import type { DictationEvent } from "../../../shared/dictation";

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

/** How long a recording that is finishing may hold up the next composer's start. */
const HANDOFF_TIMEOUT_MS = 8000;

type Owner = { end(keep: boolean): void; finish(): Promise<boolean> | null };

/** The composer whose dictation is running. Starting another one finishes it first. */
let owner: Owner | null = null;

type DictationTarget = {
	ownerKey?: string;
	onSubmit?(event: KeyboardEvent): boolean;
	/** Shows the finished transcript; interim words are never shown. */
	onText(text: string): void;
	/** Ends the dictated run: kept as typed text, or removed. */
	onEnd(keep: boolean): void;
};

/**
 * Local dictation for one composer. The transcript lands in the composer once,
 * when the user finishes; microphone levels go straight to the meter (see
 * DictationMeter) so they never re-render the composer. A short click marks
 * dictation opening and closing (see dictation-sound).
 */
export function useDictation(target: DictationTarget) {
	const [phase, setPhase] = useState<DictationPhase>("idle");
	const [error, setError] = useState<string>();
	const targetRef = useRef(target);
	targetRef.current = target;
	const self = useRef<Owner>(null!);
	/** Set while this composer waits for another one's recording to deliver its words. */
	const pendingStart = useRef(false);
	const owns = () => owner === self.current;
	// Listening only while this composer owns a dictation.
	const unsubscribe = useRef<(() => void) | null>(null);
	const waiting = useRef(new Set<(completed: boolean) => void>());
	const stopRequested = useRef(false);
	const generation = useRef(0);

	const end = useCallback((keep: boolean, completed = false) => {
		generation.current += 1;
		if (owner === self.current) owner = null;
		unsubscribe.current?.();
		unsubscribe.current = null;
		setPhase("idle");
		targetRef.current.onEnd(keep);
		for (const resolve of waiting.current) resolve(completed);
		waiting.current.clear();
	}, []);
	self.current ??= { end, finish: () => null };

	const onEvent = useCallback(
		(event: DictationEvent) => {
			switch (event.type) {
				case "listening":
					setPhase("listening");
					break;
				case "finishing":
					setPhase("finishing");
					break;
				case "text":
					// Interim words are not shown: the finished text arrives once, with "end".
					break;
				case "end":
					targetRef.current.onText(event.text);
					end(true, true);
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
		},
		[end],
	);

	useEffect(() => {
		if (!error) return;
		const timer = window.setTimeout(() => setError(undefined), 6000);
		return () => window.clearTimeout(timer);
	}, [error]);

	/** Cancels a start that is still waiting for another composer's recording to finish. */
	const cancelPendingStart = useCallback(() => {
		if (!pendingStart.current) return false;
		pendingStart.current = false;
		setPhase("idle");
		return true;
	}, []);

	/** Stops listening at once, keeping or dropping the dictated text. */
	const halt = useCallback(
		(keep: boolean) => {
			if (cancelPendingStart()) return;
			if (!owns()) return;
			end(keep);
			void dictationClient.cancel();
			playDictationSound("off");
		},
		[end, cancelPendingStart],
	);

	useEffect(() => {
		setPhase("idle");
		const onEnd = targetRef.current.onEnd;
		return () => {
			pendingStart.current = false;
			if (!owns()) return;
			generation.current += 1;
			for (const resolve of waiting.current) resolve(false);
			waiting.current.clear();
			owner = null;
			unsubscribe.current?.();
			onEnd(true);
			void dictationClient.cancel();
		};
	}, [target.ownerKey]);

	const begin = useCallback(() => {
		owner = self.current;
		stopRequested.current = false;
		const started = ++generation.current;
		unsubscribe.current = dictationClient.onEvent(onEvent);
		setError(undefined);
		setPhase("starting");
		// Played before the microphone opens, so it can never reach the recording.
		playDictationSound("on");
		void dictationClient.start().catch((error: unknown) => {
			if (generation.current !== started) return;
			setError(error instanceof Error ? error.message : "Could not start dictation.");
			end(true);
		});
	}, [onEvent, end]);

	const start = useCallback(() => {
		if (pendingStart.current) return;
		const previous = owner;
		if (previous && previous !== self.current) {
			// Another composer is still recording or finishing. Let it deliver its words first, so
			// they are not lost, then take over.
			const done = previous.finish();
			if (done) {
				pendingStart.current = true;
				setPhase("starting");
				const proceed = () => {
					if (!pendingStart.current) return;
					pendingStart.current = false;
					window.clearTimeout(fallback);
					if (owner === previous) previous.end(true);
					begin();
				};
				const fallback = window.setTimeout(proceed, HANDOFF_TIMEOUT_MS);
				void done.then(proceed);
				return;
			}
			previous.end(true);
		}
		begin();
	}, [begin]);

	/** Ends the audio and waits for the recognizer's final wording. */
	const stop = useCallback(() => {
		if (!owns() || stopRequested.current) return;
		stopRequested.current = true;
		const stopped = generation.current;
		setPhase("finishing");
		void dictationClient.stop().then(
			// Resolves once the microphone is released, so the click is never recorded.
			() => {
				if (generation.current === stopped) playDictationSound("off");
			},
			() => {
				if (generation.current !== stopped) return;
				setError("Could not finish dictation.");
				end(true);
				void dictationClient.cancel();
			},
		);
	}, [end]);

	const finish = useCallback((): Promise<boolean> | null => {
		if (!owns()) return null;
		const completion = new Promise<boolean>((resolve) => waiting.current.add(resolve));
		stop();
		return completion;
	}, [stop]);

	self.current.finish = finish;

	useEffect(() => {
		if (phase === "idle") return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Enter" && !event.isComposing && targetRef.current.onSubmit?.(event)) {
				event.preventDefault();
				event.stopPropagation();
				return;
			}
			if (event.key !== "Escape") return;
			// Escape belongs to dictation while it runs, not to menus or the page behind.
			event.preventDefault();
			event.stopPropagation();
			halt(false);
		};
		window.addEventListener("keydown", onKeyDown, true);
		return () => window.removeEventListener("keydown", onKeyDown, true);
	}, [phase, halt]);

	const toggle = useCallback(() => {
		if (cancelPendingStart()) return;
		if (!owns()) start();
		else if (phase !== "finishing") stop();
	}, [phase, start, stop, cancelPendingStart]);

	return {
		phase,
		error,
		toggle,
		finish,
		detach: useCallback(() => halt(true), [halt]),
	};
}
