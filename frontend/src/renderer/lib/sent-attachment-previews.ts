/**
 * In-memory copies of files the user just sent, by staged path.
 *
 * A sent message names its files by their staged workspace path, so its tiles
 * read them back from the daemon. Right after a send that read is still in
 * flight, and the tiles drew empty under a bubble that was already there. The
 * composer holds the bytes when it sends, so it leaves an object URL here and
 * the tiles show that first: the local echo, the steer row, the queued row and
 * the daemon's own message that replaces them all draw the same picture at once.
 *
 * An entry stops being handed out after a few minutes, long enough for the
 * daemon's copy to replace the echo, and its URL is revoked once no mounted
 * tile still shows it. A tile keeps the URL it mounted with, so nothing reloads
 * or flickers under the reader; one mounted later reads the daemon's copy. A
 * local copy that fails to draw, or was revoked before its tile could hold it,
 * hands the tile over to the daemon's copy too.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

/** How long a new tile is given the local copy. */
const HANDOUT_MS = 5 * 60_000;
/** The most bytes kept at once; the oldest entries go first past it. */
const MAX_HELD_BYTES = 256 * 1024 * 1024;
/** Base64 bigger than this is not decoded on the send path; the daemon's copy is used. */
const MAX_DECODE_CHARS = 24 * 1024 * 1024;

type Entry = { url: string; bytes: number; users: number; retired: boolean; timer?: ReturnType<typeof setTimeout> };

const entries = new Map<string, Entry>();

export type SentAttachmentSource = {
	path: string;
	mimeType: string;
	bytes: number;
	/** The picked file, when the composer still has it: no copy is made. */
	file?: Blob;
	/** Base64 without the data: prefix, when the file itself is gone. */
	data?: string;
};

function decode(data: string, mimeType: string): Blob | undefined {
	if (data.length > MAX_DECODE_CHARS) return undefined;
	try {
		const binary = atob(data);
		const bytes = new Uint8Array(binary.length);
		for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
		return new Blob([bytes], { type: mimeType });
	} catch {
		return undefined;
	}
}

function drop(path: string, entry: Entry): void {
	if (entry.timer !== undefined) clearTimeout(entry.timer);
	if (entries.get(path) === entry) entries.delete(path);
	URL.revokeObjectURL(entry.url);
}

function retire(path: string, entry: Entry): void {
	entry.retired = true;
	if (entry.users === 0) drop(path, entry);
}

/** Keep local copies of these files for the message about to show them. */
export function rememberSentAttachments(sources: readonly SentAttachmentSource[]): void {
	// Test DOMs have no object URLs; the tiles then read the daemon's copy as before.
	if (typeof URL.createObjectURL !== "function") return;
	for (const source of sources) {
		const existing = entries.get(source.path);
		if (existing && !existing.retired) continue;
		const blob = source.file ?? (source.data ? decode(source.data, source.mimeType) : undefined);
		if (!blob) continue;
		const entry: Entry = { url: URL.createObjectURL(blob), bytes: blob.size || source.bytes, users: 0, retired: false };
		entry.timer = setTimeout(() => retire(source.path, entry), HANDOUT_MS);
		entries.set(source.path, entry);
	}
	let held = 0;
	for (const entry of entries.values()) if (!entry.retired) held += entry.bytes;
	// Map order is insertion order, so this retires the oldest first.
	for (const [path, entry] of entries) {
		if (held <= MAX_HELD_BYTES) break;
		if (entry.retired) continue;
		held -= entry.bytes;
		retire(path, entry);
	}
}

/** The local copy of a staged file, while one is handed out. */
export function sentAttachmentPreview(path: string | undefined): string | undefined {
	if (!path) return undefined;
	const entry = entries.get(path);
	return entry && !entry.retired ? entry.url : undefined;
}

/** Count a tile as showing this URL; undefined when the URL is no longer live. */
function hold(path: string, url: string): (() => void) | undefined {
	const entry = entries.get(path);
	if (!entry || entry.url !== url) return undefined;
	entry.users++;
	return () => {
		entry.users--;
		if (entry.retired && entry.users === 0) drop(path, entry);
	};
}

export type SentAttachmentPreview = {
	/** The local copy, until it fails; the tile then reads the daemon's copy. */
	url: string | undefined;
	/** Call when the local copy fails to load. Undefined once the tile has no local copy. */
	onError: (() => void) | undefined;
};

/**
 * The local copy of a staged file for a tile, kept for as long as the tile is
 * mounted. Read once per path, so the source never swaps under a drawn tile,
 * except once, to the daemon's copy, if the local one fails.
 */
export function useSentAttachmentPreview(path: string | undefined): SentAttachmentPreview {
	const url = useMemo(() => sentAttachmentPreview(path), [path]);
	// The URL that failed rather than a flag, so a new path gets its own attempt.
	const [failed, setFailed] = useState<string>();
	const live = url !== undefined && url !== failed ? url : undefined;
	// Keyed on the live URL, so a tile that gave up its local copy also lets it go.
	useEffect(() => {
		if (!path || !live) return undefined;
		const release = hold(path, live);
		// Revoked between this render and now (retired with no other tile holding it).
		if (!release) setFailed(live);
		return release;
	}, [path, live]);
	const onError = useCallback(() => {
		if (url) setFailed(url);
	}, [url]);
	return { url: live, onError: live ? onError : undefined };
}

/** Test seam: forget every entry. */
export function resetSentAttachmentPreviewsForTest(): void {
	for (const [path, entry] of entries) drop(path, entry);
}
