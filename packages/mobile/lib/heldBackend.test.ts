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
