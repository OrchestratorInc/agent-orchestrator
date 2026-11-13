import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { dictationSoundsEnabled, playDictationSound, setDictationSoundsEnabled } from "./dictation-sound";

type Tone = { frequency: number; start: number; stop: number; peak: number };
const tones: Tone[] = [];

class FakeAudioContext {
	state = "running";
	currentTime = 10;
	destination = {};
	resume = vi.fn(async () => undefined);
	createOscillator() {
		const tone: Tone = { frequency: 0, start: 0, stop: 0, peak: 0 };
		tones.push(tone);
		const node = {
			type: "",
			frequency: { setValueAtTime: (value: number) => (tone.frequency = value) },
			connect: (next: unknown) => next,
			start: (at: number) => (tone.start = at),
			stop: (at: number) => (tone.stop = at),
		};
		(node as { tone?: Tone }).tone = tone;
		return node;
	}
	createGain() {
		const tone = tones[tones.length - 1]!;
		return {
			gain: {
				setValueAtTime: () => undefined,
				linearRampToValueAtTime: (value: number) => (tone.peak = value),
				exponentialRampToValueAtTime: () => undefined,
			},
			connect: (next: unknown) => next,
		};
	}
}

beforeEach(() => {
	tones.length = 0;
	localStorage.clear();
	vi.stubGlobal("AudioContext", FakeAudioContext);
});
afterEach(() => vi.unstubAllGlobals());

describe("dictation sounds", () => {
	it("opens with a rising pair of ticks and closes with a falling pair", () => {
		playDictationSound("on");
		// Each tick is a bright tap over a low thump.
		expect(tones.map((tone) => tone.frequency)).toEqual([1300, 140, 1700, 170]);
		tones.length = 0;
		playDictationSound("off");
		expect(tones.map((tone) => tone.frequency)).toEqual([1100, 130, 850, 105]);
	});

	it("stays inside the guidelines: ticks 40 ms apart, under 0.2 s in all, single-layer gain 0.04-0.30", () => {
		for (const kind of ["on", "off"] as const) {
			tones.length = 0;
			playDictationSound(kind);
			const first = tones[0]!;
			const secondTap = tones[2]!;
			expect(secondTap.start - first.start).toBeCloseTo(0.04, 5);
			expect(Math.max(...tones.map((tone) => tone.stop)) - first.start).toBeLessThan(0.2);
			for (const tone of tones) {
				expect(tone.peak).toBeGreaterThanOrEqual(0.04);
				expect(tone.peak).toBeLessThanOrEqual(0.3);
			}
		}
	});

	it("is on by default and silent once the user turns it off", () => {
		expect(dictationSoundsEnabled()).toBe(true);
		setDictationSoundsEnabled(false);
		expect(dictationSoundsEnabled()).toBe(false);
		playDictationSound("on");
		expect(tones).toHaveLength(0);
		setDictationSoundsEnabled(true);
		playDictationSound("on");
		expect(tones).toHaveLength(4);
	});

	it("keeps the choice for this session when storage cannot hold it", () => {
		const write = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		const read = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		try {
			setDictationSoundsEnabled(false);
			expect(dictationSoundsEnabled()).toBe(false);
			playDictationSound("on");
			expect(tones).toHaveLength(0);
			setDictationSoundsEnabled(true);
			expect(dictationSoundsEnabled()).toBe(true);
		} finally {
			write.mockRestore();
			read.mockRestore();
			// Storage works again: record a saved choice so no in-memory override lingers into other tests.
			setDictationSoundsEnabled(true);
		}
	});
	it("does nothing, without an error, when there is no audio support", () => {
		vi.stubGlobal("AudioContext", undefined);
		expect(() => playDictationSound("on")).not.toThrow();
	});
});
