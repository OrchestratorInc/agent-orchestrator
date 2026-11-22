import { describe, expect, it } from "vitest";
import { createReplyPacer, receiveReplyText, stepReplyPacer } from "./replyPacer";
import { glideStep, isSentItem, newestSequence, optimisticSentItem, SENT_ROOM_SHARE, sentTurnRoom } from "./timelineScroll";

const FRAME_MS = 1000 / 60;

describe("glideStep", () => {
	it("closes on the target and lands exactly on it", () => {
		let at = 0;
		let frames = 0;
		while (at !== 300 && frames < 200) {
			at = glideStep(at, 300, FRAME_MS);
			frames++;
		}
		expect(at).toBe(300);
		// About half a second for a screen-sized move: neither a snap nor a crawl.
		expect(frames).toBeGreaterThan(15);
		expect(frames).toBeLessThan(60);
	});

	it("starts no faster than its top speed", () => {
		expect(glideStep(0, 5000, FRAME_MS)).toBeLessThanOrEqual(3 * FRAME_MS + 1e-9);
	});

	it("jumps to within a screen first, then glides", () => {
		const next = glideStep(0, 5000, FRAME_MS, 700);
		expect(next).toBeGreaterThan(4300);
		expect(next).toBeLessThan(4300 + 3 * FRAME_MS + 1e-9);
	});

	it("follows a target that moves without starting over", () => {
		let at = 0;
		let target = 100;
		const steps: number[] = [];
		for (let frame = 0; frame < 120; frame++) {
			if (frame % 6 === 0) target += 22; // a line every ~100 ms
			const next = glideStep(at, target, FRAME_MS);
			steps.push(next - at);
			at = next;
		}
		// Always moving forward, never a step back, never a jump.
		expect(Math.min(...steps.slice(1))).toBeGreaterThanOrEqual(0);
		expect(Math.max(...steps)).toBeLessThanOrEqual(3 * FRAME_MS + 1e-9);
	});
});

describe("sentTurnRoom", () => {
	it("leaves the visible height between the header and the end padding, less the keyboard", () => {
		expect(sentTurnRoom({ listHeight: 800, keyboard: 0, topClear: 110, endPad: 120 })).toBe(570);
		expect(sentTurnRoom({ listHeight: 800, keyboard: 300, topClear: 110, endPad: 120 })).toBe(270);
		expect(sentTurnRoom({ listHeight: 300, keyboard: 300, topClear: 110, endPad: 120 })).toBe(0);
	});

	it("takes three quarters of that height when asked, so the message rises three quarters of the way", () => {
		expect(sentTurnRoom({ listHeight: 800, keyboard: 0, topClear: 110, endPad: 120, share: SENT_ROOM_SHARE })).toBe(428);
	});
});

describe("the sent message", () => {
	const anchor = { conversationId: "c", after: 9, at: 1_700_000_000_000 };

	it("is a human message or the person's steer after the send began, not an agent's steer or anything older", () => {
		const message = { kind: "message", id: "m", sequence: 10, revision: 1, role: "user", origin: "human", text: "hi", streaming: false, createdAt: "" } as const;
		expect(isSentItem(message, 9)).toBe(true);
		expect(isSentItem(message, 10)).toBe(false);
		expect(isSentItem({ ...message, origin: "automation" }, 9)).toBe(false);
		const steer = { kind: "activity", id: "s", sequence: 11, revision: 1, activityKind: "system", status: "completed", summary: "x", detail: { event: "steer", text: "x" }, createdAt: "" } as const;
		expect(isSentItem(steer, 9)).toBe(true);
		expect(isSentItem({ ...steer, detail: { event: "steer", text: "x", senderSessionId: "other" } }, 9)).toBe(false);
	});

	it("is drawn before the conversation has it, after everything loaded, as the real one will be", () => {
		const message = optimisticSentItem({ ...anchor, kind: "send", text: "Fix the build" }, 12);
		expect(message).toMatchObject({ kind: "message", role: "user", origin: "human", text: "Fix the build", sequence: 12.5 });
		expect(message && isSentItem(message, anchor.after)).toBe(true);
		const steer = optimisticSentItem({ ...anchor, kind: "steer", text: "Use the other API", turnId: "t1" }, 12);
		expect(steer).toMatchObject({ kind: "activity", turnId: "t1", activityKind: "system", detail: { event: "steer", text: "Use the other API" } });
		expect(steer && isSentItem(steer, anchor.after)).toBe(true);
		// Without text (files, quotes, a queued send) nothing is drawn early.
		expect(optimisticSentItem(anchor, 12)).toBeUndefined();
	});
});

describe("newestSequence", () => {
	it("is the highest sequence, or 0 for an empty conversation", () => {
		expect(newestSequence([{ sequence: 4 }, { sequence: 9 }, { sequence: 7 }])).toBe(9);
		expect(newestSequence([])).toBe(0);
	});
});

/**
 * A model of the list after a send, frame by frame. The real list is native;
 * this replays the same layout and follow rules in both versions so their
 * motion can be compared on the same events:
 *
 * - main: the newest turn is its content's height, and every content change
 *   while following snaps the list to its end;
 * - now: the sent turn holds `sentTurnRoom` open below the message, and a
 *   follow glides (glideStep) to the end.
 *
 * Events: the message lands with a delivery note that clears on the ack, a
 * Working row, two tool rows, then a streamed reply paced by replyPacer (the
 * baseline draws each snapshot whole).
 */
type Version = "main" | "now";
type Drag = { at: number; by: number };

const LIST = 800;
const TOP_CLEAR = 110;
const END_PAD = 120;
const HISTORY = 3000;
const LINE = 22;
const CHARS_PER_LINE = 40;

function simulate(version: Version, replyLength: number, drag?: Drag) {
	const text = "word ".repeat(Math.ceil(replyLength / 5)).slice(0, replyLength);
	const snapshots: { at: number; length: number; streaming: boolean }[] = [];
	for (let at = 1600, length = 0; length < replyLength; at += 220) {
		length = Math.min(replyLength, length + 60);
		snapshots.push({ at, length, streaming: length < replyLength });
	}
	const pacer = createReplyPacer("", 0, true);
	const room = sentTurnRoom({ listHeight: LIST, keyboard: 0, topClear: TOP_CLEAR, endPad: END_PAD });
	const turnTop = HISTORY; // where the sent turn starts in the content
	let offset = HISTORY + END_PAD - LIST; // resting at the end before the send
	let following = true;
	let dragged = false;
	let next = 0;
	const frames: { at: number; offset: number; messageY: number; moved: number }[] = [];
	const contentChanges: number[] = [];
	let previousContent = HISTORY + END_PAD;
	for (let at = 0; at <= 1600 + snapshots.length * 220 + 1500; at += FRAME_MS) {
		while (next < snapshots.length && snapshots[next].at <= at) {
			const snapshot = snapshots[next++];
			receiveReplyText(pacer, text.slice(0, snapshot.length), snapshot.streaming, at);
		}
		stepReplyPacer(pacer, at);
		const shownReply = version === "now" ? pacer.shown : snapshots.filter((snapshot) => snapshot.at <= at).at(-1)?.length ?? 0;
		const turn = (at >= 100 ? 80 : 0) + (at >= 100 && at < 300 ? 18 : 0) + (at >= 400 ? 28 : 0) + (at >= 900 ? 44 : 0) + (at >= 1200 ? 44 : 0) +
			(shownReply ? Math.ceil(shownReply / CHARS_PER_LINE) * LINE : 0);
		const turnHeight = version === "now" && at >= 100 ? Math.max(turn, room) : turn;
		const content = HISTORY + turnHeight + END_PAD;
		const end = content - LIST;
		if (content !== previousContent) contentChanges.push(at);
		previousContent = content;
		const before = offset;
		if (drag && !dragged && at >= drag.at) {
			offset -= drag.by;
			following = false;
			dragged = true;
		} else if (following) {
			offset = version === "now" ? glideStep(offset, end, FRAME_MS, LIST) : end;
		}
		frames.push({ at, offset, messageY: turnTop - offset, moved: offset - before });
	}
	const streaming = frames.filter((frame) => frame.at >= 1600);
	const sendScroll = frames.filter((frame) => frame.at >= 100 && frame.at < 400 && Math.abs(frame.moved) > 0.01);
	return {
		/** Where the sent message rests once everything up to the reply is in. */
		messageYBeforeReply: Math.round(frames.find((frame) => frame.at >= 1500)!.messageY),
		/** Frames the move to the sent message took (1 is a snap). */
		sendScrollFrames: sendScroll.length,
		/** Times the list lurched from still by 10 pt or more in one frame while the reply streamed. */
		streamingSteps: streaming.filter((frame, index) => Math.abs(frame.moved) >= 10 && Math.abs(streaming[index - 1]?.moved ?? 0) < 1).length,
		maxStreamingMove: Math.round(Math.max(0, ...streaming.map((frame) => Math.abs(frame.moved)))),
		/** Frames the list moved while the reply streamed. */
		movingWhileStreaming: streaming.filter((frame) => Math.abs(frame.moved) > 0.01).length,
		/** After the drag, did anything move the list? */
		movedAfterDrag: drag ? frames.filter((frame) => frame.at > drag.at + FRAME_MS && Math.abs(frame.moved) > 0.01).length : 0,
		/** Layout changes to the content's height between the message landing and the reply. */
		contentChangesBeforeReply: contentChanges.filter((at) => at > 120 && at < 1600).length,
		finalGapToEnd: Math.round((HISTORY + Math.max(80 + 28 + 88 + Math.ceil(replyLength / CHARS_PER_LINE) * LINE, version === "now" ? room : 0) + END_PAD - LIST) - frames.at(-1)!.offset),
	};
}

describe("following a send, main against now", () => {
	const short = { main: simulate("main", 300), now: simulate("now", 300) };
	const long = { main: simulate("main", 2600), now: simulate("now", 2600) };
	const dragged = { main: simulate("main", 2600, { at: 2600, by: 400 }), now: simulate("now", 2600, { at: 2600, by: 400 }) };
	if (process.env.PACE_REPORT) console.table({ "short main": short.main, "short now": short.now, "long main": long.main, "long now": long.now, "drag main": dragged.main, "drag now": dragged.now });

	it("brings the sent message to the top of the visible list and keeps it there while the turn fills in", () => {
		expect(short.now.messageYBeforeReply).toBe(TOP_CLEAR);
		expect(short.now.contentChangesBeforeReply).toBe(0);
		// On main it sits at the bottom, under what came before it, and every row moves it.
		expect(short.main.messageYBeforeReply).toBeGreaterThan(LIST / 2);
		expect(short.main.contentChangesBeforeReply).toBeGreaterThanOrEqual(4);
	});

	it("holds still while a short reply fits", () => {
		expect(short.now.movingWhileStreaming).toBe(0);
		expect(short.main.movingWhileStreaming).toBeGreaterThan(0);
	});

	it("moves to the sent message in one glide rather than a snap", () => {
		expect(short.main.sendScrollFrames).toBeLessThanOrEqual(3);
		expect(short.now.sendScrollFrames).toBeGreaterThan(10);
	});

	it("glides with a reply that outgrows the screen, without lurches, and ends at its end", () => {
		expect(long.now.streamingSteps).toBe(0);
		expect(long.main.streamingSteps).toBeGreaterThan(10);
		expect(long.now.maxStreamingMove).toBeLessThan(long.main.maxStreamingMove);
		expect(long.now.finalGapToEnd).toBe(0);
		expect(long.main.finalGapToEnd).toBe(0);
	});

	it("leaves a list the person dragged where they put it", () => {
		expect(dragged.now.movedAfterDrag).toBe(0);
		expect(dragged.main.movedAfterDrag).toBe(0);
	});
});
