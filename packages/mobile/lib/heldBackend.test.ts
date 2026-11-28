import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./config", () => ({}));

import type { ArtifactsBackend } from "./artifactsBackend";
import { UnreachableError } from "./connectionError";
import type { FilesBackend } from "./files/backend";
import { createLiveSlot, HELD_WAIT_MS, holdArtifactsBackend, holdFilesBackend } from "./heldBackend";

/** A Files backend for one connection, recording which connection served each call. */
function filesFor(connection: string, calls: string[]): FilesBackend {
	const backend: FilesBackend = {
		key: connection,
		cloud: false,
		manifest: async (sessionId) => { calls.push(`${connection}:manifest:${sessionId}`); return { sessionId } as never; },
		file: async (_sessionId, path) => { calls.push(`${connection}:file:${path}`); return { path } as never; },
		tree: async () => ({ path: "", entries: [], truncated: false }),
		search: async () => ({ results: [], truncated: false }),
		imageSource: async () => undefined,
		sendFeedback: async () => 0,
		git: {
			branches: async () => ({ branches: [], supported: true, truncated: false }),
			history: async () => ({ commits: [], commitsTruncated: false, view: { kind: "checked_out", rangeMode: "since_base" } }),
			rangeFiles: async () => ({ files: [], summary: { files: 0, additions: 0, deletions: 0 }, truncated: false }),
			at: (view) => ({ ...backend, view, file: async (_sessionId, path) => { calls.push(`${connection}:file@${view?.branch}:${path}`); return { path } as never; } }),
		},
	};
	return backend;
}

afterEach(() => vi.useRealTimers());

describe("a held Files backend", () => {
	it("serves every call through the live connection, the one it has now", async () => {
		const calls: string[] = [];
		const slot = createLiveSlot<FilesBackend>();
		slot.set(filesFor("lan", calls));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		await held.file("s", "a.ts");
		slot.set(filesFor("tunnel", calls));
		await held.file("s", "b.ts");
		expect(calls).toEqual(["lan:file:a.ts", "tunnel:file:b.ts"]);
		expect(held.key).toBe("machine-a");
	});

	it("waits while the connection is away and never uses the one that went", async () => {
		const calls: string[] = [];
		const slot = createLiveSlot<FilesBackend>();
		slot.set(filesFor("lan", calls));
		const held = holdFilesBackend(slot, slot.get()!, "machine-a");
		slot.set(null); // app switch
		const pending = held.manifest("s");
		await Promise.resolve();
		expect(calls).toEqual([]);
		slot.set(filesFor("tunnel", calls));
		await pending;
		expect(calls).toEqual(["tunnel:manifest:s"]);
	});

	it("gives up as unreachable when the connection does not come back", async () => {
		vi.useFakeTimers();
		const slot = createLiveSlot<FilesBackend>();
		const held = holdFilesBackend(slot, filesFor("lan", []), "machine-a");
		const pending = held.manifest("s").catch((error: unknown) => error);
		await vi.advanceTimersByTimeAsync(HELD_WAIT_MS);
		expect(await pending).toBeInstanceOf(UnreachableError);
	});

	it("stops waiting when the caller cancels", async () => {
		const slot = createLiveSlot<FilesBackend>();
		const held = holdFilesBackend(slot, filesFor("lan", []), "machine-a");
		const cancel = new AbortController();
		const pending = held.manifest("s", cancel.signal).catch((error: Error) => error.name);
		cancel.abort();
		expect(await pending).toBe("AbortError");
	});

	// The screen that asked closed while the call waited for the connection: once
	// the connection is back, nothing goes out for it.
	it("never sends a file, folder, search, image or feedback call cancelled while it waited", async () => {
		const live = vi.fn();
		const lan: FilesBackend = {
			...filesFor("lan", []),
			file: async () => { live("file"); return {} as never; },
			tree: async () => { live("tree"); return { path: "", entries: [], truncated: false }; },
			search: async () => { live("search"); return { results: [], truncated: false }; },
			imageSource: async () => { live("imageSource"); return undefined; },
			sendFeedback: async () => { live("sendFeedback"); return 0; },
		};
		const slot = createLiveSlot<FilesBackend>();
		const held = holdFilesBackend(slot, lan, "machine-a");
		const cancel = new AbortController();
		const calls = [
			held.file("s", "a.ts", {}, cancel.signal),
			held.tree("s", "", cancel.signal),
			held.search("s", "app", cancel.signal),
			held.imageSource("s", "a.png", "after", "v1", {}, cancel.signal),
			held.sendFeedback("s", [], undefined, cancel.signal),
			held.git!.at({ branch: "main" }).file("s", "a.ts", {}, cancel.signal),
		].map((call) => call.then(() => "sent", (error: Error) => error.name));
		cancel.abort();
		expect(await Promise.all(calls)).toEqual(Array(6).fill("AbortError"));
		slot.set(lan);
		await Promise.resolve();
		expect(live).not.toHaveBeenCalled();
	});

	it("hands the caller's signal to the live call, so the request itself can be cancelled", async () => {
		const signals: Array<AbortSignal | undefined> = [];
		const lan: FilesBackend = {
			...filesFor("lan", []),
			file: async (_sessionId, _path, _read, signal) => { signals.push(signal); return {} as never; },
			tree: async (_sessionId, _dir, signal) => { signals.push(signal); return { path: "", entries: [], truncated: false }; },
			search: async (_sessionId, _query, signal) => { signals.push(signal); return { results: [], truncated: false }; },
			imageSource: async (_sessionId, _path, _side, _version, _read, signal) => { signals.push(signal); return undefined; },
			sendFeedback: async (_sessionId, _comments, _onSent, signal) => { signals.push(signal); return 0; },
		};
		const slot = createLiveSlot<FilesBackend>();
		slot.set(lan);
		const held = holdFilesBackend(slot, lan, "machine-a");
		const { signal } = new AbortController();
		await held.file("s", "a.ts", {}, signal);
		await held.tree("s", "", signal);
		await held.search("s", "app", signal);
		await held.imageSource("s", "a.png", "after", "v1", {}, signal);
		await held.sendFeedback("s", [], undefined, signal);
		expect(signals).toEqual(Array(5).fill(signal));
	});

	it("reads another branch through the live connection too", async () => {
		const calls: string[] = [];
		const slot = createLiveSlot<FilesBackend>();
		slot.set(filesFor("lan", calls));
		const branch = holdFilesBackend(slot, slot.get()!, "machine-a").git!.at({ branch: "main" });
		slot.set(filesFor("tunnel", calls));
		await branch.file("s", "a.ts");
		expect(calls).toEqual(["tunnel:file@main:a.ts"]);
		expect(branch.view).toEqual({ branch: "main" });
		expect(branch.key).toBe('machine-a|{"branch":"main"}');
	});
});

describe("a held artifacts backend", () => {
	const artifactsFor = (host: string) => ({
		key: "machine-a",
		cloud: false,
		canChange: true,
		webSource: (sessionId: string, path: string) => ({ uri: `http://${host}/${sessionId}/${path}`, headers: { Authorization: "Bearer t" } }),
		imageSource: () => undefined,
		streamSource: () => undefined,
		markdownImageSource: () => undefined,
	}) as unknown as ArtifactsBackend;

	it("keeps a page's source as the same object, and answers nothing new while the connection is away", () => {
		const slot = createLiveSlot<ArtifactsBackend>();
		slot.set(artifactsFor("lan"));
		const held = holdArtifactsBackend(slot, slot.get()!, "machine-a");
		const shown = held.webSource("s", "page.html");
		slot.set(artifactsFor("lan"));
		expect(held.webSource("s", "page.html")).toBe(shown);
		slot.set(null);
		// The page already showing keeps its source; a file never asked for gets none from the old address.
		expect(held.webSource("s", "page.html")).toBe(shown);
		expect(held.webSource("s", "other.html")).toBeUndefined();
		slot.set(artifactsFor("tunnel"));
		expect(held.webSource("s", "page.html")?.uri).toBe("http://tunnel/s/page.html");
	});
});
