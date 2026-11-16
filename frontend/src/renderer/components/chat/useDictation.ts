import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../../lib/bridge";
import {
	createDictationController,
	type DictationController,
	type DictationPhase,
} from "../../../shared/dictation-controller";

export type { DictationPhase };

/** The composer whose dictation is running. Starting another one finishes it first. */
let owner: DictationController | null = null;

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
	const controller = useRef<DictationController>(null!);
	controller.current ??= createDictationController(aoBridge.dictation, {
		onPhase: setPhase,
		onText: (text) => targetRef.current.onText(text),
		onEnd: (keep) => {
			if (owner === controller.current) owner = null;
			targetRef.current.onEnd(keep);
		},
		onError: setError,
	});
	const owns = () => owner === controller.current;

	useEffect(() => {
		if (!error) return;
		const timer = window.setTimeout(() => setError(undefined), 6000);
		return () => window.clearTimeout(timer);
	}, [error]);

	const halt = useCallback((keep: boolean) => {
		if (owns()) controller.current.halt(keep);
	}, []);

	useEffect(() => () => {
		if (!owns()) return;
		owner = null;
		controller.current.dispose();
	}, []);

	const start = useCallback(() => {
		// The main process replaces the running helper; the composer it belonged to keeps its words.
		owner?.end(true);
		owner = controller.current;
		setError(undefined);
		controller.current.start();
	}, []);

	const stop = useCallback(() => {
		if (owns()) controller.current.stop();
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
