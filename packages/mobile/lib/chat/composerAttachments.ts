import { useCallback, useSyncExternalStore } from "react";
import type { ComposerTrayItem } from "./ComposerAttachmentTray";
import type { ChatImage, ChatResource } from "./types";

export type ComposerAttachment =
	| { id: string; kind: "image"; name: string; bytes: number; image: ChatImage }
	| { id: string; kind: "resource"; name: string; bytes: number; resource: ChatResource };

/** A session's unsent attachments, and what the last pick had to say about them. */
export type ComposerAttachmentState = { items: readonly ComposerAttachment[]; notice?: string };

const EMPTY: ComposerAttachmentState = { items: [] };

// Kept outside the composer on purpose. On Android the photo and file pickers
// are separate activities, so opening one backgrounds the app; the store then
// drops its config and the session screen unmounts the chat. The pick resolves
// into the old composer, so whatever it chose has to land somewhere the new one
// reads. Keyed like the text draft: by machine and session.
const states = new Map<string, ComposerAttachmentState>();
const listeners = new Map<string, Set<() => void>>();

export function readComposerAttachments(key: string): ComposerAttachmentState {
	return states.get(key) ?? EMPTY;
}

/** Updates from the stored value, never a render's copy, which may belong to an unmounted composer. */
export function updateComposerAttachments(key: string, update: (current: ComposerAttachmentState) => ComposerAttachmentState): void {
	const current = readComposerAttachments(key);
	const next = update(current);
	if (next === current) return;
	if (next.items.length || next.notice) states.set(key, next);
	else states.delete(key);
	listeners.get(key)?.forEach((listener) => listener());
}

/** Drops every session's unsent attachments on one machine (`machine.` keys). */
export function forgetMachineComposerAttachments(machine: string): void {
	const prefix = `${machine}.`;
	for (const key of [...states.keys()]) {
		if (!key.startsWith(prefix)) continue;
		states.delete(key);
		listeners.get(key)?.forEach((listener) => listener());
	}
}

/** Drops one attachment the user removed, keeping what the last pick had to say. */
export function removeComposerAttachment(key: string, id: string): void {
	updateComposerAttachments(key, (current) => ({ items: current.items.filter((item) => item.id !== id), notice: current.notice }));
}

/**
 * What the composer's tray draws for each attachment: a photo from its own
 * bytes, anything else by name. Each data URI copies the whole photo, so
 * callers memoize this on the attachments rather than building it per render.
 */
export function composerTrayItems(items: readonly ComposerAttachment[]): ComposerTrayItem[] {
	return items.map((item) => item.kind === "image"
		? { key: item.id, name: item.name, previewUri: `data:${item.image.mimeType};base64,${item.image.data}` }
		: { key: item.id, name: item.name });
}

/**
 * Drops what a send delivered and nothing else. The send can finish after the
 * composer that started it was replaced, and the new one may have added more.
 */
export function forgetComposerAttachments(key: string, ids: ReadonlySet<string>): void {
	updateComposerAttachments(key, (current) => {
		const items = current.items.filter((item) => !ids.has(item.id));
		return items.length === current.items.length ? current : { items, notice: current.notice };
	});
}

export function useComposerAttachments(key: string): ComposerAttachmentState {
	const subscribe = useCallback((listener: () => void) => {
		let set = listeners.get(key);
		if (!set) listeners.set(key, set = new Set());
		set.add(listener);
		return () => {
			set.delete(listener);
			if (!set.size) listeners.delete(key);
		};
	}, [key]);
	const read = useCallback(() => readComposerAttachments(key), [key]);
	return useSyncExternalStore(subscribe, read, read);
}
