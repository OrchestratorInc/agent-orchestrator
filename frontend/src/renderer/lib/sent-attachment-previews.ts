import { useCallback, useEffect, useMemo, useState } from "react";

const HANDOUT_MS = 5 * 60_000;
const MAX_HELD_BYTES = 256 * 1024 * 1024;
const MAX_DECODE_CHARS = 24 * 1024 * 1024;

type Entry = { url: string; bytes: number; users: number; retired: boolean; timer?: ReturnType<typeof setTimeout> };

const entries = new Map<string, Entry>();

export type SentAttachmentSource = {
	path: string;
	mimeType: string;
	bytes: number;
	file?: Blob;
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

export function rememberSentAttachments(sources: readonly SentAttachmentSource[]): void {
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
	for (const [path, entry] of entries) {
		if (held <= MAX_HELD_BYTES) break;
		if (entry.retired) continue;
		held -= entry.bytes;
		retire(path, entry);
	}
}

export function sentAttachmentPreview(path: string | undefined): string | undefined {
	if (!path) return undefined;
	const entry = entries.get(path);
	return entry && !entry.retired ? entry.url : undefined;
}

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
	url: string | undefined;
	onError: (() => void) | undefined;
};

export function useSentAttachmentPreview(path: string | undefined): SentAttachmentPreview {
	const url = useMemo(() => sentAttachmentPreview(path), [path]);
	const [failed, setFailed] = useState<string>();
	const live = url !== undefined && url !== failed ? url : undefined;
	useEffect(() => {
		if (!path || !live) return undefined;
		const release = hold(path, live);
		if (!release) setFailed(live);
		return release;
	}, [path, live]);
	const onError = useCallback(() => {
		if (url) setFailed(url);
	}, [url]);
	return { url: live, onError: live ? onError : undefined };
}

export function resetSentAttachmentPreviewsForTest(): void {
	for (const [path, entry] of entries) drop(path, entry);
}
