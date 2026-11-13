// @vitest-environment node
import { createHash } from "node:crypto";
import { openSync, closeSync, writeSync, promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const token = { value: "token-1" as string | null };
vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => token.value) }));

import { createCloudCpProxy } from "./cloud-cp-proxy";
import {
	CloudShareError,
	resolveShareFile,
	shareFileFingerprint,
	snapshotShareFile,
	statShareFile,
	listShareLinks,
	SHARE_TIMEOUTS,
	shareLinksAvailable,
	uploadShare,
	type CloudShareApi,
} from "./cloud-share";

const sha = (value: Uint8Array | string) => createHash("sha256").update(value).digest("hex");

let dataDir: string;
let artifacts: string;

beforeEach(async () => {
	dataDir = await fs.mkdtemp(path.join(os.tmpdir(), "ao-share-"));
	artifacts = path.join(dataDir, "artifacts", "sess-1");
	await fs.mkdir(path.join(artifacts, "docs"), { recursive: true });
	await fs.mkdir(path.join(artifacts, ".versions", "blobs"), { recursive: true });
	await fs.writeFile(path.join(artifacts, "docs", "report.html"), "<h1>report</h1>");
	await fs.writeFile(path.join(artifacts, ".versions", "blobs", "old"), "old");
	await fs.writeFile(path.join(dataDir, "secret.txt"), "outside");
	token.value = "token-1";
});

afterEach(async () => {
	await fs.rm(dataDir, { recursive: true, force: true });
});

describe("resolveShareFile", () => {
	it("resolves a file inside the session's artifacts folder", async () => {
		const file = await resolveShareFile(dataDir, "sess-1", "docs/report.html");
		expect(file).toMatchObject({ name: "report.html", relativePath: "docs/report.html", size: 15 });
		expect(file.realPath).toBe(await fs.realpath(path.join(artifacts, "docs", "report.html")));
	});

	it("refuses anything outside it, the version history and non-files", async () => {
		await fs.symlink(path.join(dataDir, "secret.txt"), path.join(artifacts, "link.txt"));
		await fs.symlink(dataDir, path.join(artifacts, "up"));
		for (const [sessionId, relPath] of [
			["sess-1", "../../secret.txt"],
			["sess-1", path.join(dataDir, "secret.txt")],
			["sess-1", "link.txt"],
			["sess-1", "up/secret.txt"],
			["sess-1", ".versions/blobs/old"],
			["sess-1", "docs"],
			["sess-1", ""],
			["sess-1", "missing.html"],
			["..", "sess-1/docs/report.html"],
			["sess/1", "docs/report.html"],
			[42, "docs/report.html"],
		] as const) {
			await expect(resolveShareFile(dataDir, sessionId, relPath), `${String(sessionId)} ${relPath}`).rejects.toBeInstanceOf(CloudShareError);
		}
	});
});

describe("share file fingerprint", () => {
	it("names a file by its size and modification time, without reading it", async () => {
		await fs.writeFile(path.join(artifacts, "config.yaml"), "token: one");
		const file = await resolveShareFile(dataDir, "sess-1", "config.yaml");
		const read = vi.spyOn(fs, "readFile");
		const fingerprint = await statShareFile(file);
		const stat = await fs.stat(file.realPath);
		expect(fingerprint).toBe(shareFileFingerprint(stat));
		expect(fingerprint).toBe(`${stat.size}:${Math.trunc(stat.mtimeMs)}`);
		expect(read).not.toHaveBeenCalled();
		read.mockRestore();
		// The snapshot of the same file has it; a rewritten file does not.
		const snapshot = await snapshotShareFile(file, path.join(dataDir, "snapshots"));
		expect(snapshot.fingerprint).toBe(fingerprint);
		const later = new Date(Date.now() + 60_000);
		await fs.writeFile(path.join(artifacts, "config.yaml"), "token: two, longer");
		await fs.utimes(path.join(artifacts, "config.yaml"), later, later);
		expect(await statShareFile(await resolveShareFile(dataDir, "sess-1", "config.yaml"))).not.toBe(fingerprint);
	});
});

describe("snapshotShareFile", () => {
	it("copies the file privately, hashing it", async () => {
		const file = await resolveShareFile(dataDir, "sess-1", "docs/report.html");
		const snapshot = await snapshotShareFile(file, path.join(dataDir, "snapshots"));
		expect(await fs.readFile(snapshot.path, "utf8")).toBe("<h1>report</h1>");
		expect(snapshot).toMatchObject({ size: 15, sha256: sha("<h1>report</h1>") });
		expect((await fs.stat(snapshot.path)).mode & 0o777).toBe(0o600);
	});

	it("refuses a file swapped for a symlink after it resolved", async () => {
		const file = await resolveShareFile(dataDir, "sess-1", "docs/report.html");
		await fs.rm(file.realPath);
		await fs.symlink(path.join(dataDir, "secret.txt"), file.realPath);
		await expect(snapshotShareFile(file, path.join(dataDir, "snapshots"))).rejects.toThrow();
		expect(await fs.readdir(path.join(dataDir, "snapshots")).catch(() => [])).toEqual([]);
	});

	it("refuses a folder on the way swapped for a symlink after the file resolved", async () => {
		const outside = path.join(dataDir, "outside");
		await fs.mkdir(outside);
		await fs.writeFile(path.join(outside, "report.html"), "private");
		const file = await resolveShareFile(dataDir, "sess-1", "docs/report.html");
		await fs.rename(path.join(artifacts, "docs"), path.join(artifacts, "docs-old"));
		await fs.symlink(outside, path.join(artifacts, "docs"));
		await expect(snapshotShareFile(file, path.join(dataDir, "snapshots"))).rejects.toThrow("changed");
		await expect(statShareFile(file)).rejects.toThrow("changed");
		expect(await fs.readdir(path.join(dataDir, "snapshots")).catch(() => [])).toEqual([]);
	});

	it("refuses a file rewritten while it is copied, though its size stays the same", async () => {
		// 2 MiB, last written long ago: a write during the copy moves its times.
		const big = path.join(artifacts, "big.bin");
		await fs.writeFile(big, Buffer.alloc(2 << 20, 1));
		const past = new Date("2020-01-01T00:00:00Z");
		await fs.utimes(big, past, past);
		const file = await resolveShareFile(dataDir, "sess-1", "big.bin");
		const inspected = await statShareFile(file);
		let rewritten = false;
		const snapshot = snapshotShareFile(file, path.join(dataDir, "snapshots"), {
			// After the first MiB is copied, an agent rewrites the second one.
			onProgress: () => {
				if (rewritten) return;
				rewritten = true;
				const handle = openSync(big, "r+");
				writeSync(handle, Buffer.alloc(1 << 20, 2), 0, 1 << 20, 1 << 20);
				closeSync(handle);
			},
		});
		await expect(snapshot).rejects.toMatchObject({ code: "source_changed" });
		expect(rewritten).toBe(true);
		expect(await fs.readdir(path.join(dataDir, "snapshots"))).toEqual([]);
		// Left alone, the same file copies, and names its new version.
		const again = await snapshotShareFile(await resolveShareFile(dataDir, "sess-1", "big.bin"), path.join(dataDir, "snapshots"));
		expect(again.fingerprint).not.toBe(inspected);
		expect(again.size).toBe(2 << 20);
	});

	it("reports progress and stops when cancelled", async () => {
		await fs.writeFile(path.join(artifacts, "big.bin"), Buffer.alloc(3 << 20, 1));
		const file = await resolveShareFile(dataDir, "sess-1", "big.bin");
		const copied: number[] = [];
		await snapshotShareFile(file, path.join(dataDir, "snapshots"), { onProgress: (bytes) => copied.push(bytes) });
		expect(copied).toEqual([1 << 20, 2 << 20, 3 << 20]);

		const controller = new AbortController();
		const stopped = snapshotShareFile(file, path.join(dataDir, "cancelled"), {
			signal: controller.signal,
			onProgress: () => controller.abort(),
		});
		await expect(stopped).rejects.toMatchObject({ code: "cancelled" });
		expect(await fs.readdir(path.join(dataDir, "cancelled"))).toEqual([]);
	});
});

interface Call {
	url: string;
	method: string;
	headers: Record<string, string>;
	body?: string | Uint8Array;
}

function fakeControlPlane(partSize: number, options: { failPart?: number; finalizeError?: { status: number; code: string } } = {}) {
	const calls: Call[] = [];
	const stored = new Map<number, Uint8Array>();
	const api: CloudShareApi = {
		getToken: async () => token.value,
		fetch: vi.fn(async (url: string, init: RequestInit) => {
			const headers = Object.fromEntries(new Headers(init.headers).entries());
			const body = init.body as string | Uint8Array | undefined;
			calls.push({ url, method: init.method ?? "GET", headers, body });
			const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
			const { pathname } = new URL(url);
			if (pathname.endsWith("/me/artifact-shares/uploads")) {
				const { size } = JSON.parse(body as string) as { size: number };
				return json({ uploadToken: "tok", partSize, partCount: Math.max(1, Math.ceil(size / partSize)) }, 201);
			}
			if (pathname.endsWith("/uploads/parts")) {
				const { parts } = JSON.parse(body as string) as { parts: Array<{ partNumber: number }> };
				return json({ parts: parts.map(({ partNumber }) => ({ partNumber, method: "PUT", url: `https://s3.test/part/${partNumber}`, headers: { "Content-Length": "4", Host: "s3.test", "X-Amz-Checksum-Sha256": "sig" }, size: 0 })) });
			}
			if (pathname.startsWith("/part/")) {
				const number = Number(pathname.split("/").pop());
				if (number === options.failPart) return new Response("", { status: 400 });
				stored.set(number, new Uint8Array(body as Uint8Array));
				return new Response("", { headers: { etag: `"etag-${number}"` } });
			}
			if (pathname.endsWith("/finalize")) {
				if (options.finalizeError) return json({ code: options.finalizeError.code, message: "refused" }, options.finalizeError.status);
				return json({ share: { id: "s-1", slug: "abc" }, replaced: false }, 201);
			}
			if (pathname.endsWith("/uploads/abort")) return new Response(null, { status: 204 });
			if (pathname.endsWith("/me/artifact-shares")) return json({ shares: [], limit: 3, used: 1 });
			return new Response("", { status: 404 });
		}),
	};
	return { api, calls, stored };
}

async function snapshotOf(content: string) {
	await fs.writeFile(path.join(artifacts, "upload.txt"), content);
	const file = await resolveShareFile(dataDir, "sess-1", "upload.txt");
	return snapshotShareFile(file, path.join(dataDir, "snapshots"));
}

// A connection that dropped answers nothing: the request only ends when its
// signal aborts.
function silentControlPlane(): CloudShareApi {
	return {
		getToken: async () => "token-1",
		fetch: vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
			const fail = () => reject(init.signal?.reason ?? new DOMException("Aborted", "AbortError"));
			// Like fetch, a request cancelled before it starts fails at once.
			if (init.signal?.aborted) return fail();
			init.signal?.addEventListener("abort", fail, { once: true });
		})),
	};
}

describe("share request time limits", () => {
	const saved = { ...SHARE_TIMEOUTS };
	beforeEach(() => Object.assign(SHARE_TIMEOUTS, { api: 30, probe: 30, transfer: 30, publish: 30 }));
	afterEach(() => Object.assign(SHARE_TIMEOUTS, saved));

	it("fails a request that never answers instead of waiting for good", async () => {
		await expect(listShareLinks(silentControlPlane(), "https://cp.test")).rejects.toMatchObject({
			code: "timeout",
			message: "Orchestrator Cloud didn't answer in time. Check your connection and try again.",
		});
		await expect(shareLinksAvailable(silentControlPlane(), "https://cp.test")).resolves.toBeNull();
	});

	it("counts a token refresh that stalls against the request's limit", async () => {
		const fetch = vi.fn();
		const stalledToken: CloudShareApi = { getToken: () => new Promise<string | null>(() => undefined), fetch };
		await expect(listShareLinks(stalledToken, "https://cp.test")).rejects.toMatchObject({ code: "timeout" });
		await expect(shareLinksAvailable(stalledToken, "https://cp.test")).resolves.toBeNull();
		expect(fetch).not.toHaveBeenCalled();
	});

	it("keeps a cancel a cancel, not a timeout", async () => {
		const snapshot = await snapshotOf("0123456789");
		Object.assign(SHARE_TIMEOUTS, { api: 60_000 });
		const controller = new AbortController();
		const upload = uploadShare(silentControlPlane(), { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "sess-1" }, () => undefined, controller.signal);
		controller.abort();
		const error = await upload.catch((caught: unknown) => caught);
		expect(error).not.toBeInstanceOf(CloudShareError);
	});

	it("times out an upload part that stops answering", async () => {
		const snapshot = await snapshotOf("0123456789");
		const { api } = fakeControlPlane(4);
		const answer = api.fetch;
		api.fetch = vi.fn((url: string, init: RequestInit) => new URL(url).pathname.startsWith("/part/")
			? silentControlPlane().fetch(url, init)
			: answer(url, init));
		await expect(uploadShare(api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "sess-1" }, () => undefined))
			.rejects.toMatchObject({ code: "timeout" });
	});
});

describe("uploadShare", () => {
	it("uploads the snapshot in signed parts and finalizes it", async () => {
		const snapshot = await snapshotOf("0123456789");
		const { api, calls, stored } = fakeControlPlane(4);
		const progress: number[] = [];
		const result = await uploadShare(api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "sess-1" }, (sent) => progress.push(sent));
		expect(result.share.id).toBe("s-1");
		// Parts upload concurrently, so they can arrive in any order.
		expect([...stored.entries()].sort(([a], [b]) => a - b).map(([number, body]) => [number, Buffer.from(body).toString()])).toEqual([[1, "0123"], [2, "4567"], [3, "89"]]);
		expect(progress.at(-1)).toBe(10);

		const start = JSON.parse(calls[0].body as string);
		expect(start).toEqual({ name: "upload.txt", size: 10, sha256: sha("0123456789"), project: "p", feature: "sess-1" });
		const presign = JSON.parse(calls.find((call) => call.url.endsWith("/uploads/parts"))!.body as string);
		expect(presign.parts).toEqual([{ partNumber: 1, sha256: sha("0123") }, { partNumber: 2, sha256: sha("4567") }, { partNumber: 3, sha256: sha("89") }]);
		const finalize = JSON.parse(calls.find((call) => call.url.endsWith("/finalize"))!.body as string);
		expect(finalize.parts.map((part: { partNumber: number; etag: string }) => [part.partNumber, part.etag])).toEqual([[1, '"etag-1"'], [2, '"etag-2"'], [3, '"etag-3"']]);

		// Chromium sets Content-Length and Host itself and refuses a request
		// that names them; the other signed headers go with each part.
		for (const call of calls.filter((candidate) => candidate.url.startsWith("https://s3.test/"))) {
			expect(call.headers).toEqual({ "x-amz-checksum-sha256": "sig" });
		}

		// The token goes only to the control plane, never to storage.
		for (const call of calls) {
			expect(call.headers.authorization === "Bearer token-1").toBe(call.url.startsWith("https://cp.test/api/cloud/v1/"));
		}
	});

	it("updates a link by its ID, naming where the file is now", async () => {
		const snapshot = await snapshotOf("x");
		const { api, calls } = fakeControlPlane(4);
		await uploadShare(api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "f", shareId: "share-9" }, () => undefined);
		expect(JSON.parse(calls[0].body as string)).toEqual({ name: "upload.txt", size: 1, sha256: sha("x"), project: "p", feature: "f", shareId: "share-9" });
	});

	it("aborts the upload when a part fails or finalize refuses", async () => {
		const snapshot = await snapshotOf("0123456789");
		const failing = fakeControlPlane(4, { failPart: 2 });
		await expect(uploadShare(failing.api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "f" }, () => undefined)).rejects.toThrow("part 2");
		expect(failing.calls.some((call) => call.url.endsWith("/uploads/abort"))).toBe(true);

		const refused = fakeControlPlane(4, { finalizeError: { status: 409, code: "artifact_share_limit_reached" } });
		await expect(uploadShare(refused.api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "f" }, () => undefined))
			.rejects.toMatchObject({ code: "artifact_share_limit_reached", status: 409 });
	});

	it("stops every part once one fails, before the upload is aborted", async () => {
		const snapshot = await snapshotOf("0123456789abcdefghij");
		const failing = fakeControlPlane(4, { failPart: 1 });
		await expect(uploadShare(failing.api, { baseUrl: "https://cp.test", snapshot, name: "upload.txt", project: "p", feature: "f" }, () => undefined)).rejects.toThrow("part 1");
		const abort = failing.calls.findIndex((call) => call.url.endsWith("/uploads/abort"));
		expect(abort).toBeGreaterThan(0);
		expect(failing.calls.slice(abort).filter((call) => call.url.startsWith("https://s3.test/"))).toEqual([]);
		// Not every part was sent: the second worker stopped.
		expect(failing.calls.filter((call) => call.url.startsWith("https://s3.test/")).length).toBeLessThan(5);
	});

	it("needs a signed-in user and a safe control plane URL", async () => {
		const { api } = fakeControlPlane(4);
		token.value = null;
		await expect(listShareLinks(api, "https://cp.test")).rejects.toMatchObject({ code: "signed_out" });
		token.value = "token-1";
		await expect(listShareLinks(api, "http://evil.test")).rejects.toThrow("https");
		await expect(listShareLinks(api, "https://cp.test")).resolves.toMatchObject({ limit: 3, used: 1, unlimited: false });
	});
});

describe("cloud control-plane proxy", () => {
	it("lets the renderer read share links but never change them", async () => {
		const fetchImpl = vi.fn(async () => new Response("{}", { status: 200 }));
		const proxy = createCloudCpProxy(() => dataDir, { controlPlaneUrl: async () => "https://cp.test", fetchImpl });
		const request = (method: string, path: string) => proxy.request({ baseUrl: "https://cp.test", path, method });
		await expect(request("GET", "/api/cloud/v1/me/artifact-shares")).resolves.toMatchObject({ status: 200 });
		for (const [method, path] of [
			["POST", "/api/cloud/v1/me/artifact-shares/uploads"],
			["POST", "/api/cloud/v1/me/artifact-shares/finalize"],
			["DELETE", "/api/cloud/v1/me/artifact-shares/00000000-0000-4000-8000-000000000000"],
			["POST", "/api/cloud/v1/me/artifact-shares"],
			["post", "/api/cloud/v1/me/./artifact-shares/uploads"],
			["POST", "/api/cloud/v1/orgs/../me/artifact-shares/finalize"],
			// A cloud session's file is shared only through the main process too.
			["POST", "/api/cloud/v1/orgs/a1a1a1a1-0000-4000-8000-000000000001/sessions/a1a1a1a1-0000-4000-8000-0000000000a1/artifacts/share"],
			["POST", "/api/cloud/v1/orgs/o/sessions/s/artifacts%2Fshare"],
		]) {
			await expect(request(method, path), `${method} ${path}`).rejects.toThrow("share links can only be changed");
		}
		// Reading a session's files is still allowed.
		await expect(request("GET", "/api/cloud/v1/orgs/o/sessions/s/artifacts")).resolves.toMatchObject({ status: 200 });
		expect(fetchImpl).toHaveBeenCalledTimes(2);
	});
});
