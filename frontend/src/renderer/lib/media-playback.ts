/**
 * The image or video an artifact viewer last showed for a file, and where its
 * video was: the comment workspace opens on the same file at the same moment
 * (no second download from the share host), and closing it comes back there.
 */
export type MediaPlayback = {
	src: string;
	crossOrigin?: "anonymous" | "use-credentials";
	/** Seconds into a video. */
	time: number;
};

const playback = new Map<string, MediaPlayback>();
/** Kept for the most recent files only. */
const KEEP = 20;

export function mediaPlaybackKey(sessionId: string, path: string, hostId?: string): string {
	return `${hostId ?? ""}\n${sessionId}\n${path}`;
}

export function mediaPlayback(key: string): MediaPlayback | undefined {
	return playback.get(key);
}

/** The file's source as shown; a new source starts at 00:00. */
export function rememberMediaSource(key: string, src: string, crossOrigin?: MediaPlayback["crossOrigin"]): void {
	const known = playback.get(key);
	playback.delete(key);
	playback.set(key, { src, crossOrigin, time: known?.src === src ? known.time : 0 });
	while (playback.size > KEEP) playback.delete(playback.keys().next().value!);
}

export function rememberMediaTime(key: string, time: number): void {
	const known = playback.get(key);
	if (known && Number.isFinite(time)) known.time = time;
}

/** Where the video was, whatever its source (the published copy of the same file). */
export function mediaTime(key: string): number {
	return playback.get(key)?.time ?? 0;
}

export function forgetMediaPlayback(): void {
	playback.clear();
}
