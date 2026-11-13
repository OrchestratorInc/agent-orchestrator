import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// AsyncStorage and the documents folder, in memory.
const store = vi.hoisted(() => ({ items: new Map<string, string>(), files: new Map<string, string>() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => store.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => { store.items.delete(key); },
} }));
vi.mock("expo-file-system", () => {
	class Directory {
		readonly path: string;
		constructor(parent: { path?: string } | string, name: string) { this.path = `${typeof parent === "string" ? parent : parent.path}/${name}`; }
		get exists() { return [...store.files.keys()].some((key) => key.startsWith(`${this.path}/`)) || store.files.has(`${this.path}/`); }
		create() { store.files.set(`${this.path}/`, ""); }
		delete() { for (const key of [...store.files.keys()]) if (key.startsWith(`${this.path}/`)) store.files.delete(key); }
	}
	class File {
		readonly path: string;
		constructor(parent: { path: string }, name: string) { this.path = `${parent.path}/${name}`; }
		get uri() { return `file://${this.path}`; }
		get exists() { return store.files.has(this.path); }
		write(content: string) { store.files.set(this.path, content); }
		async base64() { return store.files.get(this.path) ?? ""; }
	}
	return { Directory, File, Paths: { document: { path: "/docs" } } };
});

import { clearSpawnDraft, createSpawnDraftSaver, loadSpawnDraft, saveSpawnDraft, SPAWN_DRAFT_KEEP_MS } from "./spawnDraft";

const image = { name: "screen shot.png", mimeType: "image/png", data: "aW1hZ2U=", bytes: 5, previewUri: "file:///cache/picked.png" };
const log = { name: "build.log", mimeType: "text/plain", data: "bG9n", bytes: 3 };

beforeEach(() => { store.items.clear(); store.files.clear(); });
afterEach(() => vi.useRealTimers());

describe("the New task draft", () => {
	it("comes back with its prompt and attachments after the app restarts", async () => {
		await saveSpawnDraft({ prompt: "Fix the flaky login test", attachments: [image, log] });
		const draft = await loadSpawnDraft();
		expect(draft?.prompt).toBe("Fix the flaky login test");
		expect(draft?.attachments.map(({ name, data, bytes }) => ({ name, data, bytes }))).toEqual([
			{ name: "screen shot.png", data: "aW1hZ2U=", bytes: 5 },
			{ name: "build.log", data: "bG9n", bytes: 3 },
		]);
		// An image previews from its saved copy: the picker's cached file may be gone.
		expect(draft?.attachments[0].previewUri).toMatch(/^file:\/\/\/docs\/spawn-draft\//);
		expect(draft?.attachments[1].previewUri).toBeUndefined();
	});

	it("keeps the prompt when an attachment's file was cleared", async () => {
		await saveSpawnDraft({ prompt: "Keep me", attachments: [log] });
		store.files.clear();
		expect(await loadSpawnDraft()).toEqual({ prompt: "Keep me", attachments: [] });
	});

	it("is gone once cleared, emptied, or a week old", async () => {
		await saveSpawnDraft({ prompt: "Started", attachments: [log] });
		await clearSpawnDraft();
		expect(await loadSpawnDraft()).toBeNull();
		expect([...store.files.keys()]).toEqual([]);

		await saveSpawnDraft({ prompt: "x", attachments: [] });
		await saveSpawnDraft({ prompt: "  ", attachments: [] });
		expect(await loadSpawnDraft()).toBeNull();

		await saveSpawnDraft({ prompt: "old" , attachments: [] }, 0);
		expect(await loadSpawnDraft(SPAWN_DRAFT_KEEP_MS)).toBeNull();
	});

	it("saves once typing pauses, at once on leaving the app, and never after the task started", async () => {
		vi.useFakeTimers();
		const saved: string[] = [];
		const saver = createSpawnDraftSaver(async (draft) => { saved.push(draft.prompt); }, 500);
		saver.schedule({ prompt: "F", attachments: [] });
		saver.schedule({ prompt: "Fix", attachments: [] });
		await vi.advanceTimersByTimeAsync(500);
		expect(saved).toEqual(["Fix"]);

		saver.schedule({ prompt: "Fix it", attachments: [] });
		await saver.flush();
		expect(saved).toEqual(["Fix", "Fix it"]);

		saver.schedule({ prompt: "Fix it now", attachments: [] });
		await saver.cancel();
		await vi.advanceTimersByTimeAsync(1_000);
		expect(saved).toEqual(["Fix", "Fix it"]);
	});
});
