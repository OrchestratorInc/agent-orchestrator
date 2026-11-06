import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { registerSessionShareUrlsIpc, type SessionShareUrlCrypto, SessionShareUrls } from "./session-share-urls";

const CP = "https://api.example.test";
const A = "0c927f48-7214-4fb1-833a-7c1af5b7bf80";
const B = "1d038059-8325-4ac2-944b-8d2b06c8cf91";
const URL_A = "https://orchestrator.inc/s/k7Hq2x9PmB4wR5tY8uI0oP";

// Reversible but not plain text, like the keychain's encryption.
const crypto = (available = true): SessionShareUrlCrypto => ({
	available: () => available,
	encrypt: (text) => Buffer.from(text, "utf8").reverse(),
	decrypt: (value) => Buffer.from(value).reverse().toString("utf8"),
});

describe("SessionShareUrls", () => {
	let dir: string;
	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "ao-share-urls-"));
	});
	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});

	it("remembers a link's URL across restarts, encrypted and private", async () => {
		const file = path.join(dir, "session-share-links.bin");
		await new SessionShareUrls(file, crypto()).remember(`${CP}/`, A, URL_A);
		const raw = await readFile(file);
		expect(raw.toString("utf8")).not.toContain("orchestrator.inc");
		expect((await stat(file)).mode & 0o777).toBe(0o600);
		const reopened = new SessionShareUrls(file, crypto());
		expect(await reopened.recall(CP, [A, B])).toEqual({ [A]: URL_A });
		// Another control plane's link of the same id is another link.
		expect(await reopened.recall("https://staging.example.test", [A])).toEqual({});
	});

	it("forgets a revoked link", async () => {
		const file = path.join(dir, "session-share-links.bin");
		const store = new SessionShareUrls(file, crypto());
		await store.remember(CP, A, URL_A);
		await store.forget(CP, [A]);
		expect(await new SessionShareUrls(file, crypto()).recall(CP, [A])).toEqual({});
	});

	it("keeps URLs in memory only, never on disk, without protected storage", async () => {
		const file = path.join(dir, "session-share-links.bin");
		const store = new SessionShareUrls(file, crypto(false));
		await store.remember(CP, A, URL_A);
		expect(await store.recall(CP, [A])).toEqual({ [A]: URL_A });
		await expect(stat(file)).rejects.toThrow();
	});

	it("takes calls only from a window's main frame, and only share links", async () => {
		const handlers = new Map<string, (event: unknown, input: unknown) => Promise<unknown>>();
		const store = new SessionShareUrls(path.join(dir, "links.bin"), crypto());
		registerSessionShareUrlsIpc({ handle: vi.fn((channel, handler) => handlers.set(channel, handler)) } as never, store);
		const mainFrame = { senderFrame: { parent: null } };
		const subframe = { senderFrame: { parent: {} } };
		const remember = handlers.get("sessionShareUrls:remember")!;
		await expect(remember(subframe, { baseUrl: CP, linkId: A, url: URL_A })).rejects.toThrow();
		await expect(remember(mainFrame, { baseUrl: CP, linkId: "not-a-link", url: URL_A })).rejects.toThrow();
		await expect(remember(mainFrame, { baseUrl: CP, linkId: A, url: "javascript:alert(1)" })).rejects.toThrow();
		await remember(mainFrame, { baseUrl: CP, linkId: A, url: URL_A });
		expect(await handlers.get("sessionShareUrls:recall")!(mainFrame, { baseUrl: CP, linkIds: [A, "x"] })).toEqual({ [A]: URL_A });
	});
});
