import { useCallback, useEffect, useRef, useState } from "react";
import { aoBridge } from "../../lib/bridge";

const LEVEL_COUNT = 28;
const silence = () => Array<number>(LEVEL_COUNT).fill(0);

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

/** Native macOS dictation for one composer; the recognized text goes to `onText` when it ends. */
export function useDictation(onText: (text: string) => void) {
	const [phase, setPhase] = useState<DictationPhase>("idle");
	const [transcript, setTranscript] = useState("");
	const [levels, setLevels] = useState(silence);
	const [error, setError] = useState<string>();
	const active = useRef(false);
	const deliver = useRef(onText);
	deliver.current = onText;

	const reset = useCallback(() => {
		active.current = false;
		setPhase("idle");
		setTranscript("");
		setLevels(silence());
	}, []);

	useEffect(() => aoBridge.dictation.onEvent((event) => {
		// Every composer hears the events; only the one that started acts on them.
		if (!active.current) return;
		switch (event.type) {
			case "listening":
				setPhase("listening");
				break;
			case "level":
				setLevels((previous) => [...previous.slice(1), event.value]);
				break;
			case "text":
				setTranscript(event.text);
				break;
			case "end":
				if (event.text.trim()) deliver.current(event.text.trim());
				reset();
				break;
			case "error":
				setError(event.message);
				reset();
				break;
			case "exit":
				setError("Dictation stopped unexpectedly.");
				reset();
				break;
		}
	}), [reset]);

	useEffect(() => {
		if (!error) return;
		const timer = window.setTimeout(() => setError(undefined), 6000);
		return () => window.clearTimeout(timer);
	}, [error]);

	const cancel = useCallback(() => {
		if (!active.current) return;
		reset();
		void aoBridge.dictation.cancel();
	}, [reset]);

	useEffect(() => {
		if (phase === "idle") return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") cancel();
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [cancel, phase]);

	useEffect(() => () => {
		if (active.current) void aoBridge.dictation.cancel();
	}, []);

	const start = useCallback(() => {
		active.current = true;
		setError(undefined);
		setTranscript("");
		setLevels(silence());
		setPhase("starting");
		void aoBridge.dictation.start();
	}, []);

	const stop = useCallback(() => {
		if (!active.current) return;
		setPhase("finishing");
		void aoBridge.dictation.stop();
	}, []);

	return { phase, transcript, levels, error, start, stop, cancel };
}
