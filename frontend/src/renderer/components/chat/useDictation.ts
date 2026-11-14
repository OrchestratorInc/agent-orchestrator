import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../../lib/bridge";
import type { DictationEvent } from "../../../shared/dictation";

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

let owner: { end(keep: boolean): void } | null = null;

type DictationTarget = {
	onText(text: string): void;
	onEnd(keep: boolean): void;
};

export function useDictation(target: DictationTarget) {
	const [phase, setPhase] = useState<DictationPhase>("idle");
	const [error, setError] = useState<string>();
	const targetRef = useRef(target);
	targetRef.current = target;
	const self = useRef<{ end(keep: boolean): void }>(null!);
	const owns = () => owner === self.current;
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
		owner?.end(true);
		owner = self.current;
		unsubscribe.current = aoBridge.dictation.onEvent(onEvent);
		setError(undefined);
		setPhase("starting");
		void aoBridge.dictation.start();
	}, [onEvent]);

	const stop = useCallback(() => {
		if (!owns()) return;
		setPhase("finishing");
		void aoBridge.dictation.stop();
	}, []);

	useEffect(() => {
		if (phase === "idle") return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
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
