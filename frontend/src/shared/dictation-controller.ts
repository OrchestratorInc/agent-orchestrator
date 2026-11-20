import type { DictationMessage } from "./dictation";
import type { DictationTransport } from "./dictation-ipc";

export type DictationPhase = "idle" | "starting" | "listening" | "finishing";

/** What a surface does with its dictation: the chat composer (useDictation) or the annotation composer. */
type DictationSurface = {
	onPhase(phase: DictationPhase): void;
	/** Shows the transcript so far in place of the previous one. */
	onText(text: string): void;
	/** The run ended: its words kept as typed text, or removed. */
	onEnd(keep: boolean): void;
	/** The helper failed; the run has already ended. */
	onError(message: string): void;
};

export type DictationController = {
	readonly phase: DictationPhase;
	start(): void;
	/** Ends the audio and waits for the recognizer's final wording. */
	stop(): void;
	/** Ends the run here, without telling main (which already replaced the helper). */
	end(keep?: boolean): void;
	/** Stops listening at once, keeping or dropping the dictated text. */
	halt(keep?: boolean): void;
	/** Cancels a running dictation for a surface that is going away. */
	dispose(): void;
};

/** Numbers each dictation this renderer starts; main tags the run's events with it. */
let lastRun = 0;

/** One run at a time for one surface, listening only while it runs. */
export function createDictationController(transport: DictationTransport, surface: DictationSurface): DictationController {
	let phase: DictationPhase = "idle";
	let run = 0;
	let unsubscribe: (() => void) | null = null;

	const setPhase = (next: DictationPhase) => {
		phase = next;
		surface.onPhase(next);
	};
	const release = () => {
		unsubscribe?.();
		unsubscribe = null;
	};
	const end = (keep = true) => {
		if (phase === "idle") return;
		release();
		run = 0;
		setPhase("idle");
		surface.onEnd(keep);
	};

	const onEvent = (event: DictationMessage) => {
		// A late event from an earlier run, possibly another surface's.
		if (event.run !== run) return;
		switch (event.type) {
			case "listening":
			case "finishing":
				setPhase(event.type);
				break;
			case "text":
				surface.onText(event.text);
				break;
			case "end":
				surface.onText(event.text);
				end();
				break;
			case "error":
				end();
				surface.onError(event.message);
				break;
			case "exit":
				end();
				surface.onError("Dictation stopped unexpectedly.");
				break;
			case "superseded":
				end();
				break;
		}
	};

	return {
		get phase() {
			return phase;
		},
		start() {
			if (phase !== "idle") return;
			const started = ++lastRun;
			run = started;
			unsubscribe = transport.onEvent(onEvent);
			setPhase("starting");
			// Main refuses a surface that may not dictate now (a hidden tab, say) and
			// sends nothing for it, so end the run here rather than wait.
			const refused = () => {
				if (run === started) end();
			};
			transport.start(started).then((ok) => ok || refused(), refused);
		},
		stop() {
			if (phase === "idle" || phase === "finishing") return;
			setPhase("finishing");
			void transport.stop();
		},
		end,
		halt(keep = true) {
			if (phase === "idle") return;
			end(keep);
			void transport.cancel();
		},
		dispose() {
			if (phase === "idle") return;
			release();
			run = 0;
			phase = "idle";
			void transport.cancel();
		},
	};
}
