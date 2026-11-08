// The seam between the mic UI and whatever actually turns speech into text.
//
// Only an on-device recogniser ships today (see `deviceProvider.ts`), but the
// interface is deliberately shaped so a cloud provider — Wispr Flow, Deepgram —
// is an additive file rather than a rewrite. The mic is always the phone's and
// never the harness's — see the note in `app/session/[id].tsx` for why.

export type VoiceState =
	| "idle"
	| "starting"
	| "recording"
	// Audio captured, text not back yet. Unreachable for the on-device provider
	// (which streams), but present from day one so a later batch/cloud provider —
	// which does have a gap between release and text — needs no UI change.
	| "transcribing"
	| "denied"
	| "unavailable";

/**
 * How a recording was started, which decides the audio session it gets.
 *
 * Every mic in the app now starts `latched`, on a tap: holding a key to talk
 * read as broken whenever the warm-up outlasted a short press. `push` stays for
 * the hook's hold gesture (`pressIn`/`pressOut`).
 *
 * `push` — hold the key. Optimised for the shortest possible time-to-capture,
 * because a ~1s warm-up is intolerable before a three-second phrase. Uses the
 * bare `record` category: built-in mic, no Bluetooth route negotiation.
 *
 * `latched` — tap, hands-free until tapped again. Here the warm-up is
 * amortised over a long dictation and stops mattering, so this one pays for the
 * richer session that can route through a Bluetooth headset.
 */
export type VoiceMode = "push" | "latched";

export type VoiceCallbacks = {
	/**
	 * The microphone is actually capturing. Fired after a warm-up that can run to
	 * several hundred milliseconds, so it — not the return of `start()` — is the
	 * moment the UI may tell the user to speak. Anything said before this is lost.
	 */
	onReady(): void;
	/** Live, not-yet-final text. A batch provider simply never calls this. */
	onPartial(text: string): void;
	/** The finished transcript. Called at most once per `start`. */
	onFinal(text: string): void;
	/** Human-readable failure. The provider is back to idle once this fires. */
	onError(message: string): void;
	/**
	 * How loud the input is right now, 0 (silence) to 1, a few times a second
	 * while capturing. Drives the composer's waveform; a provider without a
	 * meter simply never calls it.
	 */
	onLevel?(level: number): void;
};

export interface VoiceProvider {
	readonly id: "device" | "cloud";
	/** False when this device/build can't transcribe at all — the mic key hides. */
	available(): boolean;
	/** Resolved recognition locale, or null before it has been determined. */
	language(): string | null;
	/** Prompts on first call. Resolves false if the user declined. */
	requestPermission(): Promise<boolean>;
	start(cb: VoiceCallbacks, opts?: { mode?: VoiceMode }): Promise<void>;
	/** Finish and emit a final result. */
	stop(): void;
	/** Discard — no final result. Used on unmount/blur. */
	abort(): void;
}
