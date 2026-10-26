import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { haptics } from "../haptics";
import { getDeviceVoiceProvider } from "./deviceProvider";
import type { VoiceMode, VoiceProvider, VoiceState } from "./types";

// Dictation, decoupled from any screen. Owns permission, the recording
// lifecycle, the live partial transcript, and the gesture that distinguishes a
// hold from a double-tap; knows nothing about terminals, sessions, or how the
// text is eventually sent.

// A press shorter than this was a tap, not a hold.
const TAP_MS = 250;
// How long after a tap a second tap still counts as a double-tap.
const DOUBLE_TAP_MS = 300;
// Breathing room between aborting the tap's recording and starting the latched
// one, so the native recogniser isn't still tearing down and reporting "busy".
// Only ever paid on the double-tap path, which is not latency-sensitive.
const RESTART_DELAY_MS = 120;

export type UseVoiceInput = {
	state: VoiceState;
	/** Which gesture started the current recording. */
	mode: VoiceMode;
	/** Live text while recording. Empty otherwise. */
	partial: string;
	/** Last failure, or null. Cleared on the next recording. */
	error: string | null;
	/** Finger down on the mic key. */
	pressIn(): void;
	/** Finger up off the mic key. */
	pressOut(): void;
	/**
	 * Starts hands-free dictation, or finishes it when it is already running.
	 * The screen reader's way in: an activation has no hold to time and no
	 * second tap to wait for.
	 */
	toggleLatched(): void;
};

export function useVoiceInput({
	onTranscript,
	provider,
}: {
	onTranscript: (text: string) => void;
	/** Injectable for tests; defaults to the on-device recogniser. */
	provider?: VoiceProvider;
}): UseVoiceInput {
	const device = useMemo(() => provider ?? getDeviceVoiceProvider(), [provider]);

	const [state, setState] = useState<VoiceState>("idle");
	const [mode, setMode] = useState<VoiceMode>("push");
	const [partial, setPartial] = useState("");
	const [error, setError] = useState<string | null>(null);

	// Gesture bookkeeping. All refs: these are read and written inside touch
	// handlers and timers that resolve far faster than React re-renders.
	const modeRef = useRef<VoiceMode>("push");
	const pressStartRef = useRef(0);
	// Non-null while a tap is waiting to see if it becomes a double-tap.
	const tapWindowRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const restartRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	// True for the single press that switched us into latched mode, so its own
	// release doesn't immediately stop the recording it just started.
	const startedLatchedRef = useRef(false);

	// The ref is the authoritative phase, not a mirror of `state`. Press-in →
	// permission dialog → press-out all resolve faster than React re-renders, so
	// reading `state` inside those callbacks would see stale values. Always change
	// phase through `setPhase` so the two never diverge.
	const stateRef = useRef<VoiceState>("idle");
	const setPhase = useCallback((next: VoiceState) => {
		stateRef.current = next;
		setState(next);
	}, []);
	const onTranscriptRef = useRef(onTranscript);
	onTranscriptRef.current = onTranscript;
	// Guards against a permission dialog resolving after the component unmounted.
	const mountedRef = useRef(true);

	// Availability is a device fact, not a permission — check it once so the mic
	// key can hide entirely rather than failing on press.
	useEffect(() => {
		if (!device.available()) setPhase("unavailable");
	}, [device, setPhase]);

	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
			// Never leave the microphone open behind a closed screen. This is the
			// guarantee that a latched recording cannot outlive the terminal screen.
			if (tapWindowRef.current) clearTimeout(tapWindowRef.current);
			if (restartRef.current) clearTimeout(restartRef.current);
			modeRef.current = "push";
			device.abort();
		};
	}, [device]);

	// Backgrounding the app must also close the mic: iOS will interrupt the audio
	// session anyway, and leaving the UI stuck in `recording` is worse than
	// dropping a phrase the user walked away from.
	useEffect(() => {
		const sub = AppState.addEventListener("change", (next) => {
			if (next === "active") return;
			if (stateRef.current !== "recording" && stateRef.current !== "starting") return;
			device.abort();
			modeRef.current = "push";
			setMode("push");
			setPartial("");
			setPhase("idle");
		});
		return () => sub.remove();
	}, [device, setPhase]);

	const beginRecording = useCallback(
		(mode: VoiceMode) => {
		const current = stateRef.current;
		if (current === "unavailable" || current === "starting" || current === "recording") return;

		modeRef.current = mode;
		setMode(mode);
		setError(null);
		setPartial("");
		setPhase("starting");

		// Warm-up is the whole complaint ("keep holding..." lasting too long), and it
		// splits into two very different costs: our own permission round-trip, and
		// the native recogniser + audio-session startup. Timing them separately is
		// the only way to know which one to attack.
		const t0 = Date.now();
		let tPermission = 0;

		void (async () => {
			// Asked lazily on first use, so users who never touch the mic never see
			// a permission dialog.
			const granted = await device.requestPermission();
			tPermission = Date.now() - t0;
			if (!mountedRef.current) return;
			if (!granted) {
				setPhase("denied");
				setError("Microphone access is off. Enable it in Settings to dictate.");
				return;
			}

			// The finger may have lifted while the dialog was up — don't start a
			// recording nobody is holding.
			if (stateRef.current !== "starting") return;

			await device.start(
				{
				onReady: () => {
					if (__DEV__) {
						const total = Date.now() - t0;
						console.log(
							`[voice] warm-up ${total}ms (permission ${tPermission}ms, recogniser ${total - tPermission}ms) lang=${device.language() ?? "pending"}`,
						);
					}
					if (!mountedRef.current) return;
					// Only now is the microphone actually capturing. A short buzz is the
					// cue to start talking - the user is looking at the terminal, not at
					// the key, so a visual-only change would be missed.
					if (stateRef.current === "starting") {
						setPhase("recording");
						haptics.select();
					}
				},
				onPartial: (text) => {
					if (!mountedRef.current) return;
					setPartial(text);
				},
				onFinal: (text) => {
					modeRef.current = "push";
					if (!mountedRef.current) return;
					setMode("push");
					setPartial("");
					setPhase("idle");
					// An empty transcript means silence or a stray tap: a no-op, not an
					// error, and nothing should reach the composer.
					if (text) onTranscriptRef.current(text);
				},
				onError: (message) => {
					modeRef.current = "push";
					if (!mountedRef.current) return;
					setMode("push");
					setPartial("");
					setPhase("idle");
					setError(message);
				},
				},
				{ mode },
			);

			// Deliberately does NOT move to "recording" here. start() returning only
			// means the recogniser was asked to start; the mic is still warming up and
			// speech is dropped until the `onReady` callback above fires. Claiming
			// "Listening" at this point is what made short holds lose their first
			// words - the UI invited the user to talk before anything was capturing.
		})();
		},
		[device, setPhase],
	);

	// Ends whatever is running, keeping the transcript.
	const finish = useCallback(() => {
		const current = stateRef.current;
		if (current === "starting") {
			// Released before the recogniser was live — nothing to finalise.
			device.abort();
			setPhase("idle");
			setPartial("");
			return;
		}
		if (current !== "recording") return;
		// Stays in `recording` until the provider settles, so the UI doesn't flash
		// idle while the final result is still in flight.
		device.stop();
	}, [device, setPhase]);

	const pressIn = useCallback(() => {
		// While latched the key is a stop button — the finger is not holding
		// anything, so a press means "I'm done talking".
		if (modeRef.current === "latched" && stateRef.current !== "idle") {
			startedLatchedRef.current = false;
			finish();
			return;
		}

		// Second tap of a double-tap: go hands-free. This is the only path that gets
		// the Bluetooth-capable audio session, because the session is fixed at start
		// and cannot be upgraded mid-recording.
		if (tapWindowRef.current) {
			clearTimeout(tapWindowRef.current);
			tapWindowRef.current = null;
			startedLatchedRef.current = true;
			haptics.tap();
			// The tap's own recording was aborted a moment ago; give the native
			// recogniser time to finish tearing down before asking it to start again.
			restartRef.current = setTimeout(() => beginRecording("latched"), RESTART_DELAY_MS);
			return;
		}

		// Ordinary press. Start immediately and decide what it *was* on release —
		// waiting to disambiguate would add to a warm-up that is already the sore
		// point, and the overwhelming majority of presses are holds.
		startedLatchedRef.current = false;
		pressStartRef.current = Date.now();
		beginRecording("push");
	}, [beginRecording, finish]);

	const pressOut = useCallback(() => {
		// The press that armed latched mode; releasing it must not stop anything.
		if (startedLatchedRef.current) return;
		// Latched recording ignores the finger entirely.
		if (modeRef.current === "latched") return;

		if (Date.now() - pressStartRef.current < TAP_MS) {
			// A tap, not a hold. Throw away the sliver of audio and wait to see
			// whether a second tap follows.
			device.abort();
			setPhase("idle");
			setPartial("");
			tapWindowRef.current = setTimeout(() => {
				tapWindowRef.current = null;
			}, DOUBLE_TAP_MS);
			return;
		}

		finish();
	}, [device, finish, setPhase]);

	const toggleLatched = useCallback(() => {
		if (modeRef.current === "latched" && stateRef.current !== "idle") {
			finish();
			return;
		}
		if (tapWindowRef.current) {
			clearTimeout(tapWindowRef.current);
			tapWindowRef.current = null;
		}
		startedLatchedRef.current = false;
		beginRecording("latched");
	}, [beginRecording, finish]);

	return { state, mode, partial, error, pressIn, pressOut, toggleLatched };
}
