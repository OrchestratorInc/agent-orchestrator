// @vitest-environment node
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import { CLOUD_SHARE_PROGRESS_CHANNEL } from "../shared/cloud-share";
import { type CloudShareConfirmation, confirmationText, formatShareSize, registerCloudShareIpc } from "./cloud-share-ipc";

// Built at runtime so the repository's secret scanner does not flag the fixture.
const FAKE_AWS_KEY = ["AKIA", "ABCDEFGHIJKLMNOP"].join("");

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

function setup(options: { confirm?: boolean; token?: string | null; finalizeStatus?: number; finalizeCode?: string } = {}) {
	const handlers = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
	const requests: string[] = [];
	const bodies = new Map<string, unknown>();
	const confirmations: CloudShareConfirmation[] = [];
	const sent: unknown[] = [];
	const sender = { id: 7, isDestroyed: () => false, send: vi.fn((channel: string, value: unknown) => channel === CLOUD_SHARE_PROGRESS_CHANNEL && sent.push(value)) };
	registerCloudShareIpc({
		ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => handlers.set(channel, handler)) },
		daemonDataDir: () => dataDir,
		snapshotDir: () => path.join(dataDir, "snapshots"),
		getToken: async () => (options.token === undefined ? "token-1" : options.token),
		confirm: vi.fn(async (_sender, confirmation: CloudShareConfirmation) => {
			confirmations.push(confirmation);
			return options.confirm ?? true;
		}),
		fetch: vi.fn(async (url: string, init: RequestInit) => {
			const { pathname } = new URL(url);
			requests.push(`${init.method ?? "GET"} ${pathname}`);
			if (typeof init.body === "string") bodies.set(pathname, JSON.parse(init.body));
			const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
			if (pathname.endsWith("/me/artifact-shares")) return json({ shares: [], limit: 3, used: 1 });
			if (pathname.endsWith("/uploads")) return json({ uploadToken: "tok", partSize: 1024, partCount: 1 }, 201);
			if (pathname.endsWith("/uploads/parts")) return json({ parts: [{ partNumber: 1, method: "PUT", url: "https://s3.test/p", headers: {}, size: 20 }] });
			if (pathname === "/p") return new Response("", { headers: { etag: '"e1"' } });
			if (pathname.endsWith("/finalize")) {
				if (options.finalizeStatus) return json({ code: options.finalizeCode, message: "Free plan: up to 3 active links. Upgrade to share more." }, options.finalizeStatus);
				return json({ share: { id: "s-1", linkId: "abc", contentUrl: "https://abc.share.test/" }, replaced: false }, 201);
			}
			if (pathname.endsWith("/uploads/abort")) return new Response(null, { status: 204 });
			return new Response("", { status: 404 });
		}),
	});
	const invoke = (channel: string, value?: unknown) => handlers.get(channel)!({ sender }, value);
	const file = { baseUrl: "https://cp.test", sessionId: "sess-1", path: "docs/report.html", projectId: "proj-1" };
	return { invoke, file, requests, bodies, confirmations, sent };
}

async function snapshots(): Promise<string[]> {
	return fs.readdir(path.join(dataDir, "snapshots")).catch(() => []);
}

describe("cloudShare IPC", () => {
	it("inspects a file without uploading it", async () => {
		const { invoke, file, requests } = setup();
		await expect(invoke("cloudShare:inspect", file)).resolves.toEqual({
			name: "report.html", size: 20, secretFindings: ["AWS access key"], unsupported: false,
		});
		expect(requests).toEqual([]);
	});

	it("publishes only after the person confirms", async () => {
		const declined = setup({ confirm: false });
		await expect(declined.invoke("cloudShare:publish", declined.file)).resolves.toEqual({ status: "cancelled" });
		expect(declined.requests).toEqual(["GET /api/cloud/v1/me/artifact-shares"]);
		expect(await snapshots()).toEqual([]);

		const { invoke, file, requests, confirmations, sent } = setup();
		const result = await invoke("cloudShare:publish", file);
		expect(result).toMatchObject({ status: "published", share: { id: "s-1" }, replaced: false });
		expect(confirmations[0]).toEqual({ name: "report.html", size: 20, secretFindings: ["AWS access key"], usage: { used: 1, limit: 3 }, update: false });
		expect(requests).toContain("POST /api/cloud/v1/me/artifact-shares/finalize");
		expect(sent).toEqual([{ sessionId: "sess-1", path: "docs/report.html", sentBytes: 20, totalBytes: 20 }]);
		expect(await snapshots()).toEqual([]);
	});

	it("names the document by project, session and folder", async () => {
		const { invoke, file, bodies } = setup();
		await invoke("cloudShare:publish", file);
		expect(bodies.get("/api/cloud/v1/me/artifact-shares/uploads")).toMatchObject({
			name: "report.html", size: 20, project: "proj-1", feature: "sess-1/docs",
		});
	});

	it("turns refusals into results the dialog can act on", async () => {
		const video = setup();
		await expect(video.invoke("cloudShare:publish", { ...video.file, path: "clip.mp4" })).resolves.toMatchObject({ status: "unsupported" });
		expect(video.confirmations).toEqual([]);

		const limit = setup({ finalizeStatus: 409, finalizeCode: "artifact_share_limit_reached" });
		await expect(limit.invoke("cloudShare:publish", limit.file)).resolves.toEqual({
			status: "limit", message: "Free plan: up to 3 active links. Upgrade to share more.",
		});
		expect(limit.requests).toContain("POST /api/cloud/v1/me/artifact-shares/uploads/abort");

		const signedOut = setup({ token: null });
		await expect(signedOut.invoke("cloudShare:publish", signedOut.file)).resolves.toEqual({ status: "signed_out" });
		expect(signedOut.confirmations).toEqual([]);
	});

	it("refuses malformed requests and files outside the artifacts folder", async () => {
		const { invoke, file } = setup();
		await expect(invoke("cloudShare:publish", { ...file, path: "../../etc/passwd" })).rejects.toThrow();
		await expect(invoke("cloudShare:publish", { ...file, projectId: 5 })).rejects.toThrow("Not a file to share");
		await expect(invoke("cloudShare:list", {})).rejects.toThrow("Not a control plane");
		await expect(invoke("cloudShare:unpublish", { baseUrl: "https://cp.test", shareId: "../x" })).rejects.toThrow("Not a share link");
	});
});

describe("confirmation text", () => {
	it("says the link is public, the plan usage and any credential found", () => {
		const text = confirmationText({ name: "report.html", size: 2048, secretFindings: ["AWS access key"], usage: { used: 1, limit: 3 }, update: false });
		expect(text.message).toBe("Publish this file as a public link?");
		expect(text.button).toBe("Publish");
		expect(text.detail).toContain("report.html · 2.0 KB");
		expect(text.detail).toContain("Anyone with the link can view this file.");
		expect(text.detail).toContain("Free plan: 2 of 3 active links.");
		expect(text.detail).toContain("looks like it contains a AWS access key");
		const update = confirmationText({ name: "a.md", size: 3, secretFindings: [], update: true });
		expect(update.button).toBe("Update link");
		expect(update.detail).not.toContain("Free plan");
		expect(formatShareSize(5 * 1024 ** 3)).toBe("5.0 GB");
	});
});
