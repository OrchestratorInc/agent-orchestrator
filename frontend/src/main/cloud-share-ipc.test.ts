// @vitest-environment node
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import { CLOUD_SHARE_PROGRESS_CHANNEL } from "../shared/cloud-share";
import { registerCloudShareIpc } from "./cloud-share-ipc";
import { ShareLinkRecords, shareRecordKey } from "./cloud-share-links";

// Built at runtime so the repository's secret scanner does not flag the fixture.
const FAKE_AWS_KEY = ["AKIA", "ABCDEFGHIJKLMNOP"].join("");
const SHARE_ID = "0b6e7a3c-2f4d-4a51-9c8e-1d2f3a4b5c6d";
const ORG_ID = "a1a1a1a1-0000-4000-8000-000000000001";
const CLOUD_SESSION_ID = "a1a1a1a1-0000-4000-8000-0000000000a1";
const CLOUD_SESSION_PATH = `/api/cloud/v1/orgs/${ORG_ID}/sessions/${CLOUD_SESSION_ID}`;
const CLOUD_SHA256 = "0".repeat(64);

let dataDir: string;

beforeEach(async () => {
	dataDir = await fs.mkdtemp(path.join(os.tmpdir(), "ao-share-ipc-"));
	await fs.mkdir(path.join(dataDir, "artifacts", "sess-1", "docs"), { recursive: true });
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), FAKE_AWS_KEY);
	await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "clip.mp4"), "video");
});

afterEach(async () => {
	await fs.rm(dataDir, { recursive: true, force: true });
});

function setup(options: { token?: string | null; finalizeStatus?: number; finalizeCode?: string; shareStatus?: number; shareCode?: string; listStatus?: number; unreachable?: boolean } = {}) {
	const handlers = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
	const requests: string[] = [];
	const bodies = new Map<string, unknown>();
	const records = new ShareLinkRecords(path.join(dataDir, "share-links.json"));
	const sent: unknown[] = [];
	const ranges: string[] = [];
	const authorizations: Array<string | null> = [];
	const sender = { id: 7, isDestroyed: () => false, send: vi.fn((channel: string, value: unknown) => channel === CLOUD_SHARE_PROGRESS_CHANNEL && sent.push(value)) };
	registerCloudShareIpc({
		ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => handlers.set(channel, handler)) },
		daemonDataDir: () => dataDir,
		snapshotDir: () => path.join(dataDir, "snapshots"),
		getToken: async () => (options.token === undefined ? "token-1" : options.token),
		records,
		fetch: vi.fn(async (url: string, init: RequestInit) => {
			const { pathname } = new URL(url);
			requests.push(`${init.method ?? "GET"} ${pathname}`);
			if (typeof init.body === "string") bodies.set(pathname, JSON.parse(init.body));
			const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
			if (pathname.endsWith("/me/artifact-shares")) {
				if (options.unreachable) throw new TypeError("fetch failed");
				authorizations.push(new Headers(init.headers).get("authorization"));
				if (options.listStatus) return json({ code: "not_found" }, options.listStatus);
				return json({ shares: [], limit: 3, used: 1 });
			}
			if (pathname.endsWith("/uploads")) return json({ uploadToken: "tok", partSize: 1024, partCount: 1 }, 201);
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
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/access`) return json({ filesPrefix: "/api/cloud/v1/artifact-files/tok/", expiresAt: "2026-11-01T00:00:00Z" });
			if (pathname === "/api/cloud/v1/artifact-files/tok/reports/q3%20plan.md") {
				ranges.push(new Headers(init.headers).get("range") ?? "");
				return new Response(`notes ${FAKE_AWS_KEY}`, { status: 206 });
			}
			if (pathname === `${CLOUD_SESSION_PATH}/artifacts/share`) {
				if (options.shareStatus) return json({ code: options.shareCode, message: "Refused." }, options.shareStatus);
				return json({ share: { id: SHARE_ID, slug: "abc", contentUrl: "https://abc.share.test/" }, replaced: false }, 201);
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
	return { invoke, publish, file, cloudFile, requests, bodies, records, sent, ranges, handlers, sender, authorizations };
}

async function snapshots(): Promise<string[]> {
	return fs.readdir(path.join(dataDir, "snapshots")).catch(() => []);
}

describe("cloudShare IPC", () => {
	it("inspects a file without uploading it", async () => {
		const { invoke, file, requests } = setup();
		await expect(invoke("cloudShare:inspect", file)).resolves.toEqual({
			name: "report.html", size: 20, secretFindings: ["AWS access key"], unsupported: false, fingerprint: expect.stringMatching(/^[0-9a-f]{64}:20:\d+$/),
		});
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

	it("names the document by project, session and folder", async () => {
		const { publish, bodies } = setup();
		await publish();
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/uploads")).toMatchObject({
			name: "report.html", size: 20, project: "proj-1", feature: "sess-1/docs",
		});
	});

	it("turns refusals into results the dialog can act on", async () => {
		const video = setup();
		await expect(video.invoke("cloudShare:publish", { ...video.file, path: "clip.mp4" })).resolves.toMatchObject({ status: "unsupported" });
		expect(video.requests).toEqual([]);

		const limit = setup({ finalizeStatus: 409, finalizeCode: "artifact_share_limit_reached" });
		await expect(limit.publish()).resolves.toEqual({
			status: "limit", message: "Free plan: up to 3 active links. Upgrade to share more.",
		});
		expect(limit.requests).toContain("POST /api/cloud/v1/me/artifact-shares/uploads/abort");

		const signedOut = setup({ token: null });
		await expect(signedOut.invoke("cloudShare:publish", signedOut.file)).resolves.toEqual({ status: "signed_out" });
		expect(signedOut.requests).toEqual([]);
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

	it("inspects a cloud session's file through its access link", async () => {
		const { invoke, cloudFile, requests, ranges } = setup();
		await expect(invoke("cloudShare:inspect", cloudFile)).resolves.toEqual({
			name: "q3 plan.md", size: 4096, secretFindings: ["AWS access key"], unsupported: false, fingerprint: CLOUD_SHA256,
		});
		// Only the start of the file is read, and nothing is uploaded.
		expect(ranges).toEqual([`bytes=0-${32 * 1024 * 1024 - 1}`]);
		expect(requests.filter((request) => request.includes("artifact-shares"))).toEqual([]);
	});

	it("publishes a cloud session's file without uploading it", async () => {
		const { invoke, cloudFile, requests, bodies, records } = setup();
		await expect(invoke("cloudShare:publish", { ...cloudFile, fingerprint: CLOUD_SHA256 })).resolves.toMatchObject({ status: "published", share: { id: SHARE_ID } });
		expect(requests).toEqual([`POST ${CLOUD_SESSION_PATH}/artifacts/share`]);
		// The control plane shares only the version the person was shown.
		expect(bodies.get(`${CLOUD_SESSION_PATH}/artifacts/share`)).toEqual({ path: "reports/q3 plan.md", sha256: CLOUD_SHA256 });
		// The link is remembered apart from any local session's file.
		await expect(invoke("cloudShare:inspect", cloudFile)).resolves.toMatchObject({ shareId: SHARE_ID });
		await expect(records.get(shareRecordKey("https://cp.test", CLOUD_SESSION_ID, "reports/q3 plan.md"))).resolves.toBeUndefined();

		await invoke("cloudShare:publish", { ...cloudFile, fingerprint: CLOUD_SHA256, shareId: SHARE_ID });
		expect(bodies.get(`${CLOUD_SESSION_PATH}/artifacts/share`)).toEqual({ path: "reports/q3 plan.md", sha256: CLOUD_SHA256, shareId: SHARE_ID });
	});

	it("turns a cloud file's refusals into results the popover can act on", async () => {
		const limit = setup({ shareStatus: 409, shareCode: "artifact_share_limit_reached" });
		await expect(limit.publish(limit.cloudFile)).resolves.toEqual({ status: "limit", message: "Refused." });

		// A file the session changed since it was shown is shown again.
		const changed = setup({ shareStatus: 409, shareCode: "artifact_share_source_changed" });
		await expect(changed.publish(changed.cloudFile)).resolves.toMatchObject({ status: "changed", inspection: { name: "q3 plan.md", fingerprint: CLOUD_SHA256 } });
		const unseen = setup();
		await expect(unseen.invoke("cloudShare:publish", unseen.cloudFile)).resolves.toMatchObject({ status: "changed" });
		expect(unseen.requests.filter((request) => request.endsWith("/share"))).toEqual([]);

		const video = setup();
		await expect(video.invoke("cloudShare:publish", { ...video.cloudFile, path: "demo.mp4" })).resolves.toMatchObject({ status: "unsupported" });
		expect(video.requests).toEqual([]);

		const signedOut = setup({ token: null });
		await expect(signedOut.invoke("cloudShare:publish", signedOut.cloudFile)).resolves.toEqual({ status: "signed_out" });

		const malformed = setup();
		await expect(malformed.invoke("cloudShare:publish", { ...malformed.cloudFile, orgId: "../x", fingerprint: CLOUD_SHA256 })).resolves.toEqual({ status: "error", message: "Not a cloud session's artifact." });
		await expect(malformed.invoke("cloudShare:publish", { ...malformed.cloudFile, orgId: undefined })).rejects.toThrow("Not a file to share");
		await expect(malformed.invoke("cloudShare:inspect", { ...malformed.cloudFile, path: "missing.md" })).rejects.toThrow("no longer available");
	});

	it("publishes nothing when the file changed since it was inspected", async () => {
		const { invoke, file, requests } = setup();
		const shown = (await invoke("cloudShare:inspect", file)) as { fingerprint: string };
		await fs.writeFile(path.join(dataDir, "artifacts", "sess-1", "docs", "report.html"), `now with ${FAKE_AWS_KEY} and more`);
		const result = await invoke("cloudShare:publish", { ...file, fingerprint: shown.fingerprint });
		expect(result).toMatchObject({ status: "changed", inspection: { name: "report.html", size: 38, secretFindings: ["AWS access key"] } });
		expect((result as { inspection: { fingerprint: string } }).inspection.fingerprint).not.toBe(shown.fingerprint);
		expect(requests).toEqual([]);
		expect(await snapshots()).toEqual([]);
		// A publish with no inspection at all is refused the same way.
		await expect(invoke("cloudShare:publish", file)).resolves.toMatchObject({ status: "changed" });
		expect(requests).toEqual([]);
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
