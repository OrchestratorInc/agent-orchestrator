import { useSyncExternalStore } from "react";

/**
 * The cues that mark dictation opening and closing: two tiny tactile ticks, like
 * a physical switch. Each tick is a bright sine tap over a low thump; the pair
 * rises to open and falls to close. Built to the web-kits audio guidelines
 * (https://audio.raphaelsalaja.com): the tap recipe is a sine near 1300 Hz with
 * a 15 ms decay at gain 0.2, a toggle stays under 0.2 s (this lasts about
 * 0.1 s), and the envelope is percussive.
 *
 * Plain Web Audio, no library: a few oscillators do not justify a dependency.
 */
type Layer = { frequency: number; gain: number; decay: number; release: number };
type Tick = { at: number; layers: Layer[] };

const ATTACK_S = 0.002;
const TICK_GAP_S = 0.04;

/** A bright tap on top of a low thump. */
function tick(at: number, frequency: number, thump: number, gain: number): Tick {
	return {
		at,
		layers: [
			{ frequency, gain, decay: 0.015, release: 0.005 },
			{ frequency: thump, gain: gain * 0.6, decay: 0.05, release: 0.01 },
		],
	};
}

const SOUNDS = {
	on: [tick(0, 1300, 140, 0.2), tick(TICK_GAP_S, 1700, 170, 0.16)],
	off: [tick(0, 1100, 130, 0.18), tick(TICK_GAP_S, 850, 105, 0.14)],
} as const;

export type DictationSound = keyof typeof SOUNDS;

const STORAGE_KEY = "ao.dictation.sounds";
const listeners = new Set<() => void>();
/** The choice when storage cannot hold it, so the switch still works until reload. */
let unsaved: boolean | undefined;

export function dictationSoundsEnabled(): boolean {
	if (unsaved !== undefined) return unsaved;
	try {
		return window.localStorage.getItem(STORAGE_KEY) !== "off";
	} catch {
		return true;
	}
}

export function setDictationSoundsEnabled(enabled: boolean): void {
	try {
		window.localStorage.setItem(STORAGE_KEY, enabled ? "on" : "off");
		unsaved = undefined;
	} catch {
		// Storage can be unavailable in previews; the choice then lasts until reload.
		unsaved = enabled;
	}
	listeners.forEach((listener) => listener());
}

/** The "play sounds" preference for Settings. On unless the user turned it off. */
export function useDictationSounds(): [boolean, (enabled: boolean) => void] {
	const enabled = useSyncExternalStore(
		(listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
		dictationSoundsEnabled,
		() => true,
	);
	return [enabled, setDictationSoundsEnabled];
}

let context: AudioContext | undefined;

function audio(): AudioContext | undefined {
	if (typeof AudioContext === "undefined") return undefined;
	context ??= new AudioContext({ latencyHint: "interactive" });
	if (context.state === "suspended") void context.resume().catch(() => {});
	return context;
}

/** Plays the opening or closing cue. Never throws: a missing audio device is not worth an error. */
export function playDictationSound(kind: DictationSound): void {
	if (!dictationSoundsEnabled()) return;
	try {
		const ctx = audio();
		if (!ctx) return;
		const now = ctx.currentTime;
		for (const { at, layers } of SOUNDS[kind]) {
			for (const { frequency, gain, decay, release } of layers) {
				const start = now + at;
				const end = start + ATTACK_S + decay + release;
				const oscillator = ctx.createOscillator();
				const envelope = ctx.createGain();
				oscillator.type = "sine";
				oscillator.frequency.setValueAtTime(frequency, start);
				envelope.gain.setValueAtTime(0, start);
				envelope.gain.linearRampToValueAtTime(gain, start + ATTACK_S);
				envelope.gain.exponentialRampToValueAtTime(0.0001, end);
				oscillator.connect(envelope).connect(ctx.destination);
				oscillator.start(start);
				oscillator.stop(end);
			}
		}
	} catch {
		/* Playback is best-effort. */
	}
}
