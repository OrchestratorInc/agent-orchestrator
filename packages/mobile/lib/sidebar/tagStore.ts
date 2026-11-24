/**
 * The user's home tags (see sidebarTags.ts), kept on the phone. One store, so
 * the tag page and home see the same tags the moment one is saved.
 */
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useSyncExternalStore } from "react";
import { normalizedTag, type SidebarTag } from "./sidebarTags";

const STORAGE_KEY = "ao.sidebar.tags.v1";

let tags: SidebarTag[] = [];
let loaded = false;
const listeners = new Set<() => void>();

function emit() {
	for (const listener of listeners) listener();
}

function persist() {
	void AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(tags)).catch(() => {});
}

/** Reads the stored tags once. */
export function loadTags(): Promise<void> {
	if (loaded) return Promise.resolve();
	loaded = true;
	return AsyncStorage.getItem(STORAGE_KEY).then((raw) => {
		const stored = raw ? JSON.parse(raw) as unknown : null;
		if (Array.isArray(stored)) {
			tags = stored.filter((item): item is SidebarTag => Boolean(item) && typeof item.id === "string" && typeof item.name === "string" && Array.isArray(item.projects) && Array.isArray(item.sessions));
			emit();
		}
	}).catch(() => {});
}

/** Adds the tag, or replaces the one with its id. */
export function saveTag(tag: SidebarTag) {
	const next = normalizedTag(tag);
	tags = tags.some((item) => item.id === next.id) ? tags.map((item) => (item.id === next.id ? next : item)) : [...tags, next];
	persist();
	emit();
}

export function deleteTag(id: string) {
	tags = tags.filter((item) => item.id !== id);
	persist();
	emit();
}

export function newTagId(): string {
	return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

export function useTags(): readonly SidebarTag[] {
	return useSyncExternalStore(subscribe, () => tags, () => tags);
}

// A tag just made is chosen on home: the tag page asks, home takes it once.
let pendingSelection: string | undefined;

export function requestTagSelection(id: string) {
	pendingSelection = id;
	emit();
}

export function takeTagSelection(): string | undefined {
	const id = pendingSelection;
	pendingSelection = undefined;
	return id;
}
