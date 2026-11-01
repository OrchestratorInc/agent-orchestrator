import { beforeEach, describe, expect, it, vi } from "vitest";

// A fake of the native module that lets a test drive the recogniser's event
// stream by hand. The provider's whole job is turning that ragged stream — several
// finals, a trailing `end`, errors that are really just silence — into exactly one
// outcome, so the events are what we need control over.
const listeners = new Map<string, ((e: unknown) => void)[]>();
const started: unknown[] = [];
const calls = { start: 0, stop: 0, abort: 0 };
let recognitionAvailable = true;
let permissionGranted = true;
// Counts native permission round-trips, which sit in front of the microphone start.
let permissionChecks = 0;
let supportedLocales = ["en-US", "en-GB", "en-IN"];
// iOS and Android segment differently, so the provider branches on platform.
let platformOS = "android";

vi.mock("react-native", () => ({
	Platform: {
		get OS() {
			return platformOS;
		},
	},
}));

function emit(event: string, payload?: unknown) {
	for (const l of [...(listeners.get(event) ?? [])]) l(payload);
}

vi.mock("expo-speech-recognition", () => ({
	AVAudioSessionCategory: { record: "record", playAndRecord: "playAndRecord" },
	AVAudioSessionCategoryOptions: { allowBluetooth: "allowBluetooth", defaultToSpeaker: "defaultToSpeaker" },
	AVAudioSessionMode: { default: "default", measurement: "measurement" },
	ExpoSpeechRecognitionModule: {
		addListener(event: string, cb: (e: unknown) => void) {
			const list = listeners.get(event) ?? [];
			list.push(cb);
			listeners.set(event, list);
			return {
				remove() {
					listeners.set(event, (listeners.get(event) ?? []).filter((x) => x !== cb));
				},
			};
		},
		start(options: unknown) {
			calls.start++;
			started.push(options);
		},
		stop() {
			calls.stop++;
		},
		abort() {
			calls.abort++;
		},
		isRecognitionAvailable: () => recognitionAvailable,
		getSupportedLocales: async () => ({ locales: supportedLocales, installedLocales: [] }),
		getPermissionsAsync: async () => {
			permissionChecks++;
			return { granted: permissionGranted };
		},
		requestPermissionsAsync: async () => {
			permissionChecks++;
			return { granted: permissionGranted };
		},
	},
}));

const { createDeviceVoiceProvider, voiceLevel } = await import("./deviceProvider");

function harness() {
	const onReady = vi.fn();
	const onPartial = vi.fn();
	const onFinal = vi.fn();
	const onError = vi.fn();
	return { cb: { onReady, onPartial, onFinal, onError }, onReady, onPartial, onFinal, onError };
}

const result = (transcript: string, isFinal: boolean) => ({
	isFinal,
	results: [{ transcript, confidence: 1, segments: [] }],
});

beforeEach(() => {
	listeners.clear();
	started.length = 0;
	calls.start = 0;
	calls.stop = 0;
	calls.abort = 0;
	recognitionAvailable = true;
	permissionGranted = true;
	permissionChecks = 0;
	supportedLocales = ["en-US", "en-GB", "en-IN"];
	platformOS = "android";
});

describe("availability and permission", () => {
	it("reports unavailable when the device has no recogniser", () => {
		recognitionAvailable = false;
		expect(createDeviceVoiceProvider().available()).toBe(false);
	});

	it("reports a declined permission rather than throwing", async () => {
		permissionGranted = false;
		await expect(createDeviceVoiceProvider().requestPermission()).resolves.toBe(false);
	});

	it("does not re-ask the OS once permission is granted", async () => {
		const p = createDeviceVoiceProvider();
		await p.requestPermission();
		await p.requestPermission();
		await p.requestPermission();

		// This call sits directly in front of the microphone starting. Repeating the
		// native round-trip on every press delays recording enough to swallow the
		// first words of a short phrase.
		expect(permissionChecks).toBe(1);
	});
});

describe("input level", () => {
	it("meters the input for the waveform as 0 to 1, from the recogniser's -2 to 10", async () => {
		const p = createDeviceVoiceProvider();
		const onLevel = vi.fn();
		await p.start({ ...harness().cb, onLevel });
		expect(started[0]).toMatchObject({ volumeChangeEventOptions: { enabled: true } });
		emit("volumechange", { value: 5 });
		emit("volumechange", { value: -2 });
		emit("volumechange", { value: 14 });
		expect(onLevel.mock.calls.map(([level]) => level)).toEqual([0.5, 0, 1]);
		expect(voiceLevel(2.5)).toBe(0.25);
	});

	it("stops metering once the session is abandoned", async () => {
		const p = createDeviceVoiceProvider();
		const onLevel = vi.fn();
		await p.start({ ...harness().cb, onLevel });
		p.abort();
		emit("volumechange", { value: 5 });
		expect(onLevel).not.toHaveBeenCalled();
	});
});

describe("warm-up", () => {
	it("signals readiness only when the recogniser actually starts capturing", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// start() has returned, but the microphone is still warming up. Telling the
		// user to speak now would lose the first words of the phrase.
		expect(h.onReady).not.toHaveBeenCalled();

		emit("start");
		expect(h.onReady).toHaveBeenCalledTimes(1);
	});

	it("does not signal readiness for a session already abandoned", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		p.abort();
		emit("start");

		expect(h.onReady).not.toHaveBeenCalled();
	});
});

describe("transcript delivery", () => {
	it("streams partials and delivers one final transcript", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("add a test", false));
		emit("result", result("add a test for pairing", false));
		expect(h.onPartial).toHaveBeenCalledTimes(2);
		expect(h.onPartial).toHaveBeenLastCalledWith("add a test for pairing");

		emit("result", result("add a test for pairing.", true));
		emit("end");
		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("add a test for pairing.");
	});

	it("delivers the transcript exactly once even when `end` repeats", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("ship it", true));
		emit("end");
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("ship it");
	});

	it("does not end the recording on a final result — the user may still be talking", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// A pause closes a segment in continuous mode. That is a segment boundary,
		// not the end of the recording; the finger is still on the key.
		emit("result", result("open the pull request", true));
		expect(h.onFinal).not.toHaveBeenCalled();

		emit("result", result("and rerun the tests", true));
		emit("end");

		// Both segments, in order. Settling on the first would have silently dropped
		// everything said after the pause.
		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("open the pull request and rerun the tests");
	});

	it("keeps banked segments while a later segment is still forming", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("fix the parser", true));
		emit("result", result("then", false));
		// The live readout must show everything heard so far, not just the segment
		// currently being recognised.
		expect(h.onPartial).toHaveBeenLastCalledWith("fix the parser then");

		emit("result", result("then commit", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("fix the parser then commit");
	});

	it("keeps earlier speech when Android rolls over to a new segment with no final", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// Android's onPartialResults just emits whatever the recogniser currently
		// has. After a pause it restarts from scratch with no isFinal in between,
		// so an unrelated partial is the only signal that a segment ended.
		emit("result", result("add a test", false));
		emit("result", result("add a test for the pairing flow", false));
		// ...user pauses, then keeps talking. Fresh partial, unrelated text.
		emit("result", result("then run", false));
		emit("result", result("then run the linter", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith(
			"add a test for the pairing flow then run the linter",
		);
	});

	it("banks the previous segment when Android announces a new utterance", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// onBeginningOfSpeech is the authoritative boundary: a new utterance is
		// starting, so whatever is held as a partial belongs to the previous one.
		emit("speechstart");
		emit("result", result("add tests", false));
		// Pause, then more speech. This is the case the text heuristic gets wrong —
		// same opening word AND longer — so it only works because of speechstart.
		emit("speechstart");
		emit("result", result("add documentation for the API", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("add tests add documentation for the API");
	});

	it("does not bank on speechstart on iOS, where partials are cumulative", async () => {
		platformOS = "ios";
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// iOS runs one recognition task and restates everything each time, so
		// banking on speechstart would duplicate rather than segment.
		emit("result", result("add test", false));
		emit("speechstart");
		emit("result", result("add test add documentation", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("add test add documentation");
	});

	// Regression: a long iOS dictation snowballed into
	// "How the How's the system system turning How is the system turning?".
	// iOS emits `isFinal` results periodically through one continuous task, and
	// each one restates the WHOLE recording. Banking them the way Android's
	// segments are banked re-added the entire phrase on every final.
	it("does not duplicate when iOS emits a cumulative final mid-recording", async () => {
		platformOS = "ios";
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("how is the system", false));
		// iOS finalises the task so far, restating everything — not a new segment.
		emit("result", result("how is the system turning", true));
		// ...and carries straight on, still cumulative.
		emit("result", result("how is the system turning right now", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("how is the system turning right now");
	});

	it("does not duplicate across several cumulative iOS finals", async () => {
		platformOS = "ios";
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("open the", true));
		emit("result", result("open the pull request", true));
		emit("result", result("open the pull request and merge it", true));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("open the pull request and merge it");
	});

	it("keeps the live readout cumulative-but-not-doubled on iOS", async () => {
		platformOS = "ios";
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("ship the", false));
		emit("result", result("ship the release", true));
		// The strip must not read "ship the release ship the release".
		expect(h.onPartial).toHaveBeenLastCalledWith("ship the release");
	});

	// iOS caps how long one recognition task may run. When it restarts, the new
	// task's results begin from scratch — that IS a real boundary, and dropping
	// it would lose everything said before the restart.
	it("preserves earlier speech across an iOS recognition-task restart", async () => {
		platformOS = "ios";
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("first half of the sentence", false));
		emit("result", result("first half of the sentence complete", true));
		// New task: unrelated opening, starts short.
		emit("result", result("second", false));
		emit("result", result("second half now", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("first half of the sentence complete second half now");
	});

	it("does not treat an in-place revision as a new segment", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// Recognisers rewrite words they already emitted as confidence improves.
		// Those revisions share an opening and must NOT be banked as separate
		// segments, or the transcript doubles up.
		emit("result", result("add a test", false));
		emit("result", result("add a test for pairing", false));
		emit("result", result("add attest for pairing flow", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("add attest for pairing flow");
	});

	it("does not duplicate a segment that ends with a final result", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// The final supersedes the partial it grew from; banking both would repeat it.
		emit("result", result("open the", false));
		emit("result", result("open the pull request", false));
		emit("result", result("open the pull request", true));
		emit("result", result("then merge it", false));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("open the pull request then merge it");
	});

	it("keeps the words already heard when the session ends without a final result", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("run the tests", false));
		// A silence timeout ends the session with no isFinal result. The user said
		// something; dropping it would be the worst outcome.
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("run the tests");
		expect(h.onError).not.toHaveBeenCalled();
	});

	it("reports an empty transcript rather than an error when nothing was said", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		// A stray press: the recogniser reports "no-speech", which is expected, not
		// a fault. The caller distinguishes it by the empty string.
		emit("error", { error: "no-speech", message: "No speech detected" });

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("");
		expect(h.onError).not.toHaveBeenCalled();
	});

	it("trims the transcript", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);
		emit("result", result("  hello world  ", true));
		emit("end");
		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("hello world");
	});
});

describe("failures", () => {
	it("surfaces a readable message for a real error", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("error", { error: "not-allowed", message: "denied" });

		expect(h.onError).toHaveBeenCalledTimes(1);
		expect(h.onError.mock.calls[0][0]).toMatch(/permission was denied/i);
		expect(h.onFinal).not.toHaveBeenCalled();
	});

	it("does not emit a transcript after an error", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("error", { error: "audio-capture", message: "mic failed" });
		// Listeners are gone, so a late event from the native side is inert.
		emit("result", result("too late", true));
		emit("end");

		expect(h.onFinal).not.toHaveBeenCalled();
		expect(h.onError).toHaveBeenCalledTimes(1);
	});
});

describe("lifecycle", () => {
	it("aborting delivers nothing and drops its listeners", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		p.abort();
		expect(calls.abort).toBe(1);

		// The native side emits "aborted" in response; it must not reach the caller
		// that already walked away.
		emit("error", { error: "aborted", message: "aborted" });
		emit("end");

		expect(h.onFinal).not.toHaveBeenCalled();
		expect(h.onError).not.toHaveBeenCalled();
	});

	it("does not leave the previous recording's listeners attached on restart", async () => {
		const p = createDeviceVoiceProvider();
		const first = harness();
		const second = harness();

		await p.start(first.cb);
		await p.start(second.cb);

		emit("result", result("second phrase", true));
		emit("end");

		expect(first.onFinal).not.toHaveBeenCalled();
		expect(second.onFinal).toHaveBeenCalledExactlyOnceWith("second phrase");
	});

	it("reports a synchronous start failure through onError", async () => {
		const mod = await import("expo-speech-recognition");
		const spy = vi
			.spyOn(mod.ExpoSpeechRecognitionModule, "start")
			.mockImplementationOnce(() => {
				throw new Error("mic busy");
			});

		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		expect(h.onError).toHaveBeenCalledExactlyOnceWith("mic busy");
		spy.mockRestore();
	});

	it("delivers the transcript even if the recogniser never emits `end` after stop", async () => {
		vi.useFakeTimers();
		try {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("deploy to staging", false));
			p.stop();
			// The recogniser dies silently: no `end`, no final result. Without the
			// watchdog the words would be stranded and the UI stuck recording.
			expect(h.onFinal).not.toHaveBeenCalled();

			vi.advanceTimersByTime(5000);
			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("deploy to staging");
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not double-deliver when `end` arrives after stop", async () => {
		vi.useFakeTimers();
		try {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("run the linter", false));
			p.stop();
			emit("end");
			vi.advanceTimersByTime(5000);

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("run the linter");
		} finally {
			vi.useRealTimers();
		}
	});

	it("requests push-to-talk options tuned for prompts", async () => {
		const p = createDeviceVoiceProvider();
		await p.start(harness().cb);

		const opts = started[0] as Record<string, unknown>;
		// Continuous: the finger decides when the phrase ends, not a silence timer.
		expect(opts.continuous).toBe(true);
		expect(opts.interimResults).toBe(true);
		expect(opts.addsPunctuation).toBe(true);
		expect(opts.contextualStrings).toContain("git");
	});

	it("uses the cheapest audio session for push-to-talk", async () => {
		const p = createDeviceVoiceProvider();
		await p.start(harness().cb);

		// The library's default (playAndRecord + measurement + allowBluetooth) costs
		// roughly a second to activate, largely on Bluetooth route negotiation and an
		// output path dictation never uses. Intolerable before a short phrase.
		const opts = started[0] as { iosCategory?: Record<string, unknown> };
		expect(opts.iosCategory?.category).toBe("record");
		expect(opts.iosCategory?.categoryOptions).toEqual([]);
	});

	it("buys the Bluetooth-capable audio session for latched dictation", async () => {
		const p = createDeviceVoiceProvider();
		await p.start(harness().cb, { mode: "latched" });

		// Here the warm-up is amortised over a long prompt, so it is worth paying for
		// a session that can route through a headset mic.
		const opts = started[0] as { iosCategory?: Record<string, unknown> };
		expect(opts.iosCategory?.category).toBe("playAndRecord");
		expect(opts.iosCategory?.categoryOptions).toContain("allowBluetooth");
	});
});
