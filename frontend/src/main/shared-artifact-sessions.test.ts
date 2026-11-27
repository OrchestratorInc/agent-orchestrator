import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { hasCopyMarker, SHARED_COPY_MARKER, SharedArtifactSessions, sharedCopyCheck } from "./shared-artifact-sessions";

const record = { sessionId: "ao-7", dir: "shared/roadmap", entry: "shared/roadmap/roadmap.html", updatedAt: "2026-11-01T00:00:00Z", fingerprint: "f1" };
const key = { baseUrl: "https://cp.example", userId: "u1", linkId: "link-1" };
const stored = JSON.stringify([key.baseUrl, key.userId, key.linkId]);

describe("SharedArtifactSessions", () => {
	let dir: string;
	let file: string;
	beforeEach(async () => {
		dir = await fs.mkdtemp(path.join(os.tmpdir(), "shared-sessions-"));
		file = path.join(dir, "shared-artifact-sessions.json");
	});
	afterEach(async () => {
		await fs.rm(dir, { recursive: true, force: true });
	});

	const backups = async () => (await fs.readdir(dir)).filter((name) => name.endsWith(".bak"));

	it("remembers a link's session across restarts, and forgets it", async () => {
		await new SharedArtifactSessions(file).set(key, record);
		const reopened = new SharedArtifactSessions(file);
		expect(await reopened.get(key)).toEqual(record);
		await reopened.remove(key);
		expect(await new SharedArtifactSessions(file).get(key)).toBeNull();
	});

	it("keeps each account's links apart", async () => {
		const store = new SharedArtifactSessions(file);
		await store.set(key, record);
		await store.set({ ...key, userId: "u2" }, { ...record, sessionId: "ao-8" });
		expect(await store.get({ ...key, userId: "u3" })).toBeNull();
		expect(await store.list({ baseUrl: key.baseUrl, userId: "u1" })).toEqual([{ ...record, linkId: "link-1" }]);
	});

	it("keeps every copy marked, after the link moves to a new session or is forgotten", async () => {
		const store = new SharedArtifactSessions(file);
		await store.set(key, record);
		await store.set(key, { ...record, sessionId: "ao-9" });
		await store.remove(key);
		const reopened = new SharedArtifactSessions(file);
		expect(await reopened.isCopy("ao-7", "shared/roadmap/img/a.png")).toBe(true);
		expect(await reopened.isCopy("ao-9", "shared/roadmap/roadmap.html")).toBe(true);
		expect(await reopened.isCopy("ao-7", "notes.md")).toBe(false);
		expect(await reopened.copies()).toHaveLength(2);
	});

	it("reads the first version's file, a plain map of records", async () => {
		await fs.writeFile(file, JSON.stringify({ [stored]: record }));
		const store = new SharedArtifactSessions(file);
		expect(await store.get(key)).toEqual(record);
		expect(await store.isCopy("ao-7", "shared/roadmap/roadmap.html")).toBe(true);
	});

	it("reads a file an editor saved with a byte-order mark", async () => {
		await fs.writeFile(file, `﻿${JSON.stringify({ version: 2, links: { [stored]: record }, copies: [] })}`);
		expect(await new SharedArtifactSessions(file).get(key)).toEqual(record);
		expect(await backups()).toEqual([]);
	});

	it("keeps a corrupted file, or one that is not an object, as its own .bak", async () => {
		await fs.writeFile(file, "{not json");
		await new SharedArtifactSessions(file).set(key, record);
		await fs.writeFile(file, "[]");
		await new Promise((resolve) => setTimeout(resolve, 5));
		const store = new SharedArtifactSessions(file);
		expect(await store.get(key)).toBeNull();
		const kept = await backups();
		expect(kept).toHaveLength(2);
		expect((await Promise.all(kept.map((name) => fs.readFile(path.join(dir, name), "utf8")))).sort()).toEqual(["[]", "{not json"]);
	});

	it("leaves a newer app's file as it is", async () => {
		const newer = JSON.stringify({ version: 3, links: {}, copies: [], more: true });
		await fs.writeFile(file, newer);
		const store = new SharedArtifactSessions(file);
		await expect(store.set(key, record)).rejects.toThrow();
		expect(await fs.readFile(file, "utf8")).toBe(newer);
	});

	it("keeps a file with misshapen parts as .bak before saving over it", async () => {
		await fs.writeFile(file, JSON.stringify({ version: 2, links: { [stored]: record }, copies: "oops" }));
		const store = new SharedArtifactSessions(file);
		expect(await store.get(key)).toEqual(record);
		expect(await backups()).toHaveLength(1);
	});

	it("finds the daemon's marker above a copied file", async () => {
		const root = path.join(dir, "artifacts", "ao-7");
		await fs.mkdir(path.join(root, "shared", "r", "img"), { recursive: true });
		await fs.writeFile(path.join(root, "shared", "r", SHARED_COPY_MARKER), "");
		expect(await hasCopyMarker(root, "shared/r/img/a.png")).toBe(true);
		expect(await hasCopyMarker(root, "shared/r/r.html")).toBe(true);
		expect(await hasCopyMarker(root, "notes.md")).toBe(false);
	});

	it("falls back to the daemon's marker when the file can't be read, and never blocks other publishing", async () => {
		const root = path.join(dir, "artifacts", "ao-7");
		await fs.mkdir(path.join(root, "shared", "r"), { recursive: true });
		await fs.writeFile(path.join(root, "shared", "r", SHARED_COPY_MARKER), "");
		const failing = { isCopy: async () => { throw new Error("EBUSY"); } };
		const check = sharedCopyCheck(failing, () => root);
		expect(await check("ao-7", "shared/r/r.html")).toBe(true);
		expect(await check("ao-7", "notes.md")).toBe(false);
		expect(await check("ao-7", "../ao-8/shared/r/r.html")).toBe(false);
	});

	it("still knows a copied file moved out of its copy's folder", async () => {
		const root = path.join(dir, "artifacts", "ao-7");
		await fs.mkdir(path.join(root, "shared", "r"), { recursive: true });
		const sum = createHash("sha256").update("chart bytes").digest("hex");
		await fs.writeFile(path.join(root, "shared", "r", SHARED_COPY_MARKER), `${sum}\n`);
		await fs.writeFile(path.join(root, "report.png"), "chart bytes");
		await fs.writeFile(path.join(root, "mine.png"), "my own work");
		const check = sharedCopyCheck({ isCopy: async () => false }, () => root);
		expect(await check("ao-7", "report.png")).toBe(true);
		expect(await check("ao-7", "mine.png")).toBe(false);
	});

	it("never takes a file it cannot read for an empty one", async () => {
		await new SharedArtifactSessions(file).set(key, record);
		// A folder where the file should be fails to read with something other than ENOENT.
		const blocked = path.join(dir, "blocked.json");
		await fs.mkdir(blocked);
		const store = new SharedArtifactSessions(blocked);
		await expect(store.get(key)).rejects.toThrow();
		await expect(store.set({ ...key, linkId: "link-2" }, record)).rejects.toThrow();
		expect(await new SharedArtifactSessions(file).get(key)).toEqual(record);
	});

	it("refuses a record or key it cannot trust", async () => {
		const store = new SharedArtifactSessions(file);
		await expect(store.set(key, { ...record, sessionId: 7 } as never)).rejects.toThrow();
		await expect(store.set(key, { ...record, dir: "notes", entry: "notes/a.md" })).rejects.toThrow();
		await expect(store.set(key, { ...record, dir: "shared/../x", entry: "shared/../x/a.md" })).rejects.toThrow();
		expect(await store.get({ ...key, linkId: "" })).toBeNull();
	});
});
