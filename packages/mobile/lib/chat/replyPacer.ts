/**
 * Paces a live reply's text so bursty snapshots read as a steady stream.
 *
 * The phone never sees the provider's token stream: a change event starts a
 * read of the whole conversation (see `createRefreshScheduler`), so a reply
 * grows by a burst every read, a fifth of a second or more apart, and stops
 * dead while the agent runs a tool. Drawn as it lands, the text jumps a
 * sentence at a time.
 *
 * Once a reply has grown twice, it moves at the pace the agent writes at (as the
 * reads see it), holding about a read's worth in hand so the next read lands
 * before the text runs out: more in hand speeds it up, less slows it down. Paced
 * to the backlog alone (desktop's `useSmoothStreamingText`: each frame revealing
 * the share of the backlog that decays over a settle time), it rushed out as each
 * read landed and slowed nearly to a stop before the next. That decay still plays
 * a reply's first read, before any pace is known, and the rest of a finished
 * one. From the AI SDK's `smoothStream` it takes word chunking: a frame ends on a
 * nearby word boundary rather than mid-word.
 *
 * Text already present is never replayed (`createReplyPacer`'s `shown`), a
 * finished reply plays out what is left quickly and ends on the exact text, and
 * anything that is not a continuation of what is shown is shown whole.
 */

/** About 30 updates a second: each one lays out the reply's Markdown again. */
export const PACE_FRAME_MS = 33;
/** A long reply is laid out less often. */
export const PACE_LONG_FRAME_MS = 66;
export const PACE_LONG_CHARS = 4000;
const SETTLE_MS = 140;
const MAX_SETTLE_MS = 700;
/** The share of the gap between bursts a burst is spread over: text stays close behind the stream. */
const SETTLE_GAP_SHARE = 0.6;
/** A pause longer than this (a tool running) is not a burst rhythm. */
const MAX_GAP_MS = 1500;
const MAX_FRAME_DELTA_MS = 100;
/** Once the reply is done, what is left plays out at this settle time. */
const FINISH_SETTLE_MS = 90;
/** A finished reply further behind than this is shown whole. */
const FINISH_MAX_BACKLOG = 2000;
/** Live text never trails by more than this; a bigger burst skips ahead to `CATCH_UP_BACKLOG`. */
export const PACE_MAX_BACKLOG = 1500;
const CATCH_UP_BACKLOG = 600;
/** A new reply first seen at most this long plays from its start; longer, it is shown as it is. */
export const PACE_FIRST_PLAY_MAX = 600;
/** No frame for this long means nobody watched (the app was away): show the text. */
const STALL_MS = 1000;
/** How far a frame's end moves to land on a word boundary. */
const WORD_REACH = 12;
/** Reads' worth of text held in hand while the agent writes, so the next lands before it runs out. */
const RESERVE_READS = 1.2;
const MIN_RESERVE = 24;
/** How long the backlog is averaged over, so a read landing does not lurch the stream forward. */
const BACKLOG_AVERAGE_MS = 700;
/** The least and most of the agent's own pace the stream moves at. */
const MIN_PULL = 0.45;
const MAX_PULL = 3;

export type ReplyPacer = {
	/** The reply's text as the latest snapshot has it. */
	target: string;
	/** How much of `target` is shown, in UTF-16 units, always on a grapheme boundary. */
	shown: number;
	streaming: boolean;
	settleMs: number;
	gapMs?: number;
	/** How fast the agent writes, as reads see it, in characters a millisecond. */
	rate?: number;
	/** The backlog, averaged over BACKLOG_AVERAGE_MS. */
	backlogAverage?: number;
	/** Characters earned and not yet shown (a frame lands on a word boundary, ahead or behind). */
	credit: number;
	lastGrowthAt?: number;
	lastFrameAt?: number;
};

export function createReplyPacer(text: string, shown = text.length, streaming = false): ReplyPacer {
	return { target: text, shown: Math.min(Math.max(0, shown), text.length), streaming, settleMs: SETTLE_MS, credit: 0 };
}

export function replyVisibleText(pacer: ReplyPacer): string {
	return pacer.target.slice(0, pacer.shown);
}

export function replyPending(pacer: ReplyPacer): boolean {
	return pacer.shown < pacer.target.length;
}

export function showWholeReply(pacer: ReplyPacer, text: string, streaming = pacer.streaming): void {
	pacer.target = text;
	pacer.shown = text.length;
	pacer.streaming = streaming;
	pacer.lastFrameAt = undefined;
	pacer.credit = 0;
	pacer.backlogAverage = undefined;
}

/** A snapshot's text for the reply arrived. */
export function receiveReplyText(pacer: ReplyPacer, text: string, streaming: boolean, now: number): void {
	const visible = replyVisibleText(pacer);
	if (!text.startsWith(visible)) {
		// A correction or a rewrite: the snapshot is authoritative.
		showWholeReply(pacer, text, streaming);
		pacer.lastGrowthAt = undefined;
		pacer.gapMs = undefined;
		pacer.rate = undefined;
		pacer.settleMs = SETTLE_MS;
		return;
	}
	if (streaming && text.length > pacer.target.length) {
		if (pacer.lastGrowthAt !== undefined) {
			const gap = Math.min(now - pacer.lastGrowthAt, MAX_GAP_MS);
			pacer.gapMs = pacer.gapMs === undefined ? gap : pacer.gapMs * 0.6 + gap * 0.4;
			pacer.settleMs = Math.min(MAX_SETTLE_MS, Math.max(SETTLE_MS, pacer.gapMs * SETTLE_GAP_SHARE));
			const rate = (text.length - pacer.target.length) / Math.max(gap, PACE_FRAME_MS);
			pacer.rate = pacer.rate === undefined ? rate : pacer.rate * 0.7 + rate * 0.3;
		}
		pacer.lastGrowthAt = now;
	}
	pacer.target = text;
	pacer.streaming = streaming;
	const backlog = text.length - pacer.shown;
	if (!streaming && backlog > FINISH_MAX_BACKLOG) pacer.shown = text.length;
	else if (backlog > PACE_MAX_BACKLOG) pacer.shown = Math.max(pacer.shown, wordStart(text, text.length - CATCH_UP_BACKLOG));
	if (!replyPending(pacer)) pacer.lastFrameAt = undefined;
}

/**
 * One animation frame. Returns whether more text is now shown; a frame that
 * comes too soon after the last update shows nothing new.
 */
export function stepReplyPacer(pacer: ReplyPacer, now: number): boolean {
	const length = pacer.target.length;
	if (pacer.shown >= length) {
		pacer.lastFrameAt = undefined;
		return false;
	}
	const interval = pacer.shown < PACE_LONG_CHARS ? PACE_FRAME_MS : PACE_LONG_FRAME_MS;
	const last = pacer.lastFrameAt;
	if (last !== undefined) {
		if (now - last > STALL_MS) {
			showWholeReply(pacer, pacer.target);
			return true;
		}
		// A frame or two of slack: display frames do not land on 33 ms exactly.
		if (now - last < interval - 4) return false;
	}
	const elapsed = Math.min(MAX_FRAME_DELTA_MS, last === undefined ? interval : now - last);
	pacer.lastFrameAt = now;
	const backlog = length - pacer.shown;
	if (pacer.streaming && pacer.rate !== undefined && pacer.gapMs !== undefined) {
		// While the agent writes: its own pace, faster with more than a read's worth
		// in hand and slower with less, so text flows at an even speed rather than
		// rushing out as each read lands and slowing to a stop before the next.
		const reserve = Math.max(MIN_RESERVE, pacer.rate * pacer.gapMs * RESERVE_READS);
		const average = pacer.backlogAverage ?? backlog;
		pacer.backlogAverage = average + (backlog - average) * Math.min(1, elapsed / BACKLOG_AVERAGE_MS);
		// Up only as the average rises, so a read landing does not lurch the stream
		// forward; down as soon as little is left, so it never runs dry before the next.
		const held = Math.min(pacer.backlogAverage, backlog);
		const pull = Math.min(MAX_PULL, Math.max(MIN_PULL, 0.5 + 0.5 * (held / reserve)));
		pacer.credit = Math.min(backlog, pacer.credit + pacer.rate * pull * elapsed);
		if (pacer.credit < 1) return false;
		const from = pacer.shown;
		pacer.shown = revealEnd(pacer.target, from, from + Math.floor(pacer.credit));
		pacer.credit -= pacer.shown - from;
	} else {
		// The first read of a reply (no pace known yet) and a finished one: what is
		// waiting plays out over a settle time.
		const settle = pacer.streaming ? pacer.settleMs : FINISH_SETTLE_MS;
		const earned = Math.ceil(backlog * (1 - Math.exp(-elapsed / settle)));
		pacer.shown = revealEnd(pacer.target, pacer.shown, pacer.shown + earned);
		pacer.credit = 0;
	}
	if (pacer.shown >= length) pacer.lastFrameAt = undefined;
	return true;
}

function isSpace(code: number): boolean {
	return code === 0x20 || code === 0x0a || code === 0x09 || code === 0x0d || code === 0x3000 || code === 0xa0;
}

/**
 * Where a frame that earned up to `end` stops: the nearest word boundary
 * within reach, else the grapheme boundary at `end` (code, URLs and scripts
 * written without spaces must not hold the stream up). Always past `from`.
 */
export function revealEnd(text: string, from: number, end: number): number {
	if (end >= text.length) return text.length;
	for (let distance = 0; distance <= WORD_REACH; distance++) {
		const back = end - distance;
		if (back > from && isSpace(text.charCodeAt(back - 1))) return back;
		const forward = end + distance;
		if (forward <= text.length && isSpace(text.charCodeAt(forward - 1))) return forward;
	}
	return graphemeEnd(text, Math.max(end, from + 1));
}

/** The start of the word at `index`, if it starts close by; else a grapheme boundary. */
function wordStart(text: string, index: number): number {
	for (let at = index; at > Math.max(0, index - 40); at--) {
		if (isSpace(text.charCodeAt(at - 1))) return at;
	}
	return graphemeEnd(text, index);
}

// Hermes may not know Unicode property escapes; the common combining ranges stand in.
const MARK = (() => {
	try {
		return new RegExp("^\\p{M}$", "u");
	} catch {
		return /^[̀-ͯ҃-҉֑-ֽؐ-ًؚ-ٟऀ-ःऺ-ॏัิ-ฺ็-๎᪰-᫿᷀-᷿⃐-⃿︠-︯]$/;
	}
})();

function extendsCluster(codePoint: number): boolean {
	return codePoint === 0x200d || codePoint === 0x20e3 ||
		(codePoint >= 0xfe00 && codePoint <= 0xfe0f) ||
		(codePoint >= 0x1f3fb && codePoint <= 0x1f3ff) ||
		(codePoint >= 0xe0020 && codePoint <= 0xe007f) ||
		MARK.test(String.fromCodePoint(codePoint));
}

function isRegionalIndicator(codePoint: number | undefined): boolean {
	return codePoint !== undefined && codePoint >= 0x1f1e6 && codePoint <= 0x1f1ff;
}

/**
 * The first boundary at or after `index` that splits no grapheme: not inside a
 * surrogate pair, a combining sequence, an emoji joined by ZWJ or carrying a
 * modifier, or a flag's pair of regional indicators.
 */
export function graphemeEnd(text: string, index: number): number {
	let at = Math.min(Math.max(0, index), text.length);
	while (at > 0 && at < text.length) {
		const code = text.charCodeAt(at);
		if (code >= 0xdc00 && code <= 0xdfff) {
			at++;
			continue;
		}
		const codePoint = text.codePointAt(at) ?? code;
		const width = codePoint > 0xffff ? 2 : 1;
		if (text.charCodeAt(at - 1) === 0x200d || extendsCluster(codePoint)) {
			at += width;
			continue;
		}
		if (isRegionalIndicator(codePoint)) {
			let run = 0;
			for (let back = at; back >= 2 && isRegionalIndicator(text.codePointAt(back - 2)); back -= 2) run++;
			if (run % 2 === 1) {
				at += width;
				continue;
			}
		}
		break;
	}
	return at;
}
