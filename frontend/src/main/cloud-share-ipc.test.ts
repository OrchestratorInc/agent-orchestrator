// @vitest-environment node
import { openSync, closeSync, writeSync, promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import { CLOUD_SHARE_PROGRESS_CHANNEL } from "../shared/cloud-share";
import { registerCloudShareIpc } from "./cloud-share-ipc";
import { ShareLinkRecords, artifactRecordKey, shareRecordKey } from "./cloud-share-links";

// Built at runtime so the repository's secret scanner does not flag the fixture.
const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
const ORG_ID = "a1a1a1a1-0000-4000-8000-000000000001";
const CLOUD_SESSION_ID = "a1a1a1a1-0000-4000-8000-0000000000a1";
const CLOUD_SESSION_PATH = `/api/cloud/v1/orgs/${ORG_ID}/sessions/${CLOUD_SESSION_ID}`;
const CLOUD_SHA256 = "0".repeat(64);
const GRANT_ID = "6f1c3a1e-2b4d-4c5e-8f7a-9b0c1d2e3f40";

let dataDir: string;

beforeEach(async () => {
	dataDir = await fs.mkdtemp(path.join(os.tmpdir(), "ao-share-ipc-"));
	await fs.mkdir(path.join(dataDir, "artifacts", "sess-1", "docs"), { recursive: true });
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), "<h1>quarterly</h1>!!");
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "clip.mp4"), "video");
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "clip.mkv"), "video");
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "clip.ogv"), "video");
});

afterEach(async () => {
	await fs.rm(dataDir, { recursive: true, force: true });
});

const HOST_ID = "https://h-vm.useao.dev";
const HOST_PROXY = "http://127.0.0.1:9/proxy-token";

/** A remote host's artifact as its daemon serves it: bytes, and the modification time its tag names. */
type HostArtifact = { body: string; mtimeNs: bigint; tag?: boolean; shortBy?: number; served?: () => void; status?: number; resolvesTo?: string; sendsResolved?: false };

function setup(options: { token?: string | null; finalizeStatus?: number; finalizeCode?: string; shareStatus?: number; shareCode?: string; listStatus?: number; unreachable?: boolean; shares?: unknown[]; listGate?: Promise<void>; hostFiles?: Map<string, HostArtifact>; files?: boolean; refuseVideos?: boolean } = {}) {
	const handlers = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
	const requests: string[] = [];
	const bodies = new Map<string, unknown>();
	const records = new ShareLinkRecords(path.join(dataDir, "share-links.json"));
	const sent: unknown[] = [];
	const authorizations: Array<string | null> = [];
	const sender = { id: 7, isDestroyed: () => false, send: vi.fn((channel: string, value: unknown) => channel === CLOUD_SHARE_PROGRESS_CHANNEL && sent.push(value)) };
	const hostFiles = options.hostFiles ?? new Map<string, HostArtifact>();
	const hostRequests: string[] = [];
	registerCloudShareIpc({
		ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => handlers.set(channel, handler)) },
		daemonDataDir: () => dataDir,
		snapshotDir: () => path.join(dataDir, "snapshots"),
		getToken: async () => (options.token === undefined ? "token-1" : options.token),
		records,
		hostApiBase: (hostId) => (hostId === HOST_ID ? HOST_PROXY : undefined),
		// The host's daemon, behind its proxy: the artifact route serves exact bytes tagged "<size>-<mtime ns>".
		fetchHost: vi.fn(async (url: string, init: RequestInit) => {
			const parsed = new URL(url);
			hostRequests.push(`${init.method ?? "GET"} ${parsed.pathname}${parsed.search}`);
			const prefix = "/proxy-token/api/v1/sessions/";
			const match = parsed.pathname.startsWith(prefix) ? /^([^/]+)\/preview\/files\/(.+)$/.exec(parsed.pathname.slice(prefix.length)) : null;
			const requested = match?.[2]?.split("/").map(decodeURIComponent).join("/") ?? "";
			const artifact = match && parsed.searchParams.get("source") === "artifact" ? hostFiles.get(`${decodeURIComponent(match[1] ?? "")}/${requested}`) : undefined;
			// The real route is GET-only: a HEAD is refused before the file is looked up.
			if (init.method === "HEAD") return new Response('{"error":"method not allowed"}', { status: 405 });
			if (!artifact) return new Response("", { status: 404 });
			if (artifact.status) return new Response("", { status: artifact.status });
			const bytes = Buffer.from(artifact.body);
			const headers = new Headers(artifact.tag === false
				? { "content-length": String(bytes.length), "last-modified": new Date(Number(artifact.mtimeNs / 1_000_000n)).toUTCString() }
				: { etag: `"${bytes.length}-${artifact.mtimeNs}"` });
			// The path the file resolves to once its symlinks are followed, as the daemon names it.
			if (artifact.sendsResolved !== false) headers.set("x-ao-artifact-path", (artifact.resolvesTo ?? requested).split("/").map(encodeURIComponent).join("/"));
			artifact.served?.();
			return new Response(bytes.subarray(0, bytes.length - (artifact.shortBy ?? 0)), { headers });
		}),
		fetch: vi.fn(async (url: string, init: RequestInit) => {
			const { pathname } = new URL(url);
			requests.push(`${init.method ?? "GET"} ${pathname}`);
			if (typeof init.body === "string") bodies.set(pathname, JSON.parse(init.body));
			const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
			if (pathname.endsWith("/me/artifact-shares")) {
				if (options.unreachable) throw new TypeError("fetch failed");
				authorizations.push(new Headers(init.headers).get("authorization"));
				if (options.listStatus) return json({ code: "not_found" }, options.listStatus);
				// A list read the test holds back: it answers with the links as they were when requested.
				const shares = options.shares ?? [];
				if (options.listGate) await options.listGate;
				return json({ shares, limit: 3, used: 1 });
			}
			// A control plane that does not serve videos yet (its CDN is off) refuses them.
			const video = (name: string) => options.refuseVideos && /\.(mp4|webm)$/.test(name);
			if (pathname.endsWith("/uploads/files") && options.files) {
				const { path: served } = JSON.parse(String(init.body)) as { path: string };
				if (served === "") return json({ code: "invalid_request", message: "path" }, 400);
				if (video(served)) return json({ code: "artifact_share_type_unsupported", message: "Videos can't be included in a shared page yet." }, 415);
				return json({ uploadToken: `file:${served}`, partSize: 1024, partCount: 1 }, 201);
			}
			if (pathname.endsWith("/uploads/files/complete") && options.files) {
				const { uploadToken } = JSON.parse(String(init.body)) as { uploadToken: string };
				return json({ fileToken: `ft:${uploadToken.slice("file:".length)}`, path: uploadToken.slice("file:".length), expiresAt: "" });
			}
			if (pathname.endsWith("/uploads")) {
				const { name } = JSON.parse(String(init.body)) as { name: string };
				if (video(name)) return json({ code: "artifact_share_type_unsupported", message: "Sharing videos is coming soon." }, 415);
				return json({ uploadToken: "tok", partSize: 1024, partCount: 1 }, 201);
			}
			if (pathname.endsWith("/uploads/parts")) return json({ parts: [{ partNumber: 1, method: "PUT", url: "https://s3.test/p", headers: {}, size: 20 }] });
			if (pathname === "/p") return new Response("", { headers: { etag: '"e1"' } });
			if (pathname.endsWith("/finalize")) {
				if (options.finalizeStatus) return json({ code: options.finalizeCode, message: "Free plan: up to 3 active links. Upgrade to share more." }, options.finalizeStatus);
				return json({ share: { id: SHARE_ID, slug: "abc", contentUrl: "https://abc.share.test/" }, replaced: false }, 201);
			}
			if (pathname.endsWith("/uploads/abort")) return new Response(null, { status: 204 });
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts`) {
				return json({ artifacts: [{ path: "reports/q3 plan.md", name: "q3 plan.md", kind: "markdown", size: 4096, sha256: "0".repeat(64) }] });
			}
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/share`) {
				if (options.shareStatus) return json({ code: options.shareCode, message: "Refused." }, options.shareStatus);
				return json({ share: { id: SHARE_ID, slug: "abc", contentUrl: "https://abc.share.test/" }, replaced: false }, 201);
			}
			if (pathname === `/api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants`) {
				const body = init.method === "POST" ? (JSON.parse(String(init.body)) as { emails?: string[]; orgId?: string; teamIds?: string[]; workspace?: boolean }) : { emails: ["maya@acme.co"] };
				const grants = (body.emails ?? []).map((email, index) => ({ id: `${GRANT_ID.slice(0, -1)}${index}`, email: email.toLowerCase(), createdAt: "" }));
				// Answering without teams, as a control plane that predates team sharing does.
				if (!body.orgId) return json({ grants });
				const subject = (teamId?: string) => ({ id: GRANT_ID, orgId: body.orgId, ...(teamId ? { teamId } : {}), name: teamId ? "Design" : "Acme", memberCount: 3, createdAt: "" });
				return json({ grants, teams: (body.teamIds ?? []).map(subject), workspaces: body.workspace ? [subject()] : [] });
			}
			if (init.method === "PATCH" && pathname === `/api/cloud/v1/me/artifact-shares/${SHARE_ID}`) {
				return json({ id: SHARE_ID, slug: "abc", contentUrl: "https://abc.share.test/", ...(JSON.parse(String(init.body)) as object) });
			}
			if (init.method === "DELETE" && pathname.includes("/me/artifact-shares/")) return new Response(null, { status: 204 });
			return new Response("", { status: 404 });
		}),
	});
	const invoke = (channel: string, value?: unknown) => handlers.get(channel)!({ sender }, value);
	const file = { baseUrl: "https://cp.test", sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
	const cloudFile = { source: "cloud", baseUrl: "https://cp.test", orgId: ORG_ID, sessionId: CLOUD_SESSION_ID, path: "reports/q3 plan.md", projectId: "" };
	// Publishes as the popover does: with the fingerprint of the inspection it shows.
	const publish = async (target: Record<string, unknown> = file) => {
		const { fingerprint } = (await invoke("cloudShare:inspect", target)) as { fingerprint: string };
		return invoke("cloudShare:publish", { ...target, fingerprint });
	};
	const hostFile = { source: "host", hostId: HOST_ID, baseUrl: "https://cp.test", sessionId: "sess-1", path: "docs/deck.html", projectId: "proj-9", artifactId: "art-7" };
	return { invoke, publish, file, cloudFile, hostFile, hostFiles, hostRequests, requests, bodies, records, sent, handlers, sender, authorizations };
}

async function snapshots(): Promise<string[]> {
	return fs.readdir(path.join(dataDir, "snapshots")).catch(() => []);
}

describe("cloudShare IPC", () => {
	it("inspects a file from its metadata, without reading it, uploading it or calling the control plane", async () => {
		const { invoke, file, requests } = setup();
		const read = vi.spyOn(fs, "readFile");
		await expect(invoke("cloudShare:inspect", file)).resolves.toEqual({
			name: "report.html", size: 20, unsupported: false, fingerprint: expect.stringMatching(/^20:\d+$/),
		});
		// Only this computer's publish records are read, never the file.
		expect(read.mock.calls.map(([target]) => String(target)).filter((target) => target.endsWith("report.html"))).toEqual([]);
		expect(requests).toEqual([]);
		read.mockRestore();
	});

	it("tells whether a published file changed since, from its size and modification time", async () => {
		const { invoke, publish, file, requests } = setup();
		await publish();
		requests.length = 0;
		const unchanged = await invoke("cloudShare:inspect", file) as { fingerprint: string; publishedFingerprint?: string; shareId?: string };
		expect(unchanged).toMatchObject({ shareId: SHARE_ID, publishedFingerprint: unchanged.fingerprint });
		// Rewritten since: its size and modification time differ from the published fingerprint's.
		const later = new Date(Date.now() + 60_000);
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), "new content");
		await fs.utimes(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), later, later);
		const changed = await invoke("cloudShare:inspect", file) as { fingerprint: string; publishedFingerprint?: string };
		expect(changed.fingerprint).not.toBe(changed.publishedFingerprint);
		expect(requests).toEqual([]);
	});

	it("compares a cloud file's listed SHA-256 with the published one, with no request", async () => {
		const { invoke, cloudFile, requests } = setup();
		await invoke("cloudShare:publish", { ...cloudFile, fingerprint: CLOUD_SHA256 });
		requests.length = 0;
		await expect(invoke("cloudShare:inspect", { ...cloudFile, fingerprint: CLOUD_SHA256 })).resolves.toEqual({
			name: "q3 plan.md", unsupported: false, fingerprint: CLOUD_SHA256, shareId: SHARE_ID, publishedFingerprint: CLOUD_SHA256,
		});
		await expect(invoke("cloudShare:inspect", { ...cloudFile, fingerprint: "1".repeat(64) })).resolves.toMatchObject({ fingerprint: "1".repeat(64), publishedFingerprint: CLOUD_SHA256 });
		expect(requests).toEqual([]);
	});

	it("publishes when the window asks, with no second confirmation", async () => {
		const { publish, requests, sent } = setup();
		const result = await publish();
		expect(result).toMatchObject({ status: "published", share: { id: SHARE_ID }, replaced: false });
		expect(requests).toContain("POST /api/cloud/v1/me/artifact-shares/finalize");
		// Preparing the snapshot is the first half of the progress, the upload the second.
		expect(sent).toEqual([
			{ sessionId: "sess-1", path: "docs/report.html", sentBytes: 20, totalBytes: 40 },
			{ sessionId: "sess-1", path: "docs/report.html", sentBytes: 40, totalBytes: 40 },
		]);
		expect(await snapshots()).toEqual([]);
	});

	it("remembers the link a file was published to until it is unpublished", async () => {
		const { invoke, publish, file, records } = setup();
		await expect(invoke("cloudShare:inspect", file)).resolves.not.toHaveProperty("shareId");
		await publish();
		await expect(invoke("cloudShare:inspect", file)).resolves.toMatchObject({ shareId: SHARE_ID });
		// The record survives a restart: a new store reads it back from disk.
		const reopened = new ShareLinkRecords(path.join(dataDir, "share-links.json"));
		await expect(reopened.get(shareRecordKey("https://cp.test", "sess-1", "docs/report.html"))).resolves.toBe(SHARE_ID);
		// Another control plane's record is a different file.
		await expect(invoke("cloudShare:inspect", { ...file, baseUrl: "https://other.test" })).resolves.not.toHaveProperty("shareId");

		await invoke("cloudShare:unpublish", { baseUrl: "https://cp.test", shareId: SHARE_ID });
		await expect(invoke("cloudShare:inspect", file)).resolves.not.toHaveProperty("shareId");
		await expect(records.get(shareRecordKey("https://cp.test", "sess-1", "docs/report.html"))).resolves.toBeUndefined();
	});

	it("keeps an active link's URL in its record, which the daemon shows agents", async () => {
		const key = shareRecordKey("https://cp.test", "sess-1", "docs/report.html");
		const stored = async () => JSON.parse(await fs.readFile(path.join(dataDir, "share-links.json"), "utf8"))[key];
		await setup().publish();
		expect(await stored()).toMatchObject({ shareId: SHARE_ID, url: "https://abc.share.test/" });

		await fs.writeFile(path.join(dataDir, "share-links.json"), JSON.stringify({ [key]: SHARE_ID }));
		const share = { id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://share.test/abc" };
		await setup({ shares: [share] }).invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		// With who it opens for: a link listed with no access is public.
		expect(await stored()).toEqual({ shareId: SHARE_ID, url: "https://share.test/abc", access: "anyone" });
		await setup({ shares: [{ ...share, access: "restricted" }] }).invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		expect(await stored()).toEqual({ shareId: SHARE_ID, url: "https://share.test/abc", access: "restricted" });

		const { invoke, file } = setup({ shares: [{ ...share, revokedAt: "2026-11-01T00:00:00Z" }] });
		await invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		expect(await stored()).toBe(SHARE_ID);
		await expect(invoke("cloudShare:inspect", file)).resolves.toMatchObject({ shareId: SHARE_ID });
	});

	it("drops a link's URL once the control plane no longer lists it, and keeps other control planes' links", async () => {
		const key = shareRecordKey("https://cp.test", "sess-1", "docs/report.html");
		const other = shareRecordKey("https://other.test", "sess-1", "docs/report.html");
		const recordsFile = path.join(dataDir, "share-links.json");
		await fs.writeFile(recordsFile, JSON.stringify({
			[key]: { shareId: SHARE_ID, url: "https://share.test/abc" },
			[other]: { shareId: "other-share", url: "https://other.share.test/" },
		}));
		await setup({ shares: [] }).invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		const records = JSON.parse(await fs.readFile(recordsFile, "utf8"));
		expect(records[key]).toBe(SHARE_ID);
		expect(records[other]).toEqual({ shareId: "other-share", url: "https://other.share.test/" });
	});

	it("remembers what a file held when it was published, so a later change shows", async () => {
		const { invoke, publish, file } = setup();
		await expect(invoke("cloudShare:inspect", file)).resolves.not.toHaveProperty("publishedFingerprint");
		await publish();
		const published = await invoke("cloudShare:inspect", file) as { fingerprint: string; publishedFingerprint?: string };
		expect(published.publishedFingerprint).toBe(published.fingerprint);
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), "a newer version of the report");
		const changed = await invoke("cloudShare:inspect", file) as { fingerprint: string; publishedFingerprint?: string };
		expect(changed.publishedFingerprint).toBe(published.fingerprint);
		expect(changed.fingerprint).not.toBe(published.fingerprint);
	});

	it("republishes a changed file to its link, which clears the change", async () => {
		const { invoke, publish, file, bodies } = setup();
		await publish();
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), "a newer version of the report");
		const changed = await invoke("cloudShare:inspect", file) as { fingerprint: string };
		const result = await invoke("cloudShare:publish", { ...file, fingerprint: changed.fingerprint, shareId: SHARE_ID });
		expect(result).toMatchObject({ status: "published", share: { id: SHARE_ID } });
		// An update names the link, and where the file is now, so the link
		// follows a file moved to another folder.
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/uploads")).toMatchObject({ shareId: SHARE_ID, project: "proj-1", feature: "sess-1/docs" });
		await expect(invoke("cloudShare:inspect", file)).resolves.toMatchObject({ shareId: SHARE_ID, publishedFingerprint: changed.fingerprint, fingerprint: changed.fingerprint });
	});

	it("follows a renamed file to the link it was published to, by its artifact ID", async () => {
		const { invoke, publish, file, bodies } = setup();
		const artifact = { ...file, artifactId: "art-1" };
		const before = await invoke("cloudShare:inspect", artifact) as { fingerprint: string };
		await publish({ ...artifact, fingerprint: before.fingerprint });
		await fs.mkdir(path.join(dataDir, "artifacts", "sess-1", "final"), { recursive: true });
		await fs.rename(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), path.join(dataDir, "artifacts", "sess-1", "final", "summary.html"));
		const renamed = { ...artifact, path: "final/summary.html" };
		const inspection = await invoke("cloudShare:inspect", renamed) as { fingerprint: string };
		expect(inspection).toMatchObject({ name: "summary.html", shareId: SHARE_ID, publishedFingerprint: before.fingerprint });
		// Republish updates that link under the file's new name.
		const result = await invoke("cloudShare:publish", { ...renamed, fingerprint: inspection.fingerprint, shareId: SHARE_ID });
		expect(result).toMatchObject({ status: "published", share: { id: SHARE_ID } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/uploads")).toMatchObject({ name: "summary.html", shareId: SHARE_ID });
	});

	it("moves a record kept by path to the file's artifact ID", async () => {
		const recordsFile = path.join(dataDir, "share-links.json");
		await fs.writeFile(recordsFile, JSON.stringify({ [shareRecordKey("https://cp.test", "sess-1", "docs/report.html")]: SHARE_ID }));
		const { invoke, file } = setup();
		await expect(invoke("cloudShare:inspect", { ...file, artifactId: "art-1" })).resolves.toMatchObject({ shareId: SHARE_ID });
		const records = JSON.parse(await fs.readFile(recordsFile, "utf8"));
		expect(records).toEqual({ [artifactRecordKey("https://cp.test", "sess-1", "art-1")]: SHARE_ID });
	});

	it("reads a record kept before fingerprints were, as a link with nothing to compare", async () => {
		await fs.writeFile(path.join(dataDir, "share-links.json"), JSON.stringify({ [shareRecordKey("https://cp.test", "sess-1", "docs/report.html")]: SHARE_ID }));
		const { invoke, file, records } = setup();
		const inspection = await invoke("cloudShare:inspect", file);
		expect(inspection).toMatchObject({ shareId: SHARE_ID });
		expect(inspection).not.toHaveProperty("publishedFingerprint");
		await records.forgetShare(SHARE_ID);
		await expect(records.get(shareRecordKey("https://cp.test", "sess-1", "docs/report.html"))).resolves.toBeUndefined();
	});

	it("names the document by project, session and folder", async () => {
		const { publish, bodies } = setup();
		await publish();
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/uploads")).toMatchObject({
			name: "report.html", size: 20, project: "proj-1", feature: "sess-1/docs",
		});
	});

	it("turns refusals into results the dialog can act on", async () => {
		// A video browsers cannot play is refused before anything is sent.
		const unplayable = setup();
		for (const name of ["clip.mkv", "clip.ogv"]) {
			await expect(unplayable.invoke("cloudShare:inspect", { ...unplayable.file, path: name })).resolves.toMatchObject({ unsupported: true });
			await expect(unplayable.invoke("cloudShare:publish", { ...unplayable.file, path: name })).resolves.toEqual({
				status: "unsupported", message: "This video format can't be played in a browser. Use MP4, MOV or WebM.",
			});
		}
		expect(unplayable.requests).toEqual([]);

		// A playable video goes to the control plane, which refuses it while it serves none.
		const video = setup({ refuseVideos: true });
		await expect(video.invoke("cloudShare:inspect", { ...video.file, path: "clip.mp4" })).resolves.toMatchObject({ unsupported: false });
		await expect(video.publish({ ...video.file, path: "clip.mp4" })).resolves.toEqual({ status: "unsupported", message: "Sharing videos is coming soon." });

		// One that serves videos publishes it as any other file.
		const served = setup();
		await expect(served.publish({ ...served.file, path: "clip.mp4" })).resolves.toMatchObject({ status: "published" });

		const limit = setup({ finalizeStatus: 409, finalizeCode: "artifact_share_limit_reached" });
		await expect(limit.publish()).resolves.toEqual({
			status: "limit", message: "Free plan: up to 3 active links. Upgrade to share more.",
		});
		expect(limit.requests).toContain("POST /api/cloud/v1/me/artifact-shares/uploads/abort");

		const signedOut = setup({ token: null });
		await expect(signedOut.invoke("cloudShare:publish", signedOut.file)).resolves.toEqual({ status: "signed_out" });
		expect(signedOut.requests).toEqual([]);

		// Other refusals carry a category for telemetry beside their message.
		const tooLarge = setup({ finalizeStatus: 413, finalizeCode: "payload_too_large" });
		await expect(tooLarge.publish()).resolves.toMatchObject({ status: "error", failure: "too_large" });
		const other = setup({ finalizeStatus: 400, finalizeCode: "invalid_request" });
		const refused = await other.publish();
		expect(refused).toMatchObject({ status: "error" });
		expect(refused).not.toHaveProperty("failure");
	});

	it("refuses malformed requests and files outside the artifacts folder", async () => {
		const { invoke, file } = setup();
		// A file outside the artifacts folder is a refusal the popover shows.
		await expect(invoke("cloudShare:publish", { ...file, path: "../../etc/passwd" })).resolves.toMatchObject({ status: "error" });
		await expect(invoke("cloudShare:publish", { ...file, projectId: 5 })).rejects.toThrow("Not a file to share");
		// An empty project would name a second document for the same file.
		await expect(invoke("cloudShare:publish", { ...file, projectId: "" })).rejects.toThrow("Not a file to share");
		await expect(invoke("cloudShare:list", {})).rejects.toThrow("Not a control plane");
		await expect(invoke("cloudShare:unpublish", { baseUrl: "https://cp.test", shareId: "../x" })).rejects.toThrow("Not a share link");
	});

	it("reads a cloud file's version from its session's list only when the renderer did not name it, and never downloads it", async () => {
		const { invoke, cloudFile, requests } = setup();
		await expect(invoke("cloudShare:inspect", cloudFile)).resolves.toEqual({
			name: "q3 plan.md", size: 4096, unsupported: false, fingerprint: CLOUD_SHA256,
		});
		expect(requests).toEqual([`GET ${CLOUD_SESSION_PATH}/artifacts`]);
	});

	it("publishes a cloud session's file without uploading it", async () => {
		const { invoke, cloudFile, requests, bodies, records } = setup();
		await expect(invoke("cloudShare:publish", { ...cloudFile, fingerprint: CLOUD_SHA256 })).resolves.toMatchObject({ status: "published", share: { id: SHARE_ID } });
		expect(requests).toEqual([`POST ${CLOUD_SESSION_PATH}/artifacts/share`]);
		// The control plane shares only the version the person was shown.
		expect(bodies.get(`${CLOUD_SESSION_PATH}/artifacts/share`)).toEqual({ path: "reports/q3 plan.md", sha256: CLOUD_SHA256 });
		// The link is remembered apart from any local session's file.
		await expect(invoke("cloudShare:inspect", cloudFile)).resolves.toMatchObject({ shareId: SHARE_ID, publishedFingerprint: CLOUD_SHA256 });
		await expect(records.get(shareRecordKey("https://cp.test", CLOUD_SESSION_ID, "reports/q3 plan.md"))).resolves.toBeUndefined();

		// Cancel does not stop a cloud share midway: the control plane may
		// already have made the link, so it is waited for and shown.
		const publishing = invoke("cloudShare:publish", { ...cloudFile, fingerprint: CLOUD_SHA256, shareId: SHARE_ID });
		await invoke("cloudShare:cancel");
		await expect(publishing).resolves.toMatchObject({ status: "published", share: { id: SHARE_ID } });
		expect(bodies.get(`${CLOUD_SESSION_PATH}/artifacts/share`)).toEqual({ path: "reports/q3 plan.md", sha256: CLOUD_SHA256, shareId: SHARE_ID });
	});

	it("turns a cloud file's refusals into results the popover can act on", async () => {
		const limit = setup({ shareStatus: 409, shareCode: "artifact_share_limit_reached" });
		await expect(limit.publish(limit.cloudFile)).resolves.toEqual({ status: "limit", message: "Refused." });

		// Another member's session: only its creator shares its files.
		const notOwner = setup({ shareStatus: 403, shareCode: "artifact_share_not_owner" });
		await expect(notOwner.publish(notOwner.cloudFile)).resolves.toEqual({ status: "not_owner" });

		// A file the session changed since it was shown is shown again.
		const changed = setup({ shareStatus: 409, shareCode: "artifact_share_source_changed" });
		await expect(changed.publish(changed.cloudFile)).resolves.toMatchObject({ status: "changed", inspection: { name: "q3 plan.md", fingerprint: CLOUD_SHA256 } });
		const unseen = setup();
		await expect(unseen.invoke("cloudShare:publish", unseen.cloudFile)).resolves.toMatchObject({ status: "changed" });
		expect(unseen.requests.filter((request) => request.endsWith("/share"))).toEqual([]);

		const unplayable = setup();
		await expect(unplayable.invoke("cloudShare:publish", { ...unplayable.cloudFile, path: "demo.mkv" })).resolves.toMatchObject({ status: "unsupported" });
		expect(unplayable.requests).toEqual([]);

		// A playable one is the control plane's to refuse.
		const video = setup({ shareStatus: 415, shareCode: "artifact_share_type_unsupported" });
		await expect(video.publish(video.cloudFile)).resolves.toEqual({ status: "unsupported", message: "Refused." });

		const signedOut = setup({ token: null });
		await expect(signedOut.invoke("cloudShare:publish", signedOut.cloudFile)).resolves.toEqual({ status: "signed_out" });

		const malformed = setup();
		await expect(malformed.invoke("cloudShare:publish", { ...malformed.cloudFile, orgId: "../x", fingerprint: CLOUD_SHA256 })).resolves.toEqual({ status: "error", message: "Not a cloud session's artifact." });
		await expect(malformed.invoke("cloudShare:publish", { ...malformed.cloudFile, orgId: undefined })).rejects.toThrow("Not a file to share");
		await expect(malformed.invoke("cloudShare:inspect", { ...malformed.cloudFile, path: "missing.md" })).rejects.toThrow("no longer available");
	});

	it("uploads the images a page uses with it and says what it left out", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.mkdir(path.join(docs, "images"));
		await fs.writeFile(path.join(docs, "images", "chart.png"), "png");
		await fs.writeFile(path.join(docs, "photo.jpg"), "jpeg");
		await fs.writeFile(path.join(docs, "clip.webm"), "video");
		await fs.writeFile(path.join(docs, "report.html"), `<img src="images/chart.png"><img src="photo.jpg"><video src="clip.webm"></video><img src="../clip.mp4"><img src="https://cdn.test/x.png">`);
		const { publish, requests, bodies, sent, records } = setup({ files: true, refuseVideos: true });
		const result = await publish();
		// The video is refused by a control plane that serves none yet; the page goes without it.
		expect(result).toMatchObject({ status: "published", pageFiles: { included: 2, skipped: 2, videos: 1, unsupported: false } });
		const starts = requests.filter((request) => request.endsWith("/uploads/files"));
		expect(starts).toHaveLength(3);
		// Recorded as missing, so the control plane taking videos later offers a republish.
		expect((await records.publishedPageFiles(JSON.stringify(["https://cp.test", "sess-1", "docs/report.html"])))?.["docs/clip.webm"]).toBe("");
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:images/chart.png" }, { fileToken: "ft:photo.jpg" }] });
		// Progress ends at the page's and its files' bytes, copied and sent.
		const last = sent.at(-1) as { sentBytes: number; totalBytes: number };
		const pageSize = (await fs.stat(path.join(docs, "report.html"))).size;
		expect(last).toMatchObject({ sentBytes: (pageSize + 3 + 4 + 5) * 2, totalBytes: (pageSize + 3 + 4 + 5) * 2 });
		expect(await snapshots()).toEqual([]);
	});

	it("uploads a page's video with it once the control plane serves videos", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "clip.webm"), "video");
		await fs.writeFile(path.join(docs, "take.mkv"), "video");
		await fs.writeFile(path.join(docs, "theora.ogv"), "video");
		await fs.writeFile(path.join(docs, "report.html"), `<video src="clip.webm"></video><video src="take.mkv"></video><video src="theora.ogv"></video>`);
		const { publish, bodies, requests } = setup({ files: true });
		// A format browsers cannot play is left out, though not as a video the control plane refused.
		await expect(publish()).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 2, videos: 0, unsupported: false } });
		expect(requests.filter((request) => request.endsWith("/uploads/files"))).toHaveLength(1);
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:clip.webm" }] });
	});

	it("leaves out a page's file that is removed before it is copied, and still publishes the page", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "photo.jpg"), "jpeg");
		await fs.writeFile(path.join(docs, "chart.png"), "png");
		await fs.writeFile(path.join(docs, "report.html"), `<img src="photo.jpg"><img src="chart.png">`);
		const { publish, bodies } = setup({ files: true });
		// Removed once the page's files were found, as the page's copy is written.
		const open = fs.open;
		const spy = vi.spyOn(fs, "open").mockImplementation(async (...args: Parameters<typeof fs.open>) => {
			if (path.dirname(String(args[0])) === path.join(dataDir, "snapshots")) await fs.rm(path.join(docs, "photo.jpg"), { force: true });
			return open(...args);
		});
		try {
			await expect(publish()).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1, unsupported: false } });
		} finally {
			spy.mockRestore();
		}
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:chart.png" }] });
		expect(await snapshots()).toEqual([]);
	});

	it("publishes a page alone to a control plane that cannot serve its images, and says so", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "photo.jpg"), "jpeg");
		await fs.writeFile(path.join(docs, "report.html"), `<img src="photo.jpg">`);
		const { publish, bodies } = setup();
		await expect(publish()).resolves.toMatchObject({ status: "published", pageFiles: { included: 0, skipped: 0, unsupported: true } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).not.toHaveProperty("files");
		expect(await snapshots()).toEqual([]);
	});

	it("says what a page left out though the control plane cannot take its files", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "report.html"), `<video src="clip.mkv"></video><img src="../outside.png">`);
		const { publish } = setup();
		await expect(publish()).resolves.toMatchObject({ status: "published", pageFiles: { included: 0, skipped: 2, videos: 0, unsupported: false } });
	});

	it("offers to republish a page when a file it uses changes, appears or goes, though the page did not", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "chart.svg"), "<svg>one</svg>");
		await fs.writeFile(path.join(docs, "report.html"), `<img src="chart.svg"><img src="later.png">`);
		const { invoke, publish, file, records } = setup({ files: true });
		await publish();
		const inspect = () => invoke("cloudShare:inspect", file) as Promise<{ fingerprint: string; publishedFingerprint?: string; pageFilesChanged?: boolean }>;
		const unchanged = await inspect();
		expect(unchanged.fingerprint).toBe(unchanged.publishedFingerprint);
		expect(unchanged.pageFilesChanged).toBeUndefined();

		// Only the image is rewritten.
		const later = new Date(Date.now() + 60_000);
		await fs.writeFile(path.join(docs, "chart.svg"), "<svg>two, longer</svg>");
		await fs.utimes(path.join(docs, "chart.svg"), later, later);
		const changed = await inspect();
		expect(changed.fingerprint).toBe(changed.publishedFingerprint);
		expect(changed.pageFilesChanged).toBe(true);

		// Republishing records the files as they are now.
		await publish();
		expect((await inspect()).pageFilesChanged).toBeUndefined();

		// A file the page used that was missing appears, or one goes.
		await fs.writeFile(path.join(docs, "later.png"), "png");
		expect((await inspect()).pageFilesChanged).toBe(true);
		await publish();
		await fs.rm(path.join(docs, "chart.svg"));
		expect((await inspect()).pageFilesChanged).toBe(true);
		// The record names each file in the session's artifacts, the page's own fingerprint apart.
		expect(Object.keys((await records.publishedPageFiles(JSON.stringify(["https://cp.test", "sess-1", "docs/report.html"]))) ?? {}).sort()).toEqual(["docs/chart.svg", "docs/later.png"]);
	});

	it("offers to republish a page when a symlink it uses is pointed at another file", async () => {
		const docs = path.join(dataDir, "artifacts", "sess-1", "docs");
		await fs.writeFile(path.join(docs, "old.png"), "old");
		await fs.writeFile(path.join(docs, "new.png"), "new");
		await fs.symlink("old.png", path.join(docs, "chart.png"));
		await fs.writeFile(path.join(docs, "report.html"), `<img src="chart.png">`);
		const { invoke, publish, file, records } = setup({ files: true });
		await publish();
		const inspect = () => invoke("cloudShare:inspect", file) as Promise<{ pageFilesChanged?: boolean }>;
		expect((await inspect()).pageFilesChanged).toBeUndefined();
		// Kept by the path the page names, not the file it led to.
		expect(Object.keys((await records.publishedPageFiles(JSON.stringify(["https://cp.test", "sess-1", "docs/report.html"]))) ?? {})).toEqual(["docs/chart.png"]);

		// Neither the page nor old.png changes: only where chart.png leads.
		await fs.rm(path.join(docs, "chart.png"));
		await fs.symlink("new.png", path.join(docs, "chart.png"));
		expect((await inspect()).pageFilesChanged).toBe(true);
	});

	it("publishes nothing when the file is rewritten while it is being copied", async () => {
		const { invoke, file, requests, sender } = setup();
		const big = path.join(dataDir, "artifacts", "sess-1", "docs", "report.html");
		await fs.writeFile(big, Buffer.alloc(2 << 20, 1));
		const past = new Date("2020-01-01T00:00:00Z");
		await fs.utimes(big, past, past);
		const shown = (await invoke("cloudShare:inspect", file)) as { fingerprint: string };
		// The first progress report comes after the first MiB is copied: rewrite the second.
		sender.send.mockImplementationOnce(() => {
			const handle = openSync(big, "r+");
			writeSync(handle, Buffer.alloc(1 << 20, 2), 0, 1 << 20, 1 << 20);
			closeSync(handle);
			return false;
		});
		const result = await invoke("cloudShare:publish", { ...file, fingerprint: shown.fingerprint }) as { status: string; inspection?: { fingerprint: string; size: number } };
		expect(result.status).toBe("changed");
		expect(result.inspection?.size).toBe(2 << 20);
		expect(result.inspection?.fingerprint).not.toBe(shown.fingerprint);
		expect(requests).toEqual([]);
		expect(await snapshots()).toEqual([]);
	});

	it("publishes nothing when the file changed since it was inspected", async () => {
		const { invoke, file, requests } = setup();
		const shown = (await invoke("cloudShare:inspect", file)) as { fingerprint: string };
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), "a rewritten report, longer than before");
		const result = await invoke("cloudShare:publish", { ...file, fingerprint: shown.fingerprint });
		expect(result).toMatchObject({ status: "changed", inspection: { name: "report.html", size: 38 } });
		expect((result as { inspection: { fingerprint: string } }).inspection.fingerprint).not.toBe(shown.fingerprint);
		expect(requests).toEqual([]);
		expect(await snapshots()).toEqual([]);
		// A publish with no inspection at all is refused the same way.
		await expect(invoke("cloudShare:publish", file)).resolves.toMatchObject({ status: "changed" });
		expect(requests).toEqual([]);
	});

	it("sends who a new link opens for only when asked, so an older control plane is not refused", async () => {
		const plain = setup();
		await expect(plain.publish()).resolves.toMatchObject({ status: "published" });
		expect(plain.bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).not.toHaveProperty("access");
		expect(plain.bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).not.toHaveProperty("emails");

		const restricted = setup();
		await expect(restricted.publish({ ...restricted.file, access: "restricted", emails: ["dev@acme.co"] })).resolves.toMatchObject({ status: "published" });
		expect(restricted.bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ access: "restricted", emails: ["dev@acme.co"] });

		const cloud = setup();
		await expect(cloud.invoke("cloudShare:publish", { ...cloud.cloudFile, fingerprint: CLOUD_SHA256, access: "restricted" })).resolves.toMatchObject({ status: "published" });
		expect(cloud.bodies.get(`${CLOUD_SESSION_PATH}/artifacts/share`)).toEqual({ path: "reports/q3 plan.md", sha256: CLOUD_SHA256, access: "restricted" });

		await expect(plain.invoke("cloudShare:publish", { ...plain.file, access: "everyone" })).rejects.toThrow("Not who a link opens for");
		await expect(plain.invoke("cloudShare:publish", { ...plain.file, emails: "dev@acme.co" })).rejects.toThrow("Not a list of emails");
	});

	it("records a live link's new access, so the daemon marks its artifact at once", async () => {
		const key = shareRecordKey("https://cp.test", "sess-1", "docs/report.html");
		await fs.writeFile(path.join(dataDir, "share-links.json"), JSON.stringify({ [key]: { shareId: SHARE_ID, url: "https://share.test/abc", access: "restricted" } }));
		const { invoke } = setup();
		await invoke("cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: SHARE_ID, access: "anyone" });
		const stored = JSON.parse(await fs.readFile(path.join(dataDir, "share-links.json"), "utf8"))[key];
		expect(stored).toEqual({ shareId: SHARE_ID, url: "https://share.test/abc", access: "anyone" });
	});

	it("never lets a list read that was in flight undo an access change made meanwhile", async () => {
		const key = shareRecordKey("https://cp.test", "sess-1", "docs/report.html");
		const recordsFile = path.join(dataDir, "share-links.json");
		await fs.writeFile(recordsFile, JSON.stringify({ [key]: { shareId: SHARE_ID, url: "https://share.test/abc", access: "restricted" } }));
		let release: () => void = () => undefined;
		const gate = new Promise<void>((resolve) => { release = resolve; });
		// The list is requested while the link is still private.
		const { invoke } = setup({ shares: [{ id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://share.test/abc", access: "restricted" }], listGate: gate });
		const listing = invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		// Meanwhile the person makes it public, and that is recorded.
		await invoke("cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: SHARE_ID, access: "anyone" });
		const stored = async () => JSON.parse(await fs.readFile(recordsFile, "utf8"))[key];
		expect(await stored()).toMatchObject({ access: "anyone" });
		// The older list answers last: the newer access stands.
		release();
		await listing;
		expect(await stored()).toMatchObject({ access: "anyone" });
		// A list requested after the change still corrects the record as usual.
		const later = setup({ shares: [{ id: SHARE_ID, slug: "abc", name: "report.html", contentUrl: "https://abc.share.test/", viewerUrl: "https://share.test/abc", access: "restricted" }] });
		await later.invoke("cloudShare:list", { baseUrl: "https://cp.test" });
		expect(await stored()).toMatchObject({ access: "restricted" });
	});

	it("changes a live link's access and who it is shared with", async () => {
		const { invoke, requests, bodies } = setup();
		await expect(invoke("cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: SHARE_ID, access: "anyone" })).resolves.toMatchObject({ id: SHARE_ID, access: "anyone" });
		expect(bodies.get(`/api/cloud/v1/me/artifact-shares/${SHARE_ID}`)).toEqual({ access: "anyone" });
		await expect(invoke("cloudShare:grants", { baseUrl: "https://cp.test", shareId: SHARE_ID })).resolves.toEqual({ people: [{ id: `${GRANT_ID.slice(0, -1)}0`, email: "maya@acme.co", createdAt: "" }], teams: [], workspaces: [] });
		await expect(invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, emails: ["Dev@acme.co"] })).resolves.toEqual({ people: [{ id: `${GRANT_ID.slice(0, -1)}0`, email: "dev@acme.co", createdAt: "" }], teams: [], workspaces: [] });
		expect(bodies.get(`/api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants`)).toEqual({ emails: ["Dev@acme.co"] });
		await expect(invoke("cloudShare:removeGrant", { baseUrl: "https://cp.test", shareId: SHARE_ID, grantId: GRANT_ID })).resolves.toBeUndefined();
		expect(requests).toEqual([
			`PATCH /api/cloud/v1/me/artifact-shares/${SHARE_ID}`,
			`GET /api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants`,
			`POST /api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants`,
			`DELETE /api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants/${GRANT_ID}`,
		]);
		await expect(invoke("cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: SHARE_ID, access: "public" })).rejects.toThrow("Not who a link opens for");
		await expect(invoke("cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: "../x", access: "anyone" })).rejects.toThrow("Not a share link");
		await expect(invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, emails: [] })).rejects.toThrow("Not a list of emails");
		await expect(invoke("cloudShare:removeGrant", { baseUrl: "https://cp.test", shareId: SHARE_ID, grantId: "../x" })).rejects.toThrow("Not someone the link is shared with");
	});

	it("shares a link with a team workspace's teams and everyone in it", async () => {
		const { invoke, bodies } = setup();
		const orgId = "0c6e6d0a-7a3b-4c39-9a51-5d0f3c1d2e41";
		const teamId = "1d7f7e1b-8b4c-4d4a-8b62-6e1a4d2e3f52";
		const shared = await invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, orgId, teamIds: [teamId], workspace: true });
		// Only what is set is sent: no empty emails.
		expect(bodies.get(`/api/cloud/v1/me/artifact-shares/${SHARE_ID}/grants`)).toEqual({ orgId, teamIds: [teamId], workspace: true });
		expect(shared).toMatchObject({ people: [], teams: [{ orgId, teamId, name: "Design" }], workspaces: [{ orgId, name: "Acme" }] });
		// A team needs the workspace it belongs to.
		await expect(invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, teamIds: [teamId] })).rejects.toThrow("Not a team or workspace");
		await expect(invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, orgId: "../x", workspace: true })).rejects.toThrow("Not a team or workspace");
		await expect(invoke("cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, orgId, workspace: "yes" })).rejects.toThrow("Not a workspace");
	});

	it("changes access, invites and removes people only from the app's main frame", async () => {
		const { handlers } = setup();
		const subframe = { sender: { id: 7 }, senderFrame: { parent: {} } };
		for (const [channel, value] of [
			["cloudShare:setAccess", { baseUrl: "https://cp.test", shareId: SHARE_ID, access: "anyone" }],
			["cloudShare:invite", { baseUrl: "https://cp.test", shareId: SHARE_ID, emails: ["a@b.co"] }],
			["cloudShare:removeGrant", { baseUrl: "https://cp.test", shareId: SHARE_ID, grantId: GRANT_ID }],
		] as const) {
			await expect(Promise.resolve().then(() => handlers.get(channel)!(subframe, value))).rejects.toThrow("only available from the app window");
		}
	});

	it("refuses a second publish from the same window at once", async () => {
		const { invoke, file } = setup();
		const { fingerprint } = (await invoke("cloudShare:inspect", file)) as { fingerprint: string };
		const first = invoke("cloudShare:publish", { ...file, fingerprint });
		await expect(invoke("cloudShare:publish", { ...file, fingerprint })).resolves.toEqual({ status: "error", message: "Another file is being shared." });
		await expect(first).resolves.toMatchObject({ status: "published" });
	});

	it("answers only the window's main frame", async () => {
		const { handlers, sender, file } = setup();
		// A subframe has a parent; the main frame has none. No frame objects are compared.
		const fromSubframe = { sender, senderFrame: { parent: {} } };
		await expect(Promise.resolve().then(() => handlers.get("cloudShare:inspect")!(fromSubframe, file))).rejects.toThrow("only available from the app window");
		await expect(Promise.resolve().then(() => handlers.get("cloudShare:publish")!(fromSubframe, file))).rejects.toThrow("only available from the app window");
		await expect(Promise.resolve().then(() => handlers.get("cloudShare:inspect")!({ sender, senderFrame: { parent: null } }, file))).resolves.toMatchObject({ name: "report.html" });
		await expect(Promise.resolve().then(() => handlers.get("cloudShare:inspect")!({ sender, senderFrame: null }, file))).rejects.toThrow("only available from the app window");
	});

	it("says whether the control plane serves share links, signed in or not", async () => {
		const on = setup();
		await expect(on.invoke("cloudShare:available", { baseUrl: "https://cp.test" })).resolves.toBe(true);
		expect(on.authorizations).toEqual(["Bearer token-1"]);
		// Its share routes 404 while its share flag is off.
		const off = setup({ listStatus: 404 });
		await expect(off.invoke("cloudShare:available", { baseUrl: "https://cp.test" })).resolves.toBe(false);
		// Signed out, a control plane that serves them answers 401.
		const signedOut = setup({ token: null, listStatus: 401 });
		await expect(signedOut.invoke("cloudShare:available", { baseUrl: "https://cp.test" })).resolves.toBe(true);
		expect(signedOut.authorizations).toEqual([null]);
		const unreachable = setup({ unreachable: true });
		await expect(unreachable.invoke("cloudShare:available", { baseUrl: "https://cp.test" })).resolves.toBeNull();
	});
});

describe("cloudShare IPC for a remote host's file", () => {
	const MTIME_NS = 1_763_000_000_123_456_789n;
	const deck = (artifact: Partial<HostArtifact> = {}) => new Map([["sess-1/docs/deck.html", { body: "<h1>remote deck</h1>", mtimeNs: MTIME_NS, ...artifact }]]);

	it("inspects it from the host's headers alone, with the fingerprint a local file's metadata would give", async () => {
		const { invoke, hostFile, hostRequests, requests } = setup({ hostFiles: deck() });
		await expect(invoke("cloudShare:inspect", hostFile)).resolves.toEqual({
			name: "deck.html", size: 20, unsupported: false, fingerprint: `20:${MTIME_NS / 1_000_000n}`,
		});
		expect(hostRequests).toEqual(["GET /proxy-token/api/v1/sessions/sess-1/preview/files/docs/deck.html?source=artifact&raw=true"]);
		expect(requests).toEqual([]);
	});

	it("falls back to Content-Length and Last-Modified on a host that sends no tag", async () => {
		const { invoke, hostFile } = setup({ hostFiles: deck({ tag: false }) });
		const inspection = await invoke("cloudShare:inspect", hostFile) as { fingerprint: string };
		// Last-Modified has only seconds.
		expect(inspection.fingerprint).toBe(`20:${(MTIME_NS / 1_000_000_000n) * 1000n}`);
	});

	it("publishes it by downloading it through the host's proxy, and keeps its record apart from this computer's sessions", async () => {
		const { publish, invoke, hostFile, hostRequests, requests, records, sent } = setup({ hostFiles: deck() });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", share: { id: SHARE_ID } });
		// The panel's inspect, publish's own version check and the check after the download each read only a GET's headers;
		// the third GET is the download. The route refuses HEAD.
		expect(hostRequests.filter((request) => request.startsWith("GET "))).toHaveLength(4);
		expect(hostRequests.some((request) => request.startsWith("HEAD "))).toBe(false);
		expect(requests).toContain("POST /api/cloud/v1/me/artifact-shares/finalize");
		expect(sent.at(-1)).toEqual({ sessionId: "sess-1", path: "docs/deck.html", sentBytes: 40, totalBytes: 40 });
		expect(await snapshots()).toEqual([]);
		// Recorded under the host's session, never under this computer's sess-1.
		await expect(records.get(artifactRecordKey("https://cp.test", `host:${HOST_ID}/sess-1`, "art-7"))).resolves.toBe(SHARE_ID);
		await expect(records.get(artifactRecordKey("https://cp.test", "sess-1", "art-7"))).resolves.toBeUndefined();
		await expect(invoke("cloudShare:inspect", hostFile)).resolves.toMatchObject({ shareId: SHARE_ID });
		await expect(invoke("cloudShare:inspect", { ...hostFile, hostId: "https://other-vm.useao.dev" })).rejects.toThrow("isn't connected");
	});

	it("lists the links of remote hosts' files for the renderer to mark, for one control plane", async () => {
		const { publish, invoke, hostFile } = setup({ hostFiles: deck() });
		await publish(hostFile);
		await publish();
		await expect(invoke("cloudShare:hostLinks", { baseUrl: "https://cp.test" })).resolves.toEqual([
			{ hostId: HOST_ID, sessionId: "sess-1", artifactId: "art-7", url: "https://abc.share.test/", access: "anyone" },
		]);
		await expect(invoke("cloudShare:hostLinks", { baseUrl: "https://other.test" })).resolves.toEqual([]);
	});

	it("publishes nothing when the file changed on the host since it was inspected", async () => {
		const hostFiles = deck();
		const { invoke, hostFile, requests } = setup({ hostFiles });
		const { fingerprint } = await invoke("cloudShare:inspect", hostFile) as { fingerprint: string };
		hostFiles.set("sess-1/docs/deck.html", { body: "<h1>rewritten deck!</h1>", mtimeNs: MTIME_NS + 5_000_000_000n });
		await expect(invoke("cloudShare:publish", { ...hostFile, fingerprint })).resolves.toMatchObject({
			status: "changed", inspection: { size: 24, fingerprint: `24:${(MTIME_NS + 5_000_000_000n) / 1_000_000n}` },
		});
		expect(requests).not.toContain("POST /api/cloud/v1/me/artifact-shares/uploads");
	});

	it("publishes nothing when the download is not the whole version the host named", async () => {
		const { publish, hostFile, requests } = setup({ hostFiles: deck({ shortBy: 3 }) });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "changed" });
		expect(requests).not.toContain("POST /api/cloud/v1/me/artifact-shares/uploads");
		expect(await snapshots()).toEqual([]);
	});

	it("publishes nothing when the file was rewritten on the host while it downloaded", async () => {
		const hostFiles = deck();
		let served = 0;
		// The download (the third read) gets the old version's tag and length; the file is rewritten in place, same length, as it streams.
		hostFiles.get("sess-1/docs/deck.html")!.served = () => {
			if (++served === 3) hostFiles.set("sess-1/docs/deck.html", { body: "<h1>REMOTE DECK</h1>", mtimeNs: MTIME_NS + 1_000_000_000n });
		};
		const { publish, hostFile, requests } = setup({ hostFiles });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "changed" });
		expect(requests).not.toContain("POST /api/cloud/v1/me/artifact-shares/uploads");
		expect(await snapshots()).toEqual([]);
	});

	it("refuses paths outside the session's artifacts and its version history before asking the host", async () => {
		const { invoke, hostFile, hostRequests } = setup({ hostFiles: deck() });
		for (const bad of ["../../etc/passwd", "/etc/passwd", ".versions/deck.html/1", "docs/../../x", "docs\\deck.html"]) {
			await expect(invoke("cloudShare:publish", { ...hostFile, path: bad, fingerprint: "1:1" })).resolves.toMatchObject({ status: "error" });
		}
		await expect(invoke("cloudShare:publish", { ...hostFile, hostId: "" })).rejects.toThrow("Not a file to share");
		expect(hostRequests).toEqual([]);
	});

	// A page on the host and the files it links to, beside it in the host's session artifacts.
	const hostPage = (html: string, files: Record<string, Partial<HostArtifact> & { body: string }>) => new Map<string, HostArtifact>([
		["sess-1/docs/deck.html", { body: html, mtimeNs: MTIME_NS }],
		...Object.entries(files).map(([name, artifact]) => [`sess-1/docs/${name}`, { mtimeNs: MTIME_NS, ...artifact }] as [string, HostArtifact]),
	]);

	it("uploads the images a host's page links to with it, downloaded through the host's proxy, and says what it left out", async () => {
		const html = `<img src="img/a.png"><img src="img/b.png"><img src="img/a.png"><img src="gone.png"><img src="../outside.png"><video src="clip.webm"></video><img src="https://cdn.test/x.png">`;
		const { publish, hostFile, hostRequests, requests, bodies, sent } = setup({
			files: true,
			refuseVideos: true,
			hostFiles: hostPage(html, { "img/a.png": { body: "png-a" }, "img/b.png": { body: "png-bb" }, "clip.webm": { body: "video" } }),
		});
		// The missing image and the one outside the page's folder are left out; the video is refused by a control plane that serves none yet.
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 2, skipped: 3, videos: 1, unsupported: false } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:img/a.png" }, { fileToken: "ft:img/b.png" }] });
		expect(requests.filter((request) => request.endsWith("/uploads/files"))).toHaveLength(3);
		// Each file is asked for once by its path on the host; nothing outside the page's folder is asked for.
		const asked = (name: string) => hostRequests.filter((request) => request.includes(`/preview/files/${name}?`));
		expect(asked("docs/img/a.png").length).toBeGreaterThan(0);
		expect(asked("docs/gone.png")).toHaveLength(1);
		expect(hostRequests.some((request) => request.includes("outside.png"))).toBe(false);
		// Progress ends at the page's and its files' bytes, downloaded and sent.
		const total = (html.length + 5 + 6 + 5) * 2;
		expect(sent.at(-1)).toEqual({ sessionId: "sess-1", path: "docs/deck.html", sentBytes: total, totalBytes: total });
		expect(await snapshots()).toEqual([]);
	});

	it("leaves out a host page's file the host refuses to serve, and still publishes the page", async () => {
		const { publish, hostFile, bodies } = setup({
			files: true,
			hostFiles: hostPage(`<img src="a.png"><img src="secret.png">`, { "a.png": { body: "png" }, "secret.png": { body: "png", status: 403 } }),
		});
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:a.png" }] });
	});

	it("leaves out a host page's file removed after it was found, and still publishes the page", async () => {
		const hostFiles = hostPage(`<img src="a.png"><img src="b.png">`, { "a.png": { body: "png" }, "b.png": { body: "png" } });
		// Gone once it was looked up, before it is downloaded.
		hostFiles.get("sess-1/docs/a.png")!.served = () => hostFiles.delete("sess-1/docs/a.png");
		const { publish, hostFile, bodies } = setup({ files: true, hostFiles });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:b.png" }] });
		expect(await snapshots()).toEqual([]);
	});

	it("leaves out a host page's file that is a symlink to a file outside the page's folder", async () => {
		const { publish, hostFile, bodies } = setup({
			files: true,
			hostFiles: hostPage(`<img src="a.png"><img src="link.png">`, { "a.png": { body: "png" }, "link.png": { body: "customer", resolvesTo: "private/customer.png" } }),
		});
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:a.png" }] });
	});

	it("leaves out a host page's file whose symlink is pointed elsewhere between its lookup and its download", async () => {
		const hostFiles = hostPage(`<img src="link.png">`, { "link.png": { body: "png" } });
		const link = hostFiles.get("sess-1/docs/link.png")!;
		link.served = () => { link.resolvesTo = "private/customer.png"; };
		const { publish, hostFile, bodies } = setup({ files: true, hostFiles });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 0, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [] });
		expect(await snapshots()).toEqual([]);
	});

	it("leaves out a host page's files when the host is too old to say where they resolve", async () => {
		const { publish, hostFile, bodies } = setup({
			files: true,
			hostFiles: hostPage(`<img src="a.png">`, { "a.png": { body: "png", sendsResolved: false } }),
		});
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 0, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [] });
	});

	it("leaves out a host page's file the host refuses once it is downloaded", async () => {
		const hostFiles = hostPage(`<img src="a.png"><img src="b.png">`, { "a.png": { body: "png" }, "b.png": { body: "png" } });
		const refused = hostFiles.get("sess-1/docs/a.png")!;
		// Found, then refused (403) when it is downloaded.
		refused.served = () => { refused.status = 403; };
		const { publish, hostFile, bodies } = setup({ files: true, hostFiles });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1 } });
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/finalize")).toMatchObject({ files: [{ fileToken: "ft:b.png" }] });
		expect(await snapshots()).toEqual([]);
	});

	it("offers to republish a host page when a file it uses changes, is pointed elsewhere or appears, though the page did not", async () => {
		const hostFiles = hostPage(`<img src="a.png"><img src="later.png">`, { "a.png": { body: "png" } });
		const { invoke, publish, hostFile, records } = setup({ files: true, hostFiles });
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "published", pageFiles: { included: 1, skipped: 1 } });
		const inspect = () => invoke("cloudShare:inspect", hostFile) as Promise<{ pageFilesChanged?: boolean }>;
		expect((await inspect()).pageFilesChanged).toBeUndefined();
		// The record names each file by the path the page uses, with where it resolved and its version; a missing one as "".
		const recorded = await records.publishedPageFiles(artifactRecordKey("https://cp.test", `host:${HOST_ID}/sess-1`, "art-7"));
		expect(recorded).toEqual({ "docs/a.png": `docs/a.png\n3:${MTIME_NS / 1_000_000n}`, "docs/later.png": "" });

		// The image is rewritten on the host.
		hostFiles.set("sess-1/docs/a.png", { body: "png2", mtimeNs: MTIME_NS + 1_000_000_000n });
		expect((await inspect()).pageFilesChanged).toBe(true);
		hostFiles.set("sess-1/docs/a.png", { body: "png", mtimeNs: MTIME_NS });
		expect((await inspect()).pageFilesChanged).toBeUndefined();
		// A symlink is pointed at another file of the same version.
		hostFiles.set("sess-1/docs/a.png", { body: "png", mtimeNs: MTIME_NS, resolvesTo: "docs/b.png" });
		expect((await inspect()).pageFilesChanged).toBe(true);
		hostFiles.set("sess-1/docs/a.png", { body: "png", mtimeNs: MTIME_NS });
		// A file the page used that was missing appears.
		hostFiles.set("sess-1/docs/later.png", { body: "png", mtimeNs: MTIME_NS });
		expect((await inspect()).pageFilesChanged).toBe(true);
	});

	it("publishes nothing when the host stops answering while a page's files are found", async () => {
		const { publish, hostFile, requests } = setup({
			files: true,
			hostFiles: hostPage(`<img src="a.png">`, { "a.png": { body: "png", status: 502 } }),
		});
		await expect(publish(hostFile)).resolves.toMatchObject({ status: "error" });
		expect(requests).not.toContain("POST /api/cloud/v1/me/artifact-shares/uploads");
		expect(await snapshots()).toEqual([]);
	});

	it("says the file is gone when the host no longer has it", async () => {
		const { invoke, hostFile } = setup({ hostFiles: new Map() });
		await expect(invoke("cloudShare:inspect", hostFile)).rejects.toThrow("no longer available");
	});
});
