import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
// Swappable so a test can hold the locale lookup open, or make it fail.
let localeLookup = async () => ({ locales: supportedLocales, installedLocales: [] as string[] });
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
		getSupportedLocales: () => localeLookup(),
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

// A result carrying native audio timings, as iOS reports them per segment.
const timed = (transcript: string, isFinal: boolean, startTimeMillis: number, endTimeMillis: number) => ({
	isFinal,
	results: [
		{ transcript, confidence: 1, segments: [{ startTimeMillis, endTimeMillis, segment: transcript.trim(), confidence: 1 }] },
	],
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
	localeLookup = async () => ({ locales: supportedLocales, installedLocales: [] });
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

	describe("iOS: revisions of an utterance replace, later utterances append", () => {
		beforeEach(() => {
			platformOS = "ios";
		});

		// Regression (#8159): the recogniser re-spells its opening word as context
		// arrives. Treating a changed first word as a new segment banked each
		// revision, producing "How the How's the system system turning How is ...".
		it("replaces when the opening word is revised", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("How the", false));
			emit("result", result("How's the system", false));
			emit("result", result("How's the system turning", false));
			emit("result", result("How is the system turning?", true));
			emit("end");

			expect(h.onPartial.mock.calls.map(([t]) => t)).toEqual([
				"How the",
				"How's the system",
				"How's the system turning",
				"How is the system turning?",
			]);
			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("How is the system turning?");
		});

		it("replaces overlapping partials rather than appending them", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("run the", false));
			emit("result", result("run the tests in", false));
			emit("result", result("run the tests in the mobile package", false));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("run the tests in the mobile package");
		});

		it("replaces with a shorter revision, even one that starts differently", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("add a test for the pairing", false));
			emit("result", result("attest", false));
			expect(h.onPartial).toHaveBeenLastCalledWith("attest");
			emit("result", result("add attest for pairing", false));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("add attest for pairing");
		});

		it("replaces when punctuation and capitalisation are revised", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("ok ship it", false));
			emit("result", result("OK, ship it.", false));
			emit("result", result("Okay. Ship it!", true));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("Okay. Ship it!");
		});

		it("does not repeat text across repeated finals of the same task", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("merge the branch", true));
			// The library prefixes later iOS 18 transcripts with a space.
			emit("result", result(" merge the branch", true));
			emit("result", result(" merge the branch.", true));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("merge the branch.");
		});

		it("does not let a trailing empty final wipe the transcript", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("deploy to staging", true));
			emit("result", { isFinal: true, results: [] });
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("deploy to staging");
		});

		// Deduplicating text would eat this; replacing keeps exactly what was said.
		it("keeps intentional repetition", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("go", false));
			emit("result", result("go go", false));
			emit("result", result("go go go", false));
			emit("result", result("go go go. Test it, test it again.", true));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("go go go. Test it, test it again.");
		});

		// A genuine new native task only exists across start() calls. The next
		// recording starts clean and nothing leaks from the previous one.
		it("starts fresh on a genuine new recognition task", async () => {
			const p = createDeviceVoiceProvider();
			const first = harness();
			await p.start(first.cb);
			emit("result", result("first recording", true));
			emit("end");

			const second = harness();
			await p.start(second.cb);
			emit("result", result("second", false));
			expect(second.onPartial).toHaveBeenLastCalledWith("second");
			emit("result", result("second recording", true));
			emit("end");

			expect(first.onFinal).toHaveBeenCalledExactlyOnceWith("first recording");
			expect(second.onFinal).toHaveBeenCalledExactlyOnceWith("second recording");
			expect(calls.start).toBe(2);
		});

		// Reproduced by review: two iOS 18 final-like results covering disjoint audio
		// are two utterances. Replacing would drop the first.
		it("appends a final whose audio starts after the previous final's", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("first phrase", true, 0, 1000));
			emit("result", timed("second phrase", true, 2000, 3000));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("first phrase second phrase");
		});

		it("streams a new utterance after the closed one, with the library's leading space", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("How the", false, 0, 600));
			emit("result", timed("How is the build?", true, 0, 1200));
			emit("result", timed(" Ship", false, 2000, 2300));
			expect(h.onPartial).toHaveBeenLastCalledWith("How is the build? Ship");
			emit("result", timed(" Ship it", false, 2000, 2600));
			emit("result", timed(" Ship it now.", true, 2000, 3000));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("How is the build? Ship it now.");
		});

		it("replaces revisions and repeated finals that overlap the same audio", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("How the", true, 0, 600));
			emit("result", timed("How's the system", true, 0, 1100));
			emit("result", timed("How is the system turning", false, 0, 1500));
			emit("result", timed("How is the system turning?", true, 0, 1600));
			emit("result", timed(" How is the system turning?", true, 0, 1600));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("How is the system turning?");
		});

		// Reproduced by review: a cumulative final restating audio already banked
		// must supersede it, not sit beside it.
		it("replaces banked utterances that a cumulative revision covers", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("first phrase", true, 0, 1000));
			emit("result", timed("second phrase", true, 2000, 3000));
			emit("result", timed("first phrase second phrase", true, 0, 3000));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("first phrase second phrase");
		});

		it("keeps earlier speech that a revision does not cover", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("open the PR", true, 0, 1000));
			emit("result", timed("run the tests", true, 2000, 3000));
			emit("result", timed("then merge", true, 4000, 5000));
			// Restates the last two utterances only.
			emit("result", timed("run the tests, then merge it", true, 2000, 5200));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("open the PR run the tests, then merge it");
		});

		it("revises earlier banked speech in place without touching later speech", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("open the bee are", true, 0, 1000));
			emit("result", timed("run the tests", true, 2000, 3000));
			emit("result", timed("open the PR", true, 0, 1000));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("open the PR run the tests");
		});

		it("keeps identical words spoken at different times", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("test it", true, 0, 800));
			emit("result", timed(" test it", true, 1500, 2300));
			emit("result", timed(" test it", true, 1500, 2300));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("test it test it");
		});

		it("needs a final to close an utterance, not just a gap in the audio", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("deploy", false, 0, 500));
			emit("result", timed("deploy it", false, 900, 1400));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("deploy it");
		});

		// Interim results can arrive with zeroed timings. That is no evidence of a
		// new utterance, so the closed one is held until a timed result decides.
		it("banks a closed utterance once timing proves a gap, across untimed results", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("first phrase", true, 0, 1000));
			emit("result", timed(" second", false, 0, 0));
			emit("result", timed(" second phrase", true, 2000, 3000));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("first phrase second phrase");
		});

		it("drops the closed utterance when a timed result shows it was revised", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", timed("first face", true, 0, 1000));
			emit("result", result(" first phase", false));
			emit("result", timed("first phrase, then", true, 0, 1500));
			emit("result", timed(" more", true, 2000, 2400));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("first phrase, then more");
		});

		// Conservative fallback: with no timings at all there is no native evidence
		// of a boundary, so nothing is guessed from the text.
		it("replaces when no result carries timings", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("first phrase", true));
			emit("result", result(" second phrase", true));
			emit("end");

			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("second phrase");
		});

		it("delivers the latest result when stopped mid-revision", async () => {
			const p = createDeviceVoiceProvider();
			const h = harness();
			await p.start(h.cb);

			emit("result", result("How the", false));
			emit("result", result("How's the build", false));
			p.stop();
			emit("end");

			expect(calls.stop).toBe(1);
			expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("How's the build");
		});
	});

	// Android is untouched: a segment that repeats the previous one's words is
	// still kept, because a final result really does close a segment there.
	it("keeps an Android segment that repeats the previous one", async () => {
		const p = createDeviceVoiceProvider();
		const h = harness();
		await p.start(h.cb);

		emit("result", result("test it", true));
		emit("result", result("test it", true));
		emit("end");

		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("test it test it");
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

describe("locale resolution before start", () => {
	// The resolved locale is cached for the process, so each test needs a module
	// that has never resolved one.
	async function freshProvider() {
		vi.resetModules();
		const mod = await import("./deviceProvider");
		return mod.createDeviceVoiceProvider();
	}

	function deferred<T>() {
		let resolve!: (v: T) => void;
		let reject!: (e: unknown) => void;
		const promise = new Promise<T>((res, rej) => {
			resolve = res;
			reject = rej;
		});
		return { promise, resolve, reject };
	}

	const listenerCount = () => [...listeners.values()].reduce((n, l) => n + l.length, 0);

	function deviceLocale(locale: string) {
		vi.spyOn(Intl.DateTimeFormat.prototype, "resolvedOptions").mockReturnValue({
			locale,
		} as Intl.ResolvedDateTimeFormatOptions);
	}

	afterEach(() => {
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	it("waits for the device's locale instead of starting the first phrase in en-US", async () => {
		deviceLocale("en-IN");
		const lookup = deferred<{ locales: string[]; installedLocales: string[] }>();
		localeLookup = () => lookup.promise;
		const p = await freshProvider();

		const starting = p.start(harness().cb);
		await Promise.resolve();
		expect(calls.start).toBe(0);

		lookup.resolve({ locales: ["en-US", "en-IN"], installedLocales: [] });
		await starting;

		expect(calls.start).toBe(1);
		expect(started[0]).toMatchObject({ lang: "en-IN" });
		expect(p.language()).toBe("en-IN");
	});

	it("falls back to the same language in another region before en-US", async () => {
		deviceLocale("en-AU");
		supportedLocales = ["de-DE", "en-GB", "fr-FR"];
		const p = await freshProvider();
		await p.start(harness().cb);
		expect(started[0]).toMatchObject({ lang: "en-GB" });

		// Underscore device locales match too.
		deviceLocale("fr_CA");
		const q = await freshProvider();
		await q.start(harness().cb);
		expect(started[1]).toMatchObject({ lang: "fr-FR" });
	});

	it("uses en-US when the lookup fails, and retries on the next press", async () => {
		deviceLocale("en-GB");
		localeLookup = async () => {
			throw new Error("recognition service unavailable");
		};
		const p = await freshProvider();
		const h = harness();
		await p.start(h.cb);

		expect(started[0]).toMatchObject({ lang: "en-US" });
		expect(h.onError).not.toHaveBeenCalled();
		// A failure is not a resolution; it must not pin en-US for the process.
		expect(p.language()).toBeNull();
		emit("end");

		localeLookup = async () => ({ locales: ["en-US", "en-GB"], installedLocales: [] });
		await p.start(harness().cb);
		expect(started[1]).toMatchObject({ lang: "en-GB" });
	});

	it("uses en-US for this phrase when the lookup never answers", async () => {
		// Only timers: faking Intl as well would bypass the device-locale stub.
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		deviceLocale("en-GB");
		localeLookup = () => new Promise(() => {});
		const p = await freshProvider();

		const starting = p.start(harness().cb);
		await vi.advanceTimersByTimeAsync(1999);
		expect(calls.start).toBe(0);
		await vi.advanceTimersByTimeAsync(1);
		await starting;

		expect(started[0]).toMatchObject({ lang: "en-US" });
	});

	it("retries after a timed-out lookup, and ignores that lookup's late answer", async () => {
		// Only timers: faking Intl as well would bypass the device-locale stub.
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		deviceLocale("en-GB");
		const hung = deferred<{ locales: string[]; installedLocales: string[] }>();
		localeLookup = () => hung.promise;
		const p = await freshProvider();

		const first = p.start(harness().cb);
		await vi.advanceTimersByTimeAsync(2000);
		await first;
		expect(started[0]).toMatchObject({ lang: "en-US" });
		emit("end");

		// The next press asks again instead of waiting on the hung lookup.
		localeLookup = async () => ({ locales: ["en-US", "en-GB"], installedLocales: [] });
		await p.start(harness().cb);
		expect(started[1]).toMatchObject({ lang: "en-GB" });
		emit("end");

		// The abandoned lookup finally answers; it must not overwrite the newer one.
		hung.resolve({ locales: ["en-US"], installedLocales: [] });
		await vi.advanceTimersByTimeAsync(0);
		expect(p.language()).toBe("en-GB");
		await p.start(harness().cb);
		expect(started[2]).toMatchObject({ lang: "en-GB" });
	});

	it("retries after the native lookup throws synchronously", async () => {
		deviceLocale("en-GB");
		localeLookup = () => {
			throw new Error("module not ready");
		};
		const p = await freshProvider();
		await p.start(harness().cb);
		expect(started[0]).toMatchObject({ lang: "en-US" });
		emit("end");

		localeLookup = async () => ({ locales: ["en-GB"], installedLocales: [] });
		await p.start(harness().cb);
		expect(started[1]).toMatchObject({ lang: "en-GB" });
	});

	it("starts nothing and attaches nothing when aborted during the lookup", async () => {
		const lookup = deferred<{ locales: string[]; installedLocales: string[] }>();
		localeLookup = () => lookup.promise;
		const p = await freshProvider();
		const h = harness();

		const starting = p.start(h.cb);
		p.abort();
		lookup.resolve({ locales: ["en-US"], installedLocales: [] });
		await starting;

		expect(calls.start).toBe(0);
		// The recogniser was never started, so there is nothing to abort either.
		expect(calls.abort).toBe(0);
		expect(listenerCount()).toBe(0);
		emit("result", result("ghost", true));
		emit("end");
		expect(h.onFinal).not.toHaveBeenCalled();
		expect(h.onError).not.toHaveBeenCalled();
	});

	it("finishes empty without starting when stopped during the lookup", async () => {
		const lookup = deferred<{ locales: string[]; installedLocales: string[] }>();
		localeLookup = () => lookup.promise;
		const p = await freshProvider();
		const h = harness();

		const starting = p.start(h.cb);
		p.stop();
		expect(h.onFinal).toHaveBeenCalledExactlyOnceWith("");

		lookup.resolve({ locales: ["en-US"], installedLocales: [] });
		await starting;
		expect(calls.start).toBe(0);
		expect(calls.stop).toBe(0);
		expect(listenerCount()).toBe(0);
	});

	it("starts only the replacing session when start() is called again during the lookup", async () => {
		const lookup = deferred<{ locales: string[]; installedLocales: string[] }>();
		localeLookup = () => lookup.promise;
		const p = await freshProvider();
		const first = harness();
		const second = harness();

		const firstStart = p.start(first.cb);
		const secondStart = p.start(second.cb);
		lookup.resolve({ locales: ["en-US"], installedLocales: [] });
		await Promise.all([firstStart, secondStart]);

		expect(calls.start).toBe(1);
		expect(calls.abort).toBe(0);
		emit("result", result("second phrase", true));
		emit("end");
		expect(first.onFinal).not.toHaveBeenCalled();
		expect(second.onFinal).toHaveBeenCalledExactlyOnceWith("second phrase");
		expect(listenerCount()).toBe(0);
	});
});
