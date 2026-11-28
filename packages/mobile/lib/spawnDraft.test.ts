import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// AsyncStorage and the documents folder, in memory.
const store = vi.hoisted(() => ({ items: new Map<string, string>(), files: new Map<string, string>(), failWrite: null as string | null, failRemove: false, account: null as string | null, hosts: ["machine-a"] }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => store.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => {
		if (store.failRemove) throw new Error("storage busy");
		store.items.delete(key);
	},
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

import { CloudError } from "./cloud/errors";
import { attachmentsKey, clearSpawnDraft, clearStartedSpawnDraft, createSpawnDraftSaver, forgetSpawnDraftFor, loadSpawnDraft, requestMatches, saveBeforeStart, saveSpawnDraft, SPAWN_DRAFT_KEEP_MS, SPAWN_DRAFT_KEY, startRefused } from "./spawnDraft";

const image = { name: "screen shot.png", mimeType: "image/png", data: "aW1hZ2U=", bytes: 5, previewUri: "file:///cache/picked.png" };
const log = { name: "build.log", mimeType: "text/plain", data: "bG9n", bytes: 3 };

beforeEach(() => { store.items.clear(); store.files.clear(); store.failWrite = null; store.failRemove = false; store.account = null; store.hosts = ["machine-a"]; });
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
		const request = { id: "req-1", payload: "{\"prompt\":\"Ship it\"}", attachments: attachmentsKey([log]) };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [log], request });
		const restored = (await loadSpawnDraft())!;
		expect(restored.request).toEqual(request);
		// The restored files are new objects, read back from disk: still the same Start.
		expect(requestMatches(restored.request, request.payload, restored.attachments)).toBe(true);
	});

	// The default row's model is the catalog's, which can change before a
	// retry: the restored Start sends the model it first went with, so the
	// retry's body matches the request id's.
	it("keeps the model a Start sent for the default row", async () => {
		const request = { id: "req-2", payload: "{\"prompt\":\"Ship it\"}", attachments: attachmentsKey([]), model: "opus" };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [], request });
		expect((await loadSpawnDraft())?.request).toEqual(request);
		// A malformed model is left out.
		store.items.set(SPAWN_DRAFT_KEY, JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "x", attachments: [], request: { id: "r", payload: "{}", attachments: attachmentsKey([]), model: 3 } }));
		expect((await loadSpawnDraft())?.request).toEqual({ id: "r", payload: "{}", attachments: attachmentsKey([]) });
	});

	// A draft saved before the field existed kept a picked model only in its
	// payload. Its retry still matches that payload and reuses the request id,
	// so it must send that model too, or the daemon, which fingerprints the
	// body, refuses a request it already took.
	it("recovers a picked model from a draft saved before the field existed", async () => {
		const payload = JSON.stringify({ hostId: "machine-a", prompt: "Ship it", harness: "claude-code", model: "sonnet", mode: "chat" });
		store.items.set(SPAWN_DRAFT_KEY, JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "Ship it", attachments: [], request: { id: "legacy", payload, attachments: attachmentsKey([]) } }));
		expect((await loadSpawnDraft())?.request).toEqual({ id: "legacy", payload, attachments: attachmentsKey([]), model: "sonnet" });
		// No model in the payload, or one that is not JSON: none.
		const bare = JSON.stringify({ hostId: "machine-a", prompt: "Ship it" });
		store.items.set(SPAWN_DRAFT_KEY, JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "Ship it", attachments: [], request: { id: "bare", payload: bare, attachments: attachmentsKey([]) } }));
		expect((await loadSpawnDraft())?.request?.model).toBeUndefined();
		store.items.set(SPAWN_DRAFT_KEY, JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "Ship it", attachments: [], request: { id: "odd", payload: "not json", attachments: attachmentsKey([]) } }));
		expect((await loadSpawnDraft())?.request).toEqual({ id: "odd", payload: "not json", attachments: attachmentsKey([]) });
	});

	// Cleared after the daemon took the Start, but the draft could not be
	// removed: its files stay, so the restored draft is still that Start (same
	// attachments, same request id) and the daemon answers with the same task.
	it("keeps the attachment files when removing the draft fails", async () => {
		const request = { id: "req-1", payload: "{\"prompt\":\"Ship it\"}", attachments: attachmentsKey([log]) };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [log], request });
		store.failRemove = true;
		await expect(clearSpawnDraft()).rejects.toThrow("storage busy");
		store.failRemove = false;
		const restored = (await loadSpawnDraft())!;
		expect(restored.attachments.map((attachment) => attachment.name)).toEqual(["build.log"]);
		expect(requestMatches(restored.request, request.payload, restored.attachments)).toBe(true);
	});

	// After a Start that did not land, the attachments changed: the next Start
	// sends something else and must not reuse the id the daemon may know.
	it("does not reuse the request id once the attachments or the request changed", async () => {
		const request = { id: "req-1", payload: "{\"prompt\":\"Ship it\"}", attachments: attachmentsKey([log]) };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [log, image], request });
		const restored = (await loadSpawnDraft())!;
		expect(requestMatches(restored.request, request.payload, restored.attachments)).toBe(false);
		expect(requestMatches(request, request.payload, [image])).toBe(false);
		expect(requestMatches(request, "{\"prompt\":\"Ship it now\"}", [log])).toBe(false);
		expect(requestMatches(undefined, request.payload, [log])).toBe(false);
		// The same file picked again is the same attachment.
		expect(requestMatches(request, request.payload, [{ ...log }])).toBe(true);
	});

	// A Start sends each attachment's type and bytes, not its name.
	it("matches a Start by the attachments' types and bytes, not their names", () => {
		const request = { id: "req-1", payload: "{}", attachments: attachmentsKey([log]) };
		expect(requestMatches(request, request.payload, [{ ...log, mimeType: "application/octet-stream" }])).toBe(false);
		expect(requestMatches(request, request.payload, [{ ...log, name: "renamed.log" }])).toBe(true);
		expect(requestMatches(request, request.payload, [{ ...log, data: "bG9nIQ==" }])).toBe(false);
		expect(attachmentsKey([log, image])).not.toBe(attachmentsKey([image, log]));
	});

	// What was picked goes into the Start: restored without it (a model
	// override), the next Start would send another request under a new id.
	it("keeps what was picked: project, agent, model override, level, approval, mode, agent account", async () => {
		const selections = { projectId: "web-app", harness: "codex", model: "gpt-5", effort: "high", approvalMode: "auto", mode: "tui", accountId: "work", accountHarness: "codex" };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [], selections });
		expect((await loadSpawnDraft())?.selections).toEqual(selections);
		// Anything not shaped as written is left out, not handed to the page.
		store.items.set(SPAWN_DRAFT_KEY, JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "x", attachments: [], selections: { model: 3, mode: "chat" }, request: { id: "r" } }));
		const odd = await loadSpawnDraft();
		expect(odd?.selections).toEqual({ mode: "chat" });
		expect(odd?.request).toBeUndefined();
	});

	// The draft saves on every pause in typing: each attachment is hashed once,
	// not its whole base64 again on every save.
	it("hashes each attachment once, however often the draft is saved", async () => {
		let reads = 0;
		const counted = { name: "photo.jpg", mimeType: "image/jpeg", bytes: 4, get data() { reads++; return "cGhvdG8="; } };
		await saveSpawnDraft({ prompt: "one", attachments: [counted] });
		const afterFirst = reads;
		await saveSpawnDraft({ prompt: "two", attachments: [counted] });
		await saveSpawnDraft({ prompt: "three", attachments: [counted] });
		attachmentsKey([counted]);
		expect(reads).toBe(afterFirst);
	});

	// Start must not wait on storage, but a draft without this Start's id must
	// not survive to be restored and sent again as a second task.
	it("before a Start, retries a failed save once, then deletes the draft rather than keep one without the id", async () => {
		await saveSpawnDraft({ prompt: "Ship it", attachments: [] });
		let attempts = 0;
		const failing = createSpawnDraftSaver(async () => { attempts++; throw new Error("disk full"); });
		const request = { id: "req-1", payload: "{}", attachments: "" };
		expect(await saveBeforeStart(failing, { prompt: "Ship it", attachments: [], request })).toBe(false);
		expect(attempts).toBe(2);
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(false);

		const working = createSpawnDraftSaver();
		expect(await saveBeforeStart(working, { prompt: "Ship it", attachments: [], request })).toBe(true);
		expect((await loadSpawnDraft())?.request).toEqual(request);
	});

	// The account changed, or the draft's machine was forgotten, while the save
	// before a Start waited: that save was dropped, and the Start must not go.
	it("reports a save before a Start that a cancel or a forget dropped, and leaves the draft alone", async () => {
		await saveSpawnDraft({ prompt: "Another page's draft", attachments: [] });
		let release!: () => void;
		const blocking = createSpawnDraftSaver(async () => { await new Promise<void>((done) => { release = done; }); });
		blocking.schedule({ prompt: "other", attachments: [] });
		const writing = blocking.flush();
		await vi.waitFor(() => expect(release).toBeDefined());
		const saved: string[] = [];
		const saver = createSpawnDraftSaver(async (draft) => { saved.push(draft.prompt); });
		const request = { id: "req-1", payload: "{}", attachments: "" };
		const starting = saveBeforeStart(saver, { prompt: "Ship it", attachments: [], request });
		await Promise.resolve();
		void saver.cancel();
		release();
		await writing;
		expect(await starting).toBe("dropped");
		expect(saved).toEqual([]);
		expect((await loadSpawnDraft())?.prompt).toBe("Another page's draft");

		const owner = { hostId: "machine-a", accountId: null };
		blocking.schedule({ prompt: "other", attachments: [] });
		release = undefined as unknown as () => void;
		const writingAgain = blocking.flush();
		await vi.waitFor(() => expect(release).toBeDefined());
		saver.schedule({ prompt: "Ship it", attachments: [], owner, request });
		const flushing = saver.flush();
		const forgetting = forgetSpawnDraftFor({ hostId: "machine-a" });
		release();
		await Promise.all([writingAgain, forgetting]);
		expect(await flushing).toBe("dropped");
	});

	// Two pages open: one's Start finishing must not delete the draft the other saved meanwhile.
	it("clears the started draft only while the phone still holds it", async () => {
		const request = { id: "req-1", payload: "{}", attachments: "" };
		await saveSpawnDraft({ prompt: "Ship it", attachments: [], request });
		await saveSpawnDraft({ prompt: "Next task", attachments: [log] });
		await clearStartedSpawnDraft("req-1");
		expect((await loadSpawnDraft())?.prompt).toBe("Next task");
		expect(draftFiles()).toHaveLength(1);
		await saveSpawnDraft({ prompt: "Ship it", attachments: [log], request });
		await clearStartedSpawnDraft("req-1");
		expect(await loadSpawnDraft()).toBeNull();
		expect(draftFiles()).toEqual([]);
	});

	it("reports a failed save from flush, without rejecting", async () => {
		let fail = true;
		const saver = createSpawnDraftSaver(async () => { if (fail) throw new Error("disk full"); });
		saver.schedule({ prompt: "a", attachments: [] });
		expect(await saver.flush()).toBe(false);
		fail = false;
		saver.schedule({ prompt: "b", attachments: [] });
		expect(await saver.flush()).toBe(true);
	});

	it("keeps a failed background save for the next flush, unless a newer draft replaced it", async () => {
		const saved: string[] = [];
		let fail = true;
		const saver = createSpawnDraftSaver(async (draft) => { if (fail) throw new Error("disk full"); saved.push(draft.prompt); });
		saver.schedule({ prompt: "latest edits", attachments: [] });
		expect(await saver.flush()).toBe(false);
		fail = false;
		// Leaving the app later: the failed draft is tried again.
		expect(await saver.flush()).toBe(true);
		expect(saved).toEqual(["latest edits"]);

		fail = true;
		saver.schedule({ prompt: "older", attachments: [] });
		const failing = saver.flush();
		saver.schedule({ prompt: "newer", attachments: [] });
		expect(await failing).toBe(false);
		fail = false;
		expect(await saver.flush()).toBe(true);
		expect(saved).toEqual(["latest edits", "newer"]);
	});

	it("does not retry an older failed save once a newer one is queued", async () => {
		const saved: string[] = [];
		let failOlder!: () => void;
		const saver = createSpawnDraftSaver(async (draft) => {
			if (draft.prompt === "older") await new Promise<void>((_done, reject) => { failOlder = () => reject(new Error("disk full")); });
			saved.push(draft.prompt);
		});
		saver.schedule({ prompt: "older", attachments: [] });
		const older = saver.flush();
		await vi.waitFor(() => expect(failOlder).toBeDefined());
		saver.schedule({ prompt: "newer", attachments: [] });
		const newer = saver.flush();
		failOlder();
		expect(await older).toBe(false);
		expect(await newer).toBe(true);
		await saver.flush();
		expect(saved).toEqual(["newer"]);
	});

	// Read in the same queue as saves: another page's save never deletes the
	// attachment files of the draft being read.
	it("reads the draft in the queue saves and removals use", async () => {
		await saveSpawnDraft({ prompt: "Keep me", attachments: [log] });
		let release!: () => void;
		const blocking = createSpawnDraftSaver(async () => { await new Promise<void>((done) => { release = done; }); });
		blocking.schedule({ prompt: "other", attachments: [] });
		const saving = blocking.flush();
		await vi.waitFor(() => expect(release).toBeDefined());
		let loaded = false;
		const loading = loadSpawnDraft().then((draft) => { loaded = true; return draft; });
		await new Promise((done) => setTimeout(done, 0));
		expect(loaded).toBe(false);
		release();
		await saving;
		expect((await loading)?.attachments).toHaveLength(1);
	});

	it("does not retry a failed save after a Start cancelled it", async () => {
		let attempts = 0;
		let fail!: () => void;
		const saver = createSpawnDraftSaver(async () => { attempts++; await new Promise<void>((_done, reject) => { fail = () => reject(new Error("disk full")); }); });
		saver.schedule({ prompt: "a", attachments: [] });
		const flushing = saver.flush();
		await vi.waitFor(() => expect(attempts).toBe(1));
		void saver.cancel();
		fail();
		expect(await flushing).toBe(false);
		await saver.flush();
		expect(attempts).toBe(1);
	});

	// A failed read is not an empty draft: the page must not save over it.
	it("rejects when the saved draft could not be read", async () => {
		await saveSpawnDraft({ prompt: "Keep me", attachments: [] });
		const { default: AsyncStorage } = await import("@react-native-async-storage/async-storage");
		const getItem = vi.spyOn(AsyncStorage, "getItem").mockRejectedValueOnce(new Error("storage busy"));
		await expect(loadSpawnDraft()).rejects.toThrow("storage busy");
		getItem.mockRestore();
		expect((await loadSpawnDraft())?.prompt).toBe("Keep me");
	});

	// Two open pages (a cloud project's page under a second New task) save in
	// one queue: one's file cleanup never runs between the other's file write
	// and its commit.
	it("runs every saver's saves and removals one at a time", async () => {
		const events: string[] = [];
		let release!: () => void;
		const first = createSpawnDraftSaver(async () => { events.push("first:start"); await new Promise<void>((done) => { release = done; }); events.push("first:end"); });
		const second = createSpawnDraftSaver(async () => { events.push("second"); });
		first.schedule({ prompt: "a", attachments: [] });
		const firstSave = first.flush();
		await Promise.resolve();
		second.schedule({ prompt: "b", attachments: [] });
		const secondSave = second.flush();
		const clearing = clearSpawnDraft().then(() => { events.push("clear"); });
		await new Promise((done) => setTimeout(done, 0));
		expect(events).toEqual(["first:start"]);
		release();
		await Promise.all([firstSave, secondSave, clearing]);
		expect(events).toEqual(["first:start", "first:end", "second", "clear"]);
	});

	// A save still waiting, or queued behind one writing, when the machine is
	// forgotten or the account signs out must not write the draft back after.
	it("drops saves of a forgotten owner's draft that are still waiting or queued", async () => {
		vi.useFakeTimers();
		const owner = { hostId: "machine-a", accountId: "user-1" };
		const saver = createSpawnDraftSaver(undefined, 500);
		saver.schedule({ prompt: "Private", attachments: [log], owner });
		await forgetSpawnDraftFor({ accountId: "user-1" });
		await vi.advanceTimersByTimeAsync(1_000);
		await saver.flush();
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(false);
		expect(draftFiles()).toEqual([]);

		// Queued behind a save that is writing: the writing one finishes before
		// the draft is deleted, the queued one never runs.
		let finish = () => {};
		const writes: string[] = [];
		const slow = createSpawnDraftSaver(async (draft) => {
			if (draft.prompt === "first") await new Promise<void>((resolve) => { finish = resolve; });
			writes.push(draft.prompt);
			await saveSpawnDraft(draft);
		}, 500);
		slow.schedule({ prompt: "first", attachments: [], owner });
		const first = slow.flush();
		slow.schedule({ prompt: "second", attachments: [], owner });
		const second = slow.flush();
		await Promise.resolve();
		const forgetting = forgetSpawnDraftFor({ hostId: "machine-a" });
		finish();
		await Promise.all([first, second, forgetting]);
		expect(writes).toEqual(["first"]);
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(false);
	});

	// Read, checked and removed in one turn of the queue: another machine's save
	// that commits between the read and the removal is not deleted.
	it("does not delete another owner's draft saved while the forgotten one is checked", async () => {
		await saveSpawnDraft({ prompt: "For a", attachments: [], owner: { hostId: "machine-a", accountId: null } });
		const saver = createSpawnDraftSaver(undefined, 500);
		const forgetting = forgetSpawnDraftFor({ hostId: "machine-a" });
		await Promise.resolve();
		saver.schedule({ prompt: "For b", attachments: [], owner: { hostId: "machine-b", accountId: null } });
		const saving = saver.flush();
		await Promise.all([forgetting, saving]);
		store.hosts = ["machine-b"];
		expect((await loadSpawnDraft())?.prompt).toBe("For b");
	});

	it("still saves another owner's draft when one is forgotten", async () => {
		const saver = createSpawnDraftSaver(undefined, 500);
		saver.schedule({ prompt: "For b", attachments: [], owner: { hostId: "machine-b", accountId: null } });
		await forgetSpawnDraftFor({ hostId: "machine-a" });
		expect(await saver.flush()).toBe(true);
		store.hosts = ["machine-b"];
		expect((await loadSpawnDraft())?.prompt).toBe("For b");
	});

	it("never saves after a Start cancelled it, even a save already queued", async () => {
		let finish = () => {};
		const writes: string[] = [];
		const saver = createSpawnDraftSaver(async (draft) => {
			if (draft.prompt === "first") await new Promise<void>((resolve) => { finish = resolve; });
			writes.push(draft.prompt);
		}, 500);
		saver.schedule({ prompt: "first", attachments: [] });
		const first = saver.flush();
		saver.schedule({ prompt: "second", attachments: [] });
		const second = saver.flush();
		await Promise.resolve();
		const cancelled = saver.cancel();
		finish();
		await Promise.all([first, second, cancelled]);
		expect(writes).toEqual(["first"]);
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

	// The server refused the Start (a 4xx it answered): no task started, so the
	// next Start may go under a new id with the model shown then. Anything that
	// may have started one (a lost answer, a timeout, a conflict, a rate limit)
	// keeps the id, so the daemon answers with that task.
	it("tells a Start the server refused from one that may have started a task", () => {
		// Shaped as the daemon client's ApiError: a status the daemon answered.
		const apiError = (status: number) => Object.assign(new Error("daemon said no"), { status });
		expect(startRefused(apiError(400))).toBe(true);
		expect(startRefused(apiError(422))).toBe(true);
		expect(startRefused(Object.assign(new Error("Cloud said no"), { cause: new CloudError("bad model", { status: 400, code: "invalid_request" }) }))).toBe(true);
		for (const status of [408, 409, 429, 500, 502]) expect(startRefused(apiError(status))).toBe(false);
		expect(startRefused(new CloudError("unreachable", { status: 0 }))).toBe(false);
		expect(startRefused(new Error("network request failed"))).toBe(false);
		expect(startRefused(undefined)).toBe(false);
	});
});
