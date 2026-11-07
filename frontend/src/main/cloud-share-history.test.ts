// @vitest-environment node
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import type { CloudShareHistoryInspection, CloudSharePublishResult } from "../shared/cloud-share";
import { MAX_HISTORY_VERSIONS, olderVersions, resolveShareFile, resolveShareVersion } from "./cloud-share";
import { registerCloudShareIpc } from "./cloud-share-ipc";
import { ShareLinkRecords } from "./cloud-share-links";

// Built at runtime so the repository's secret scanner does not flag the fixture.
const FAKE_AWS_KEY = ["AKIA", "ABCDEFGHIJKLMNOP"].join("");
const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
const ORG_ID = "a1a1a1a1-0000-4000-8000-000000000001";
const CLOUD_SESSION_ID = "a1a1a1a1-0000-4000-8000-0000000000a1";
const CLOUD_SESSION_PATH = `/api/cloud/v1/orgs/${ORG_ID}/sessions/${CLOUD_SESSION_ID}`;
const HISTORY_PATH = `/api/cloud/v1/me/artifact-shares/${SHARE_ID}/history`;
const CURRENT = "the latest report";
const OLDER = ["first draft", "second draft"];

const sha = (text: string) => createHash("sha256").update(text).digest("hex");

let dataDir: string;

async function keepBlob(text: string) {
	const sum = sha(text);
	const dir = path.join(dataDir, "artifacts", "sess-1", ".versions", "blobs", sum.slice(0, 2));
	await fs.mkdir(dir, { recursive: true });
	await fs.writeFile(path.join(dir, sum), text);
}

beforeEach(async () => {
	dataDir = await fs.mkdtemp(path.join(os.tmpdir(), "ao-share-history-"));
	await fs.mkdir(path.join(dataDir, "artifacts", "sess-1", "docs"), { recursive: true });
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), CURRENT);
	for (const text of [...OLDER, CURRENT]) await keepBlob(text);
});

afterEach(async () => {
	await fs.rm(dataDir, { recursive: true, force: true });
});

/** The versions the daemon keeps, newest first; the first is the file as it is. */
function keptVersions(texts = [CURRENT, ...[...OLDER].reverse()]) {
	return texts.map((text, index) => ({
		version: texts.length - index, fileFingerprint: sha(text), size: text.length, createdAt: `2026-10-0${texts.length - index}T00:00:00Z`, source: "agent",
	}));
}

function setup(options: { kept?: ReturnType<typeof keptVersions>; failFinalizeAfter?: number; daemonPort?: number | null; failSync?: boolean; cloudContent?: Record<number, string>; turnedOffMidway?: boolean } = {}) {
	const handlers = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
	const requests: string[] = [];
	const historyBodies: unknown[] = [];
	const uploads: Array<Record<string, unknown>> = [];
	const stored = new Set<number>();
	let pendingVersion: number | undefined;
	let finalized = 0;
	const sender = { id: 7, isDestroyed: () => false, send: vi.fn() };
	const shareLink = (includeHistory: boolean) => ({ id: SHARE_ID, slug: "abc", contentUrl: "https://abc.share.test/", includeHistory });
	const daemonFetch = vi.fn(async (url: string, init: RequestInit) => {
		requests.push(`daemon ${init.method} ${new URL(url).pathname}`);
		if (options.failSync) return new Response("", { status: 500 });
		return new Response(JSON.stringify({ versions: options.kept ?? keptVersions() }));
	});
	registerCloudShareIpc({
		ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => handlers.set(channel, handler)) },
		daemonDataDir: () => dataDir,
		snapshotDir: () => path.join(dataDir, "snapshots"),
		getToken: async () => "token-1",
		records: new ShareLinkRecords(path.join(dataDir, "share-links.json")),
		daemonPort: () => (options.daemonPort === null ? undefined : options.daemonPort ?? 4000),
		daemonFetch: daemonFetch as unknown as typeof fetch,
		fetch: vi.fn(async (url: string, init: RequestInit) => {
			const { pathname } = new URL(url);
			const method = init.method ?? "GET";
			requests.push(`${method} ${pathname}`);
			const body = typeof init.body === "string" ? JSON.parse(init.body) as Record<string, unknown> : undefined;
			const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
			if (pathname === HISTORY_PATH && method === "PUT") {
				historyBodies.push(body);
				if (body?.revision && options.turnedOffMidway) return json({ code: "artifact_share_history_changed", message: "History was turned off." }, 409);
				const listed = (body?.versions as Array<{ version: number }>).map((entry) => entry.version);
				const missing = listed.filter((version) => !stored.has(version)).sort((a, b) => a - b);
				return json({ share: shareLink(missing.length === 0), missing, revision: 7 });
			}
			if (pathname === HISTORY_PATH && method === "DELETE") return json({ share: shareLink(false), missing: [], revision: 8 });
			if (pathname.endsWith("/uploads")) {
				uploads.push(body ?? {});
				pendingVersion = body?.historyVersion as number | undefined;
				return json({ uploadToken: "tok", partSize: 1 << 20, partCount: 1 }, 201);
			}
			if (pathname.endsWith("/uploads/parts")) return json({ parts: [{ partNumber: 1, method: "PUT", url: "https://s3.test/p", headers: {}, size: 20 }] });
			if (pathname === "/p") return new Response("", { headers: { etag: '"e1"' } });
			if (pathname.endsWith("/uploads/abort")) return new Response(null, { status: 204 });
			if (pathname.endsWith("/finalize")) {
				finalized += 1;
				if (options.failFinalizeAfter !== undefined && finalized > options.failFinalizeAfter) return json({ code: "artifact_store_unavailable", message: "The bucket is down." }, 502);
				if (pendingVersion) stored.add(pendingVersion);
				return json({ share: shareLink(false), replaced: false }, 201);
			}
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts`) {
				return json({ artifacts: [{ path: "plan.md", name: "plan.md", size: 4, sha256: sha("now") }] });
			}
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/access`) return json({ filesPrefix: "/api/cloud/v1/artifact-files/tok/" });
			if (pathname === "/api/cloud/v1/artifact-files/tok/plan.md") return new Response("now", { status: 206 });
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/versions`) return json({ versions: keptVersions(["now", "then", "before"]) });
			const version = pathname.match(/artifacts\/versions\/(\d+)$/);
			if (version) return json({ content: options.cloudContent?.[Number(version[1])] ?? "plain" });
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/share`) return json({ share: shareLink(false), replaced: false }, 201);
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/share/history`) {
				historyBodies.push(body);
				return json({ share: shareLink(true), missing: [], revision: 7 });
			}
			return new Response("", { status: 404 });
		}),
	});
	const invoke = (channel: string, value?: unknown) => handlers.get(channel)!({ sender }, value);
	const file = { baseUrl: "https://cp.test", sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
	const cloudFile = { source: "cloud", baseUrl: "https://cp.test", orgId: ORG_ID, sessionId: CLOUD_SESSION_ID, path: "plan.md", projectId: "" };
	const inspectHistory = (target: unknown = file) => invoke("cloudShare:inspectHistory", target) as Promise<CloudShareHistoryInspection>;
	const publish = async (target: Record<string, unknown>, history?: unknown) => {
		const { fingerprint } = (await invoke("cloudShare:inspect", target)) as { fingerprint: string };
		return invoke("cloudShare:publish", { ...target, fingerprint, history }) as Promise<CloudSharePublishResult>;
	};
	return { invoke, handlers, inspectHistory, publish, file, cloudFile, requests, historyBodies, uploads, sender, daemonFetch };
}

describe("olderVersions", () => {
	it("leaves out the file as it is now, and only that", () => {
		const kept = keptVersions();
		expect(olderVersions(kept, sha(CURRENT)).map((version) => version.version)).toEqual([2, 1]);
		// The file moved on from what the daemon has kept: all of it is older.
		expect(olderVersions(kept, sha("unkept")).map((version) => version.version)).toEqual([3, 2, 1]);
		expect(olderVersions([], sha(CURRENT))).toEqual([]);
	});

	it("keeps the newest a link can show", () => {
		const many = keptVersions(Array.from({ length: MAX_HISTORY_VERSIONS + 5 }, (_, index) => `version ${index}`));
		const older = olderVersions(many, "none");
		expect(older).toHaveLength(MAX_HISTORY_VERSIONS);
		expect(older[0]?.version).toBe(MAX_HISTORY_VERSIONS + 5);
	});
});

describe("resolveShareVersion", () => {
	it("finds a kept version, which publishing a file by path still refuses", async () => {
		await expect(resolveShareVersion(dataDir, "sess-1", sha(OLDER[0]!), "report.html")).resolves.toMatchObject({ name: "report.html", size: OLDER[0]!.length });
		const blob = `.versions/blobs/${sha(OLDER[0]!).slice(0, 2)}/${sha(OLDER[0]!)}`;
		await expect(resolveShareFile(dataDir, "sess-1", blob)).rejects.toThrow("Version history cannot be shared");
	});

	it("refuses anything but a blob named by a hash", async () => {
		await expect(resolveShareVersion(dataDir, "sess-1", "../../report.html", "x")).rejects.toThrow("Not a kept version");
		await expect(resolveShareVersion(dataDir, "..", sha(OLDER[0]!), "x")).rejects.toThrow("Not a session's artifact");
		await expect(resolveShareVersion(dataDir, "sess-1", sha("never kept"), "x")).rejects.toThrow("no longer available");
	});

	it("refuses a blob that links out of the folder", async () => {
		const sum = sha("outside");
		const dir = path.join(dataDir, "artifacts", "sess-1", ".versions", "blobs", sum.slice(0, 2));
		await fs.mkdir(dir, { recursive: true });
		await fs.writeFile(path.join(dataDir, "secret"), "outside");
		await fs.symlink(path.join(dataDir, "secret"), path.join(dir, sum));
		await expect(resolveShareVersion(dataDir, "sess-1", sum, "x")).rejects.toThrow("Not a kept version");
	});
});

describe("cloudShare history IPC", () => {
	it("lists a local file's older versions with no upload", async () => {
		const { inspectHistory, requests, daemonFetch } = setup();
		const inspection = await inspectHistory();
		expect(inspection.versions.map((version) => version.version)).toEqual([2, 1]);
		expect(inspection.versions[1]).toEqual({ version: 1, sha256: sha(OLDER[0]!), size: OLDER[0]!.length, createdAt: "2026-10-01T00:00:00Z" });
		expect(inspection.secretFindings).toEqual([]);
		// The daemon records the file as it is now first, so "older" is exact.
		expect(requests).toEqual(["daemon POST /api/v1/sessions/sess-1/artifact-versions/sync"]);
		expect(JSON.parse((daemonFetch.mock.calls[0]![1] as RequestInit).body as string)).toEqual({ path: "docs/report.html" });
	});

	it("finds a credential a later version removed", async () => {
		const leaked = `password draft ${FAKE_AWS_KEY}`;
		await keepBlob(leaked);
		const { inspectHistory } = setup({ kept: keptVersions([CURRENT, leaked]) });
		await expect(inspectHistory()).resolves.toMatchObject({ secretFindings: ["AWS access key"] });
	});

	it("says when the daemon cannot list versions", async () => {
		await expect(setup({ daemonPort: null }).inspectHistory()).rejects.toThrow("not ready");
		await expect(setup({ failSync: true }).inspectHistory()).rejects.toThrow("failed (500)");
	});

	it("publishes the file, then its older versions, and shows them last", async () => {
		const { inspectHistory, publish, file, requests, uploads, historyBodies } = setup();
		const { versions } = await inspectHistory();
		const result = await publish(file, versions);
		expect(result).toMatchObject({ status: "published", share: { id: SHARE_ID, includeHistory: true } });
		expect(result).not.toHaveProperty("historyError");

		expect(uploads.map((upload) => upload.historyVersion)).toEqual([undefined, 2, 1]);
		expect(uploads[1]).toMatchObject({ shareId: SHARE_ID, name: "report.html", sha256: sha(OLDER[1]!), historyRevision: 7, createdAt: "2026-10-02T00:00:00Z" });
		const puts = requests.map((request, index) => (request === `PUT ${HISTORY_PATH}` ? index : -1)).filter((index) => index >= 0);
		const finalizes = requests.map((request, index) => (request.endsWith("/finalize") ? index : -1)).filter((index) => index >= 0);
		expect(puts).toHaveLength(2);
		expect(finalizes.every((index) => index < puts[1]!)).toBe(true);
		// The second call finishes the update the first started, and no other.
		expect(historyBodies[0]).not.toHaveProperty("revision");
		expect(historyBodies[1]).toEqual({ ...(historyBodies[0] as object), revision: 7 });
		await expect(fs.readdir(path.join(dataDir, "snapshots"))).resolves.toEqual([]);
	});

	it("publishes with no history request when the choice is off", async () => {
		const { publish, file, requests } = setup();
		await expect(publish(file)).resolves.toMatchObject({ status: "published" });
		expect(requests.filter((request) => request.includes("/history") || request.startsWith("daemon"))).toEqual([]);
	});

	it("leaves a live link showing only its latest when a version fails", async () => {
		const { inspectHistory, publish, file, requests } = setup({ failFinalizeAfter: 2 });
		const { versions } = await inspectHistory();
		const result = await publish(file, versions);
		expect(result).toMatchObject({ status: "published", share: { id: SHARE_ID }, historyError: "The bucket is down." });
		expect(result).not.toMatchObject({ share: { includeHistory: true } });
		expect(requests.filter((request) => request === `PUT ${HISTORY_PATH}`)).toHaveLength(1);
		await expect(fs.readdir(path.join(dataDir, "snapshots"))).resolves.toEqual([]);
	});

	it("says so when history was turned off while its versions were uploading", async () => {
		const { inspectHistory, publish, file } = setup({ turnedOffMidway: true });
		const { versions } = await inspectHistory();
		const result = await publish(file, versions);
		expect(result).toMatchObject({ status: "published", historyError: "History was turned off." });
		expect(result).not.toMatchObject({ share: { includeHistory: true } });
	});

	it("does not send a version whose blob is not what was shown", async () => {
		const sum = sha(OLDER[0]!);
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", ".versions", "blobs", sum.slice(0, 2), sum), "swapped");
		const { inspectHistory, publish, file, uploads } = setup({ kept: keptVersions([CURRENT, OLDER[0]!]) });
		const { versions } = await inspectHistory();
		await expect(publish(file, versions)).resolves.toMatchObject({ status: "published", historyError: "A version of this file changed while it was being shared." });
		expect(uploads.filter((upload) => upload.historyVersion)).toEqual([]);
	});

	it("shares a live link's history without touching what it serves", async () => {
		const { inspectHistory, invoke, file, uploads } = setup();
		const { versions } = await inspectHistory();
		await expect(invoke("cloudShare:shareHistory", { ...file, shareId: SHARE_ID, history: versions })).resolves.toMatchObject({ status: "published", share: { includeHistory: true } });
		expect(uploads.every((upload) => upload.historyVersion)).toBe(true);
		await expect(Promise.resolve().then(() => invoke("cloudShare:shareHistory", file))).rejects.toThrow("Not a file to share");
	});

	it("turns a link's history off", async () => {
		const { invoke, requests } = setup();
		await expect(invoke("cloudShare:unshareHistory", { baseUrl: "https://cp.test", shareId: SHARE_ID })).resolves.toMatchObject({ id: SHARE_ID, includeHistory: false });
		expect(requests).toEqual([`DELETE ${HISTORY_PATH}`]);
	});

	it("refuses a malformed list of versions", async () => {
		const { invoke, file } = setup();
		await expect(Promise.resolve().then(() => invoke("cloudShare:publish", { ...file, fingerprint: "x", history: [{ version: "1" }] }))).rejects.toThrow("Not a file to share");
	});

	it("answers only the window's main frame", async () => {
		const { handlers, file } = setup();
		const subframe = { sender: { id: 7 }, senderFrame: { parent: {} } };
		for (const channel of ["cloudShare:inspectHistory", "cloudShare:shareHistory", "cloudShare:unshareHistory"]) {
			await expect(Promise.resolve().then(() => handlers.get(channel)!(subframe, file))).rejects.toThrow("only available from the app window");
		}
	});

	it("checks a cloud file's older versions and copies them on the control plane", async () => {
		const { inspectHistory, publish, cloudFile, historyBodies, uploads } = setup({ cloudContent: { 1: `notes ${FAKE_AWS_KEY}` } });
		const inspection = await inspectHistory(cloudFile);
		expect(inspection.versions.map((version) => version.version)).toEqual([2, 1]);
		expect(inspection.secretFindings).toEqual(["AWS access key"]);
		const result = await publish(cloudFile, inspection.versions);
		expect(result).toMatchObject({ status: "published", share: { includeHistory: true } });
		expect(historyBodies).toEqual([{
			path: "plan.md",
			shareId: SHARE_ID,
			versions: [{ version: 2, sha256: sha("then") }, { version: 1, sha256: sha("before") }],
		}]);
		expect(uploads).toEqual([]);
	});
});
