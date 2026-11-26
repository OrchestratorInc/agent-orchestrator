import { describe, expect, it } from "vitest";
import {
	createReplyPacer,
	graphemeEnd,
	PACE_MAX_BACKLOG,
	receiveReplyText,
	replyVisibleText,
	revealEnd,
	stepReplyPacer,
	type ReplyPacer,
} from "./replyPacer";

const FRAME_MS = 1000 / 60;
const WORDS = "the agent reads each file and writes a short note about what changed and why it matters for the next step";

function prose(length: number, seed = 0): string {
	const words = WORDS.split(" ");
	let text = "";
	for (let index = seed; text.length < length; index++) text += `${words[index % words.length]}${index % 13 === 12 ? ".\n\n" : " "}`;
	return text.slice(0, length);
}

type Snapshot = { at: number; length: number; streaming: boolean };

/**
 * A live reply as the phone reads it: the daemon writes a delta run about every
 * 100 ms and publishes an event, the phone reads the conversation behind a
 * 120 ms window plus the read itself, so text lands in bursts ~200-260 ms apart.
 * The agent stops for a tool call partway, then a large burst lands at once,
 * and the turn ends with a final snapshot that adds the last words.
 */
function burstPauseFixture(): { text: string; snapshots: Snapshot[] } {
	const text = prose(2300);
	const snapshots: Snapshot[] = [];
	let at = 400;
	let length = 0;
	const burst = (chars: number, gap: number) => {
		length = Math.min(text.length, length + chars);
		snapshots.push({ at, length, streaming: true });
		at += gap;
	};
	for (let index = 0; index < 18; index++) burst(index % 3 === 0 ? 70 : 45, index % 2 ? 260 : 200);
	at += 2500; // a tool runs
	burst(420, 220); // what the agent wrote while the phone's read was held up
	for (let index = 0; index < 14; index++) burst(index % 4 === 0 ? 90 : 50, index % 2 ? 240 : 190);
	snapshots.push({ at, length: text.length, streaming: false });
	return { text, snapshots };
}

type Trace = { at: number; length: number }[];

/** Draws each snapshot the moment it lands: the phone's chat before pacing. */
function baseline(text: string, snapshots: Snapshot[]): Trace {
	return snapshots.map(({ at, length }) => ({ at, length }));
}

function paced(text: string, snapshots: Snapshot[], playFromStart = true): { trace: Trace; pacer: ReplyPacer } {
	const first = snapshots[0];
	const pacer = createReplyPacer(text.slice(0, first.length), playFromStart ? 0 : first.length, first.streaming);
	const trace: Trace = playFromStart ? [] : [{ at: first.at, length: first.length }];
	const end = snapshots.at(-1)!.at + 2000;
	let next = playFromStart ? 0 : 1;
	for (let now = first.at; now <= end; now += FRAME_MS) {
		while (next < snapshots.length && snapshots[next].at <= now) {
			const snapshot = snapshots[next++];
			receiveReplyText(pacer, text.slice(0, snapshot.length), snapshot.streaming, now);
		}
		const before = pacer.shown;
		stepReplyPacer(pacer, now);
		if (pacer.shown !== before) trace.push({ at: now, length: pacer.shown });
	}
	return { trace, pacer };
}

function metrics(text: string, snapshots: Snapshot[], trace: Trace) {
	const jumps = trace.map((point, index) => point.length - (trace[index - 1]?.length ?? 0)).filter((jump) => jump > 0);
	const sorted = [...jumps].sort((a, b) => a - b);
	const final = snapshots.at(-1)!;
	// Longest stretch with text waiting to be drawn and nothing new drawn, while the agent writes.
	let longestStill = 0;
	for (let index = 1; index < trace.length; index++) {
		const gap = trace[index].at - trace[index - 1].at;
		const waiting = snapshots.some((snapshot) => snapshot.at <= trace[index - 1].at && snapshot.length > trace[index - 1].length);
		if (waiting || gap < 1000) longestStill = Math.max(longestStill, gap < 1000 ? gap : 0);
	}
	const midWord = trace.filter(({ length }) => length < text.length && /\w/.test(text[length - 1]) && /\w/.test(text[length])).length;
	const exactAt = trace.find((point) => point.length === text.length)?.at ?? Infinity;
	return {
		updates: trace.length,
		maxJump: sorted.at(-1) ?? 0,
		p95Jump: sorted[Math.floor(sorted.length * 0.95)] ?? 0,
		longestStill: Math.round(longestStill),
		midWordStops: midWord,
		exactAfterEndMs: Math.round(exactAt - final.at),
	};
}

describe("live reply pacing on a burst/pause fixture", () => {
	const { text, snapshots } = burstPauseFixture();
	const before = metrics(text, snapshots, baseline(text, snapshots));
	const { trace, pacer } = paced(text, snapshots);
	const after = metrics(text, snapshots, trace);
	if (process.env.PACE_REPORT) console.table({ before, after });

	it("spreads each burst into small steps instead of drawing it at once", () => {
		expect(before.maxJump).toBeGreaterThanOrEqual(420);
		expect(after.p95Jump).toBeLessThanOrEqual(30);
		expect(after.p95Jump * 3).toBeLessThan(before.p95Jump);
		expect(after.maxJump * 10).toBeLessThan(before.maxJump);
		expect(after.updates).toBeGreaterThan(before.updates * 5);
	});

	it("moves at an even speed while the agent writes, not in a rush as each read lands", () => {
		// Characters drawn in each tenth of a second across the steady stretch
		// before the tool runs. Paced to the backlog alone, the stream rushed out
		// as each read landed and slowed nearly to a stop before the next: the
		// speed varied by a third of itself and a fast tenth drew three slow ones.
		const shownAt = (time: number) => trace.filter((point) => point.at <= time).at(-1)?.length ?? 0;
		const speeds: number[] = [];
		for (let time = 1000; time < 4100; time += 100) speeds.push(shownAt(time + 100) - shownAt(time));
		const mean = speeds.reduce((sum, speed) => sum + speed, 0) / speeds.length;
		const spread = Math.sqrt(speeds.reduce((sum, speed) => sum + (speed - mean) ** 2, 0) / speeds.length) / mean;
		expect(spread).toBeLessThan(0.2);
		expect(Math.max(...speeds)).toBeLessThan(Math.min(...speeds) * 2.5);
	});

	it("keeps moving between bursts rather than holding still for the next one", () => {
		expect(before.longestStill).toBeGreaterThanOrEqual(200);
		expect(after.longestStill).toBeLessThan(120);
	});

	it("ends on word boundaries, not mid-word", () => {
		expect(after.midWordStops).toBeLessThanOrEqual(Math.ceil(after.updates * 0.05));
	});

	it("catches up after a big burst and finishes on the exact text soon after the turn ends", () => {
		expect(replyVisibleText(pacer)).toBe(text);
		expect(after.exactAfterEndMs).toBeLessThanOrEqual(400);
	});
});

describe("short and long replies", () => {
	it("plays a short reply in full and lands on its exact text", () => {
		const text = "Done. The tests pass.";
		const { trace, pacer } = paced(text, [{ at: 0, length: 12, streaming: true }, { at: 200, length: text.length, streaming: false }]);
		expect(replyVisibleText(pacer)).toBe(text);
		expect(trace.at(-1)!.at).toBeLessThan(600);
	});

	it("never trails a live reply by more than the backlog cap", () => {
		const text = prose(6000, 3);
		const pacer = createReplyPacer(text.slice(0, 100), 100, true);
		receiveReplyText(pacer, text.slice(0, 5000), true, 0);
		expect(5000 - pacer.shown).toBeLessThanOrEqual(PACE_MAX_BACKLOG);
		expect(5000 - pacer.shown).toBeGreaterThan(0);
	});

	it("shows a finished reply whole when it is far behind", () => {
		const text = prose(5000);
		const pacer = createReplyPacer(text.slice(0, 100), 100, true);
		receiveReplyText(pacer, text, false, 0);
		expect(replyVisibleText(pacer)).toBe(text);
	});
});

describe("history and corrections", () => {
	it("shows text it was created with at once", () => {
		const pacer = createReplyPacer("An older reply.");
		expect(replyVisibleText(pacer)).toBe("An older reply.");
		expect(stepReplyPacer(pacer, 0)).toBe(false);
	});

	it("shows a rewrite whole instead of revealing it", () => {
		const pacer = createReplyPacer("Hello wor", 4, true);
		receiveReplyText(pacer, "Goodbye", true, 0);
		expect(replyVisibleText(pacer)).toBe("Goodbye");
	});

	it("shows what arrived after a stall (the app was away) instead of replaying it", () => {
		const text = prose(400);
		const pacer = createReplyPacer(text.slice(0, 10), 0, true);
		receiveReplyText(pacer, text, true, 0);
		stepReplyPacer(pacer, 0);
		stepReplyPacer(pacer, 1500);
		expect(replyVisibleText(pacer)).toBe(text);
	});

	it("draws at most about 30 updates a second", () => {
		const text = prose(400);
		const pacer = createReplyPacer("", 0, true);
		receiveReplyText(pacer, text, true, 0);
		let updates = 0;
		for (let now = 0; now < 1000; now += FRAME_MS) {
			const before = pacer.shown;
			stepReplyPacer(pacer, now);
			if (pacer.shown !== before) updates++;
		}
		expect(updates).toBeLessThanOrEqual(31);
	});
});

describe("boundaries", () => {
	it("moves a frame's end to a nearby word boundary", () => {
		const text = "alpha beta gamma delta";
		expect(revealEnd(text, 0, 8)).toBe(6);
		expect(revealEnd(text, 6, 8)).toBe(11);
	});

	it("never splits an emoji sequence, a flag or a combining mark", () => {
		const family = "👩‍👩‍👧";
		expect(graphemeEnd(family, 1)).toBe(family.length);
		expect(graphemeEnd(family, 3)).toBe(family.length);
		const flags = "🇮🇳🇯🇵";
		expect(graphemeEnd(flags, 2)).toBe(4);
		expect(graphemeEnd(flags, 1)).toBe(4);
		expect(graphemeEnd("e\u0301a", 1)).toBe(2);
		expect(graphemeEnd("👍🏽!", 2)).toBe(4);
	});

	it("reveals text written without spaces a grapheme at a time rather than waiting for a space", () => {
		const text = "日本語のテキストは空白なしで書かれます";
		const pacer = createReplyPacer("", 0, true);
		receiveReplyText(pacer, text, true, 0);
		stepReplyPacer(pacer, 0);
		expect(pacer.shown).toBeGreaterThan(0);
		expect(pacer.shown).toBeLessThan(text.length);
	});
});
