export const STREAM_SETTLE_MS = 140;
export const STREAM_MIN_FRAME_MS = 32;
export const STREAM_MAX_FRAME_DELTA_MS = 100;
export const STREAM_SMOOTH_MAX_CHARS = 4000;
export const STREAM_LONG_UPDATE_MS = 100;
export const STREAM_STALL_MS = 1000;

// Spread a burst over most of the recent arrival gap. Cap the sample so a
// pause for a tool call does not set the pace for the rest of the reply.
export function streamGapMs(previousGap: number | undefined, elapsedMs: number): number {
	const gap = Math.min(elapsedMs, 1500);
	return previousGap === undefined ? gap : previousGap * 0.6 + gap * 0.4;
}

export function streamSettleMs(gapMs: number): number {
	return Math.min(700, Math.max(STREAM_SETTLE_MS, gapMs * 0.6));
}

export function streamRevealCount(backlog: number, elapsedMs: number, settleMs: number): number {
	return Math.ceil(backlog * (1 - Math.exp(-elapsedMs / settleMs)));
}

// A later combining mark or joiner can change the last visible grapheme.
// Rewind to its previous boundary before revealing it with the new suffix.
export function reconciledStreamPrefix(visibleText: string, targetGraphemes: readonly string[]) {
	let boundary = 0;
	let count = 0;
	for (const grapheme of targetGraphemes) {
		const nextBoundary = boundary + grapheme.length;
		if (nextBoundary > visibleText.length) break;
		boundary = nextBoundary;
		count++;
	}
	return { text: visibleText.slice(0, boundary), count };
}
