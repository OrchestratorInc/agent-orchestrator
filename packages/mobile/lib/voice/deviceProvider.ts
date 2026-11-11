import {
	AVAudioSessionCategory,
	AVAudioSessionCategoryOptions,
	AVAudioSessionMode,
	ExpoSpeechRecognitionModule,
} from "expo-speech-recognition";
import { Platform } from "react-native";
import type { VoiceCallbacks, VoiceMode, VoiceProvider } from "./types";

// On-device speech recognition: iOS SFSpeechRecognizer, Android SpeechRecognizer.
// Chosen over a cloud recogniser because it is free, private, needs no API key
// and carries no per-minute cost — dictation is the kind of thing people leave
// on, and a metered one would be quietly expensive.

// Bias the recogniser toward words that show up constantly in agent prompts and
// would otherwise come back mangled ("get" for "git", "MPM" for "npm"). iOS maps
// this to SFSpeechRecognitionRequest.contextualStrings, Android to
// EXTRA_BIASING_STRINGS. Same trick Claude Code applies to its own dictation.
const CODING_VOCABULARY = [
	"git",
	"npm",
	"repo",
	"commit",
	"rebase",
	"branch",
	"merge",
	"PR",
	"diff",
	"refactor",
	"TypeScript",
	"JavaScript",
	"Go",
	"React",
	"Expo",
	"JSON",
	"API",
	"CLI",
	"daemon",
	"worktree",
	"regex",
	"localhost",
	"stack trace",
	"lint",
	"typecheck",
];

// How long stop() waits for the recogniser's `end` event before delivering the
// transcript anyway. Generous: `end` normally arrives in well under a second, and
// firing early would cut off a final result still in flight.
const STOP_GRACE_MS = 4000;

// How often the recogniser reports the input level while capturing.
const LEVEL_INTERVAL_MS = 80;

/** The recogniser's -2 to 10 input meter as 0 (inaudible) to 1. */
export function voiceLevel(value: number): number {
	return Math.min(1, Math.max(0, value / 10));
}

// Recognition accuracy drops sharply when the locale doesn't match the speaker's
// accent - "en-US" applied to an en-IN or en-GB speaker mishears steadily rather
// than failing outright, which reads as "it just transcribes the wrong words".
// So resolve the device's own locale against what the recogniser actually
// supports, and fall back to the same language in another region before giving up
// on en-US.
let resolvedLang: string | null = null;
// One lookup in flight at a time, shared by the warm-up kick and any start().
let langLookup: Promise<string | null> | null = null;
// Bumped when start() gives up on a lookup, so that lookup can neither cache a
// late answer over a newer one nor keep blocking retries.
let langLookupGeneration = 0;

// How long start() waits on an unfinished locale lookup before giving up on it
// for this phrase. The lookup is a native callback on Android that a misbehaving
// recognition service may never answer; dictation must not hang on it.
const LANG_LOOKUP_TIMEOUT_MS = 2000;

const normalizeLocale = (s: string) => s.replace(/_/g, "-").toLowerCase();

// The recogniser locale, or null when the lookup itself failed. Only a completed
// lookup is cached (including "nothing matched", which genuinely resolves to
// en-US); a failure or timeout is retried on the next press rather than pinning en-US.
function resolveLang(): Promise<string | null> {
	if (resolvedLang) return Promise.resolve(resolvedLang);
	if (langLookup) return langLookup;
	const generation = langLookupGeneration;
	const lookup = (async () => {
		let device = "en-US";
		try {
			device = Intl.DateTimeFormat().resolvedOptions().locale || "en-US";
		} catch {
			// No Intl (unexpected on Hermes) - keep the default.
		}

		try {
			const { locales } = await ExpoSpeechRecognitionModule.getSupportedLocales({});
			// Abandoned after a timeout: a newer lookup owns the cache now.
			if (generation !== langLookupGeneration) return null;
			const want = normalizeLocale(device);
			const exact = locales.find((l) => normalizeLocale(l) === want);
			if (exact) return (resolvedLang = exact);

			// e.g. device is en-IN but only en-GB is installed - still far closer than en-US.
			const language = want.split("-")[0];
			const sameLanguage = locales.find((l) => normalizeLocale(l).split("-")[0] === language);
			return (resolvedLang = sameLanguage ?? "en-US");
		} catch {
			return null;
		}
	})();
	langLookup = lookup;
	// Cleared by identity, after assignment, so neither a synchronous native throw
	// nor a stale lookup settling late can clobber the current one.
	void lookup.then(() => {
		if (langLookup === lookup) langLookup = null;
	});
	return lookup;
}

// The locale to start a recording with. Waits for the lookup (normally already
// finished by the warm-up kick) so the first phrase is not recognised in the
// wrong accent; en-US only once the lookup has failed or timed out. Only a start
// whose session is still live may abandon the lookup: a replaced start's deadline
// says nothing about a lookup its replacement is still within time to use.
async function langForStart(stillWanted: () => boolean): Promise<string> {
	const timedOut = Symbol("timedOut");
	let timer: ReturnType<typeof setTimeout> | undefined;
	const timeout = new Promise<typeof timedOut>((resolve) => {
		timer = setTimeout(() => resolve(timedOut), LANG_LOOKUP_TIMEOUT_MS);
	});
	try {
		const lookup = resolveLang();
		const lang = await Promise.race([lookup, timeout]);
		if (lang === timedOut) {
			// Abandon the hung lookup so the next press asks the recogniser again,
			// unless a newer lookup already replaced it.
			if (stillWanted() && langLookup === lookup) {
				langLookupGeneration++;
				langLookup = null;
			}
			return "en-US";
		}
		return lang ?? "en-US";
	} finally {
		clearTimeout(timer);
	}
}

// LAST-RESORT Android segment detection, for a recognition service that emits
// neither a final result nor `speechstart` at a boundary. Never used on iOS, whose
// boundaries come from native audio timings (see the result listener). The real boundaries come from
// those two events; this only catches a partial that has obviously rolled over
// without one.
//
// Within a segment, successive partials restate the whole segment: they grow, and
// revise words in place ("add a test" -> "add a test for" -> "add attest for").
// They therefore keep sharing an opening. A partial that shares none belongs to a
// new segment, so the previous one is finished and must be banked.
function isSameSegment(prev: string, next: string): boolean {
	if (!prev.trim()) return true;
	const a = prev.trim().toLowerCase();
	const b = next.trim().toLowerCase();
	// Plain growth, or a revision that shortened the text.
	if (b.startsWith(a) || a.startsWith(b)) return true;
	// A revision can rewrite words the recogniser already emitted ("a test" ->
	// "attest"), so a character prefix is too brittle. But it still opens on the
	// same word and does not get shorter. A new segment after a pause starts fresh
	// and short, so this separates them without being fooled by re-transcription.
	const firstWord = (s: string) => s.slice(0, s.indexOf(" ") === -1 ? s.length : s.indexOf(" "));
	return firstWord(a) === firstWord(b) && b.length >= a.length;
}

// One recognised word, where in the recording it was spoken, and - when it could
// be located - where it sits in its utterance's formatted transcript.
type TimedWord = { text: string; start: number; end: number; at?: number };

// A result's words with their native timings (SFTranscriptionSegment substring,
// timestamp and duration, measured from the start of the task's audio), and the
// span they cover. Null when there is nothing usable: no segments, or the zeroed
// timings iOS can report on interim results.
//
// Each word is also located in `text`, the formatted transcript. Apple defines a
// segment's substring as a range of that formatted string, so in order each one
// must be found after the last; if any is not, no word gets a position and
// nothing is cut from this utterance's text later (see the result listener).
function timedWords(
	segments:
		| readonly { segment?: string; startTimeMillis: number; endTimeMillis: number }[]
		| undefined,
	text: string,
): { words: TimedWord[]; start: number; end: number } | null {
	if (!segments?.length) return null;
	let start = Infinity;
	let end = -Infinity;
	const words: TimedWord[] = [];
	for (const s of segments) {
		if (!Number.isFinite(s.startTimeMillis) || !Number.isFinite(s.endTimeMillis)) return null;
		start = Math.min(start, s.startTimeMillis);
		end = Math.max(end, s.endTimeMillis);
		words.push({ text: typeof s.segment === "string" ? s.segment : "", start: s.startTimeMillis, end: s.endTimeMillis });
	}
	if (!(end > start)) return null;

	let cursor = 0;
	for (const w of words) {
		const at = w.text ? text.indexOf(w.text, cursor) : -1;
		if (at === -1) {
			for (const u of words) u.at = undefined;
			break;
		}
		w.at = at;
		cursor = at + w.text.length;
	}
	return { words, start, end };
}

// A "no speech" error is what you get from a mis-tap or a silent room. It is the
// expected outcome of a stray press, not a fault worth showing a banner for.
const SILENT_ERRORS = new Set(["no-speech", "aborted", "speech-timeout"]);

const ERROR_MESSAGES: Record<string, string> = {
	"not-allowed": "Microphone or speech permission was denied. Enable it in Settings to dictate.",
	"service-not-allowed": "Speech recognition is unavailable on this device.",
	"language-not-supported": "Speech recognition is not available for this language.",
	network: "Speech recognition needs a network connection and could not reach it.",
	"audio-capture": "Couldn't capture audio from the microphone.",
	interrupted: "Recording was interrupted.",
	busy: "The speech recogniser is busy. Try again in a moment.",
};

// Process-wide singleton. The provider caches the permission grant, and the
// native module caches the SFSpeechRecognizer across starts — both of which are
// wasted if a new provider appears every time a terminal screen mounts. Recording
// state lives inside one provider, and only one recording can run at a time
// anyway, so sharing is safe.
let shared: VoiceProvider | null = null;

export function getDeviceVoiceProvider(): VoiceProvider {
	shared ??= createDeviceVoiceProvider();
	return shared;
}

export function createDeviceVoiceProvider(): VoiceProvider {
	// Kick locale resolution off now rather than on the first press: warm-up is
	// already the sore point, and this keeps start()'s wait on it near zero.
	void resolveLang();

	// Everything about one recording lives in a single object, so ending a
	// recording is "drop the session" rather than "reset five variables". A stale
	// event that somehow outlives its subscriptions finds `session` null and is
	// ignored, and there is no window where half the state has been cleared.
	type Session = {
		cb: VoiceCallbacks;
		subscriptions: { remove(): void }[];
		// Continuous recognition is SEGMENTED on Android: each pause closes a segment
		// with its own `isFinal` result, and the next segment starts from scratch
		// rather than restating what came before. So finished segments must be kept
		// and joined - treating any single result as "the transcript" silently drops
		// everything the user said after their first pause.
		finalized: string[];
		// The in-flight segment, replaced as it firms up (partials restate the whole
		// current segment, they are not deltas).
		partial: string;
		// Set by stop(); fires only if `end` never arrives. See stop().
		settleTimer?: ReturnType<typeof setTimeout>;
		// False while start() is still resolving the locale: nothing native is
		// running for this session yet, so there is nothing to stop or abort.
		nativeStarted: boolean;
		// iOS only: the utterance a timed final result closed, and the audio it
		// covers. Banked once a later result is shown to cover audio after it.
		closed?: { text: string; start: number; end: number; words: TimedWord[] };
		// iOS only: the timed words of each `finalized` entry, index for index, so a
		// later result restating some of that audio replaces just those words
		// rather than repeating them or dropping the rest.
		finalizedWords: TimedWord[][];
		// iOS only: the separator written before each `finalized` entry. A space
		// between separate utterances; inside a split utterance, whatever its
		// formatted transcript had there - which is nothing in a no-space language.
		finalizedSeps: string[];
	};
	let session: Session | null = null;
	// Sticky once granted. Permission can only be revoked from Settings, which
	// restarts the app, so caching it for the process lifetime is safe.
	let granted = false;

	// Everything heard so far: completed segments plus the one in progress.
	function transcriptOf(s: Session): string {
		let text = "";
		s.finalized.forEach((entry, i) => {
			if (entry) text += (text ? (s.finalizedSeps[i] ?? " ") : "") + entry;
		});
		if (s.partial) text += (text ? " " : "") + s.partial;
		return text.trim();
	}

	function close(): VoiceCallbacks | null {
		if (!session) return null;
		const { cb, subscriptions, settleTimer } = session;
		session = null;
		if (settleTimer) clearTimeout(settleTimer);
		for (const sub of subscriptions) sub.remove();
		return cb;
	}

	// Ends the recording exactly once. Driven by `end` — documented as always the
	// last event, including after errors — rather than by a final result, because
	// in continuous mode a final result means "segment done", not "recording done".
	// Always reports through onFinal; an empty string is a legitimate result
	// meaning "nothing was said", which the caller treats as a no-op, not an error.
	function settle() {
		if (!session) return;
		const text = transcriptOf(session);
		const cb = close();
		cb?.onFinal(text);
	}

	function fail(message: string) {
		const cb = close();
		cb?.onError(message);
	}

	// Drop listeners before aborting so the resulting "aborted" error event can't
	// settle a session the caller has already walked away from.
	function abortSession() {
		const current = session;
		close();
		// Cancelled during locale lookup: the native recogniser was never asked to
		// start, and start() sees the session is gone and won't start it.
		if (current && !current.nativeStarted) return;
		try {
			ExpoSpeechRecognitionModule.abort();
		} catch {
			// Nothing was running.
		}
	}

	return {
		id: "device",

		language() {
			return resolvedLang;
		},

		available() {
			try {
				return ExpoSpeechRecognitionModule.isRecognitionAvailable();
			} catch {
				// Module missing entirely (e.g. a build that skipped prebuild).
				return false;
			}
		},

		async requestPermission() {
			// Every press goes through here, and this sits directly in front of the
			// microphone starting — so once permission is known to be granted, skip the
			// native round-trip entirely. Re-asking each time delayed the start of
			// recording enough to swallow the first words of a short phrase.
			if (granted) return true;
			try {
				const existing = await ExpoSpeechRecognitionModule.getPermissionsAsync();
				if (existing.granted) {
					granted = true;
					return true;
				}
				const res = await ExpoSpeechRecognitionModule.requestPermissionsAsync();
				granted = res.granted;
				return res.granted;
			} catch {
				return false;
			}
		},

		async start(cb, opts) {
			const longForm = opts?.mode === "latched";
			// A previous recording that never settled must not leak its listeners.
			// When there is no live session the native side is already stopped (every
			// settle path follows a native end/error), so don't abort for nothing.
			if (session) abortSession();
			const current: Session = {
				cb,
				subscriptions: [],
				finalized: [],
				partial: "",
				nativeStarted: false,
				finalizedWords: [],
				finalizedSeps: [],
			};
			session = current;

			// Resolve the locale BEFORE starting: it cannot be changed once the
			// recogniser is running. The session may be aborted, stopped or replaced
			// while this waits; if so, start nothing and attach nothing.
			const lang = await langForStart(() => session === current);
			if (session !== current) return;

			current.subscriptions.push(
				ExpoSpeechRecognitionModule.addListener("result", (e) => {
					if (session !== current) return;
					const transcript = e.results?.[0]?.transcript ?? "";

					// iOS runs one recognition task per start(). Within an utterance every
					// result restates it, so it replaces the last one whether it grew,
					// shortened, gained punctuation, re-spelled its first word ("How the"
					// -> "How's the" -> "How is the") or repeated a final. Guessing
					// boundaries from the text is what snowballed a dictation into
					// "How the How's the system system turning How is the system turning?".
					//
					// But on iOS 18+ the module reports "final-like" results (isFinal) in
					// the middle of a continuous task, and what follows can be a NEW
					// utterance rather than a restatement. The evidence for that is the
					// audio itself: a result whose segments start at or after the end of
					// the utterance a final closed covers different speech, so the closed
					// one is banked. Overlapping audio is a revision and replaces it.
					// Without usable timings there is no evidence either way, so the
					// result replaces what is shown but keeps the closed utterance on
					// hand, banking it only once a timed result proves the gap.
					if (Platform.OS === "ios") {
						const text = transcript.trim();
						// An empty result carries no transcript (iOS 18 sends a trailing
						// `isFinal` with no results); it must not wipe what was heard.
						if (!text) return;
						const span = timedWords(e.results?.[0]?.segments, text);
						// Where a revision split an utterance: the piece left before the cut
						// and the piece left after it, each with the whitespace the
						// transcript had at that cut (nothing, in a no-space language).
						let cutBefore: { words: TimedWord[]; sep: string } | undefined;
						let cutAfter: { words: TimedWord[]; sep: string } | undefined;
						if (span) {
							// A result restating audio already banked (a cumulative revision)
							// supersedes exactly the words whose audio it covers. Words before
							// or after it survive, cut from the utterance's own formatted
							// transcript so their punctuation and spacing are unchanged; an
							// utterance it does not touch keeps its text as is.
							for (let i = current.finalizedWords.length - 1; i >= 0; i--) {
								const ws = current.finalizedWords[i];
								let before = 0;
								while (before < ws.length && ws[before].end <= span.start) before++;
								let after = 0;
								while (after < ws.length - before && ws[ws.length - 1 - after].start >= span.end) after++;
								const covered = ws.length - before - after;
								if (!covered && (!before || !after)) continue;
								const entry = current.finalized[i];
								const parts: { text: string; words: TimedWord[]; sep: string }[] = [];
								const trailingSpace = (end: number) => /\s*$/.exec(entry.slice(0, end))?.[0] ?? "";
								if (before || after) {
									// Conservative fallback: without verified positions the
									// uncovered words cannot be cut out without guessing at
									// formatting, so the utterance is kept whole.
									if (ws.some((w) => w.at === undefined)) continue;
									if (before) {
										const words = ws.slice(0, before);
										parts.push({
											text: entry.slice(0, ws[before].at).trimEnd(),
											words,
											sep: current.finalizedSeps[i] ?? " ",
										});
										cutBefore = { words, sep: trailingSpace(ws[before].at ?? 0) };
									}
									if (after) {
										const from = ws[ws.length - after].at ?? 0;
										const words = ws.slice(ws.length - after).map((w) => ({ ...w, at: (w.at ?? 0) - from }));
										// A space unless the revision lands right before it (below).
										parts.push({ text: entry.slice(from).trim(), words, sep: " " });
										cutAfter = { words, sep: trailingSpace(from) };
									}
								}
								current.finalized.splice(i, 1, ...parts.map((part) => part.text));
								current.finalizedWords.splice(i, 1, ...parts.map((part) => part.words));
								current.finalizedSeps.splice(i, 1, ...parts.map((part) => part.sep));
							}
						}
						const closed = current.closed;
						if (closed && span) {
							if (span.start >= closed.end) {
								current.finalized.push(closed.text);
								current.finalizedWords.push(closed.words);
								current.finalizedSeps.push(" ");
								current.closed = undefined;
							} else if (span.end <= closed.start) {
								// Revises only earlier, already-banked speech: put it back in
								// its place in time. The current utterance is still the closed
								// one, so it is shown again in place of any untimed result
								// that was in fact this earlier revision.
								const at = current.finalizedWords.findIndex((ws) => ws[0].start >= span.start);
								const i = at === -1 ? current.finalized.length : at;
								current.finalized.splice(i, 0, text);
								current.finalizedWords.splice(i, 0, span.words);
								// Back inside the utterance it was cut from, the revision keeps
								// that utterance's separators at the cut; next to anything else
								// it is a separate utterance, joined with a space.
								current.finalizedSeps.splice(
									i,
									0,
									cutBefore && i > 0 && current.finalizedWords[i - 1] === cutBefore.words ? cutBefore.sep : " ",
								);
								if (cutAfter && current.finalizedWords[i + 1] === cutAfter.words) {
									current.finalizedSeps[i + 1] = cutAfter.sep;
								}
								current.partial = closed.text;
								cb.onPartial(transcriptOf(current));
								return;
							} else {
								closed.text = text;
								closed.start = Math.min(closed.start, span.start);
								closed.end = Math.max(closed.end, span.end);
								closed.words = span.words;
							}
						}
						current.partial = text;
						if (e.isFinal && span && !current.closed) {
							current.closed = { text, start: span.start, end: span.end, words: span.words };
						}
						cb.onPartial(transcriptOf(current));
						return;
					}

					if (e.isFinal) {
						// A closed segment, NOT the end of the recording. Bank it and wait —
						// the user is still holding the key and may keep talking. The final
						// supersedes the partial it was built from, so the partial is
						// dropped rather than banked alongside it.
						if (transcript.trim()) current.finalized.push(transcript.trim());
						current.partial = "";
					} else {
						// Normally `speechstart` has already banked the previous segment by
						// now. This only fires if the service announced no boundary at all.
						if (!isSameSegment(current.partial, transcript) && current.partial.trim()) {
							current.finalized.push(current.partial.trim());
						}
						current.partial = transcript;
					}
					cb.onPartial(transcriptOf(current));
				}),
			);

			current.subscriptions.push(
				// The authoritative segment boundary on Android, and the reason its
				// transcripts can now match iOS's.
				//
				// iOS runs one recognition task and signals only its initial speech
				// start here; its utterance boundaries use result timings above. Android's
				// SpeechRecognizer is built for a single utterance, so it emits a
				// sequence of independent chunks that have to be stitched.
				// `onBeginningOfSpeech` fires at the start of each new utterance, so
				// anything still held as a partial belongs to the previous one.
				//
				// iOS is excluded deliberately: it fires `speechstart` once after a
				// result, so treating that callback as a new utterance would
				// duplicate the text rather than segment it.
				ExpoSpeechRecognitionModule.addListener("speechstart", () => {
					if (session !== current || Platform.OS !== "android") return;
					if (current.partial.trim()) {
						current.finalized.push(current.partial.trim());
						current.partial = "";
					}
				}),
			);

			current.subscriptions.push(
				ExpoSpeechRecognitionModule.addListener("start", () => {
					// The recogniser is live. Until this fires the microphone is still
					// warming up and speech is dropped on the floor, so this is the only
					// honest moment to prompt the user to talk.
					if (session === current) cb.onReady();
				}),
			);

			current.subscriptions.push(
				// The input meter, for the composer's waveform. The recogniser reports
				// -2 to 10, anything below 0 inaudible.
				ExpoSpeechRecognitionModule.addListener("volumechange", (e) => {
					if (session === current) cb.onLevel?.(voiceLevel(e.value));
				}),
			);

			current.subscriptions.push(
				ExpoSpeechRecognitionModule.addListener("error", (e) => {
					if (session !== current) return;
					// Silence, a stray tap, or a deliberate abort is not a fault. Settle
					// normally so anything already transcribed survives.
					if (SILENT_ERRORS.has(e.error)) settle();
					else fail(ERROR_MESSAGES[e.error] ?? e.message ?? "Speech recognition failed.");
				}),
			);

			current.subscriptions.push(
				ExpoSpeechRecognitionModule.addListener("end", () => {
					// `end` is the only reliable "the recording is over" signal: it is
					// documented as always the last event dispatched, including after an
					// error, and unlike a final result it never also means "segment
					// boundary". This is what settles a normal recording.
					if (session === current) settle();
				}),
			);

			try {
				current.nativeStarted = true;
				ExpoSpeechRecognitionModule.start({
					lang,
					interimResults: true,
					// Push-to-talk: the user's finger decides when the phrase ends, not a
					// silence timer. Unsupported on Android 12 and below, where it falls
					// back to auto-stop — still correct, just less forgiving of a pause.
					continuous: true,
					addsPunctuation: true,
					contextualStrings: CODING_VOCABULARY,
					// Often enough for the waveform to follow speech, rarely enough to
					// stay off the bridge's back.
					volumeChangeEventOptions: { enabled: true, intervalMillis: LEVEL_INTERVAL_MS },
					// The audio session is the warm-up: measured at ~1.1s, dominated by
					// Bluetooth HFP route negotiation and configuring an output path.
					// That cost is intolerable before a three-second phrase and irrelevant
					// before a sixty-second one, so the two modes buy different sessions.
					// It cannot be changed mid-recording - iOS would need a teardown and
					// re-activation - which is why the mode is fixed at start.
					iosCategory: longForm
						? {
								// Hands-free dictation: worth the slower activation to let a
								// Bluetooth headset mic carry a long prompt.
								category: AVAudioSessionCategory.playAndRecord,
								categoryOptions: [
									AVAudioSessionCategoryOptions.allowBluetooth,
									AVAudioSessionCategoryOptions.defaultToSpeaker,
								],
								mode: AVAudioSessionMode.measurement,
							}
						: {
								// Push-to-talk: cheapest session that can capture. Built-in mic
								// only, no route negotiation, no output path.
								category: AVAudioSessionCategory.record,
								categoryOptions: [],
								mode: AVAudioSessionMode.default,
							},
					androidIntentOptions: {
						// Thinking mid-sentence is normal when dictating a prompt. Android's
						// default silence timeout is short enough to end the session while
						// the user is still holding the key, so widen it and let the finger
						// decide instead.
						EXTRA_SPEECH_INPUT_COMPLETE_SILENCE_LENGTH_MILLIS: 10000,
						EXTRA_SPEECH_INPUT_POSSIBLY_COMPLETE_SILENCE_LENGTH_MILLIS: 10000,
					},
				});
			} catch (e) {
				fail(e instanceof Error ? e.message : "Couldn't start the microphone.");
			}
		},

		stop() {
			const current = session;
			// Stopped while the locale was still resolving: nothing was recorded and
			// nothing native is running, so finish now with an empty transcript.
			if (current && !current.nativeStarted) {
				settle();
				return;
			}
			try {
				ExpoSpeechRecognitionModule.stop();
			} catch {
				// Already stopped; the `end` listener settles us.
			}
			if (!current) return;
			// Watchdog. `end` normally lands within a few hundred ms of stop() and is
			// what delivers the transcript. If the recogniser dies without emitting it
			// the UI would sit in "recording" forever with the words stranded — the
			// "it recorded but nothing happened" failure. Settle with what we heard.
			current.settleTimer = setTimeout(() => {
				if (session === current) settle();
			}, STOP_GRACE_MS);
		},

		abort: abortSession,
	};
}
