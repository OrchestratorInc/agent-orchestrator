import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// AsyncStorage and the documents folder, in memory.
const store = vi.hoisted(() => ({ items: new Map<string, string>(), files: new Map<string, string>(), failWrite: null as string | null, account: null as string | null, hosts: ["machine-a"] }));
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
		list() { return [...store.files.keys()].filter((key) => key.startsWith(`${this.path}/`) && key !== `${this.path}/`).map((key) => new File(this, key.slice(this.path.length + 1))); }
	}
	class File {
		readonly path: string;
		constructor(parent: { path: string }, name: string) { this.path = `${parent.path}/${name}`; }
		get uri() { return `file://${this.path}`; }
		get name() { return this.path.slice(this.path.lastIndexOf("/") + 1); }
		get exists() { return store.files.has(this.path); }
		write(content: string) {
			if (store.failWrite !== null && content === store.failWrite) throw new Error("disk full");
			store.files.set(this.path, content);
		}
		rename(name: string) { const content = store.files.get(this.path)!; store.files.delete(this.path); store.files.set(`${this.path.slice(0, this.path.lastIndexOf("/"))}/${name}`, content); }
		delete() { store.files.delete(this.path); }
		async base64() { return store.files.get(this.path) ?? ""; }
	}
	return { Directory, File, Paths: { document: { path: "/docs" } } };
});
vi.mock("./account", () => ({ loadAccount: async () => store.account ? { id: store.account, email: "a@example.com" } : null }));
vi.mock("./hosts", () => ({ loadHosts: async () => store.hosts.map((id) => ({ id })) }));

import { clearSpawnDraft, createSpawnDraftSaver, forgetSpawnDraftFor, loadSpawnDraft, saveSpawnDraft, SPAWN_DRAFT_KEEP_MS } from "./spawnDraft";

const image = { name: "screen shot.png", mimeType: "image/png", data: "aW1hZ2U=", bytes: 5, previewUri: "file:///cache/picked.png" };
const log = { name: "build.log", mimeType: "text/plain", data: "bG9n", bytes: 3 };

beforeEach(() => { store.items.clear(); store.files.clear(); store.failWrite = null; store.account = null; store.hosts = ["machine-a"]; });
const draftFiles = () => [...store.files.keys()].filter((key) => key.startsWith("/docs/spawn-draft/") && key !== "/docs/spawn-draft/");
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
		expect(await loadSpawnDraft()).toMatchObject({ prompt: "Keep me", attachments: [] });
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

	// An attachment's file is named for what is in it, not for its place: taking
	// one out never leaves another's restored preview on a deleted file.
	it("keeps the other attachments' files when one is removed", async () => {
		await saveSpawnDraft({ prompt: "Two files", attachments: [log, image] });
		const restored = (await loadSpawnDraft())!;
		const picture = restored.attachments[1]!;
		await saveSpawnDraft({ prompt: "Two files", attachments: [picture] });
		expect(store.files.has(picture.previewUri!.slice("file://".length))).toBe(true);
		expect(draftFiles()).toHaveLength(1);
		expect((await loadSpawnDraft())!.attachments.map((attachment) => attachment.previewUri)).toEqual([picture.previewUri]);
	});

	it("keeps the last draft whole when writing a new attachment fails", async () => {
		await saveSpawnDraft({ prompt: "Saved", attachments: [log] });
		const big = { name: "dump.bin", mimeType: "application/octet-stream", data: "ZHVtcA==", bytes: 4 };
		store.failWrite = big.data;
		await expect(saveSpawnDraft({ prompt: "Saved and more", attachments: [log, big] })).rejects.toThrow("disk full");
		const draft = await loadSpawnDraft();
		expect(draft?.prompt).toBe("Saved");
		expect(draft?.attachments.map(({ name, data }) => ({ name, data }))).toEqual([{ name: "build.log", data: "bG9n" }]);
		// Nothing half-written under a name a draft could point at.
		expect(draftFiles().every((key) => !key.endsWith("dump.bin"))).toBe(true);
	});

	it("keeps the Start it sent, so starting it again reuses the request id", async () => {
		await saveSpawnDraft({ prompt: "Ship it", attachments: [], request: { id: "req-1", payload: "{\"prompt\":\"Ship it\"}" } });
		expect((await loadSpawnDraft())?.request).toEqual({ id: "req-1", payload: "{\"prompt\":\"Ship it\"}" });
	});

	// A draft is the words of whoever was signed in: the next account must not find it.
	it("is not restored once its account signed out or another signed in", async () => {
		store.account = "user-1";
		await saveSpawnDraft({ prompt: "Private", attachments: [log], owner: { hostId: "machine-a", accountId: "user-1" } });
		expect((await loadSpawnDraft())?.prompt).toBe("Private");
		store.account = "user-2";
		expect(await loadSpawnDraft()).toBeNull();
		expect(store.items.size).toBe(0);
		expect(draftFiles()).toEqual([]);
	});

	it("is not restored once its machine was forgotten", async () => {
		await saveSpawnDraft({ prompt: "For a", attachments: [], owner: { hostId: "machine-a", accountId: null } });
		store.hosts = [];
		expect(await loadSpawnDraft()).toBeNull();
		// Orchestrator Cloud is never paired; its account decides.
		await saveSpawnDraft({ prompt: "For Cloud", attachments: [], owner: { hostId: "orchestrator-cloud", accountId: null } });
		expect((await loadSpawnDraft())?.prompt).toBe("For Cloud");
	});

	it("is deleted when its machine is forgotten or its account signs out", async () => {
		await saveSpawnDraft({ prompt: "For a", attachments: [log], owner: { hostId: "machine-a", accountId: "user-1" } });
		await forgetSpawnDraftFor({ hostId: "machine-b" });
		expect(store.items.size).toBe(1);
		await forgetSpawnDraftFor({ hostId: "machine-a" });
		expect(store.items.size).toBe(0);
		expect(draftFiles()).toEqual([]);
		await saveSpawnDraft({ prompt: "For a", attachments: [], owner: { hostId: "machine-a", accountId: "user-1" } });
		await forgetSpawnDraftFor({ accountId: "user-1" });
		expect(store.items.size).toBe(0);
	});
});
