import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { uploadMock } = vi.hoisted(() => ({ uploadMock: vi.fn() }));
vi.mock("./cloud-cp/renderer-client", () => ({ cloudCpUpload: uploadMock }));
vi.mock("./artifact-content-url", () => ({ artifactContentUrl: (_id: string, path: string) => `http://127.0.0.1:3001/files/${path}` }));

import { ArtifactShareAuthError, ArtifactShareLimitError, ArtifactShareUnavailableError, publishArtifactShare } from "./artifact-share";

const input = {
	baseUrl: "http://127.0.0.1:8081",
	userId: "user-1",
	sessionId: "sess-1",
	path: "img/chart.png",
	name: "chart.png",
	rawUrl: "http://ao-preview-artifact.x.localhost:3001/img/chart.png?raw=true",
	project: "agent-orchestrator",
	feature: "Login page",
};
const bytes = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0xff]);

function jsonResponse(status: number, body: unknown) {
	return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("publishArtifactShare", () => {
	beforeEach(() => {
		uploadMock.mockReset();
		window.localStorage.clear();
		vi.stubGlobal("fetch", vi.fn(async () => new Response(bytes, { status: 200 })));
	});
	afterEach(() => vi.unstubAllGlobals());

	it("uploads the file's exact bytes with their SHA-256 and the link name parts", async () => {
		uploadMock.mockResolvedValue(jsonResponse(201, { share: { id: "s1", url: "http://l/", name: "chart.png" }, replaced: false }));
		const link = await publishArtifactShare(input, "failed");
		expect(link).toEqual({ id: "s1", url: "http://l/", name: "chart.png", replaced: false });
		expect(fetch).toHaveBeenCalledWith("http://127.0.0.1:3001/files/img/chart.png");
		const [baseUrl, path, method, sent, headers] = uploadMock.mock.calls[0]!;
		expect(baseUrl).toBe("http://127.0.0.1:8081");
		expect(method).toBe("POST");
		const query = new URL(path, "http://x").searchParams;
		expect(query.get("name")).toBe("chart.png");
		expect(query.get("project")).toBe("agent-orchestrator");
		expect(query.get("feature")).toBe("Login page");
		expect(Array.from(sent as Uint8Array)).toEqual(Array.from(bytes));
		const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
		expect(headers["X-AO-Artifact-SHA256"]).toBe(Array.from(digest, (b) => b.toString(16).padStart(2, "0")).join(""));
	});

	it("reports a replaced link", async () => {
		uploadMock.mockResolvedValue(jsonResponse(200, { share: { id: "s1", url: "http://l/", name: "chart.png" }, replaced: true }));
		expect((await publishArtifactShare(input, "failed")).replaced).toBe(true);
	});

	it("turns the limit refusal and a signed-out answer into typed errors", async () => {
		uploadMock.mockResolvedValueOnce(jsonResponse(409, { code: "artifact_share_limit_reached", message: "You can have up to 3 shared artifact links." }));
		await expect(publishArtifactShare(input, "failed")).rejects.toBeInstanceOf(ArtifactShareLimitError);
		uploadMock.mockResolvedValueOnce(new Response(null, { status: 401 }));
		await expect(publishArtifactShare(input, "failed")).rejects.toBeInstanceOf(ArtifactShareAuthError);
	});

	it("reports a server without share links, which answers a plain-text 404", async () => {
		uploadMock.mockResolvedValueOnce(new Response("404 page not found", { status: 404, headers: { "content-type": "text/plain" } }));
		await expect(publishArtifactShare(input, "failed")).rejects.toBeInstanceOf(ArtifactShareUnavailableError);
	});

	it("uses the server's message, or the fallback, for any other failure", async () => {
		uploadMock.mockResolvedValueOnce(jsonResponse(502, { code: "artifact_store_unavailable", message: "The artifact could not be stored." }));
		await expect(publishArtifactShare(input, "failed")).rejects.toThrow("The artifact could not be stored.");
		vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
		await expect(publishArtifactShare(input, "failed")).rejects.toThrow("failed");
		expect(uploadMock).toHaveBeenCalledTimes(1);
	});

	it("updates the remembered link with PUT when the same file is shared again, even after a rename", async () => {
		uploadMock.mockResolvedValueOnce(jsonResponse(201, { share: { id: "s1", url: "http://l/", name: "chart.png" }, replaced: false }));
		await publishArtifactShare(input, "failed");
		uploadMock.mockResolvedValueOnce(jsonResponse(200, { share: { id: "s1", url: "http://l/", name: "chart.png" } }));
		const link = await publishArtifactShare({ ...input, feature: "Renamed session" }, "failed");

		expect(link).toEqual({ id: "s1", url: "http://l/", name: "chart.png", replaced: true });
		const [, path, method] = uploadMock.mock.calls[1]!;
		expect(method).toBe("PUT");
		expect(path).toBe("/api/cloud/v1/me/artifact-shares/s1?name=chart.png");
	});

	it("publishes by name again when the remembered link is gone or names another file", async () => {
		for (const stale of [
			jsonResponse(404, { code: "not_found", message: "The requested resource was not found." }),
			jsonResponse(409, { code: "artifact_share_name_changed", message: "A share link keeps its file name." }),
		]) {
			window.localStorage.clear();
			uploadMock.mockReset();
			uploadMock.mockResolvedValueOnce(jsonResponse(201, { share: { id: "old", url: "http://old/", name: "chart.png" } }));
			await publishArtifactShare(input, "failed");
			uploadMock.mockResolvedValueOnce(stale);
			uploadMock.mockResolvedValueOnce(jsonResponse(201, { share: { id: "new", url: "http://new/", name: "chart.png" } }));
			const link = await publishArtifactShare(input, "failed");
			expect(link.id).toBe("new");
			expect(uploadMock.mock.calls.map((call) => call[2])).toEqual(["POST", "PUT", "POST"]);
			// The new link is remembered for next time.
			uploadMock.mockResolvedValueOnce(jsonResponse(200, { share: { id: "new", url: "http://new/", name: "chart.png" } }));
			await publishArtifactShare(input, "failed");
			expect(uploadMock.mock.calls[3]![1]).toContain("/artifact-shares/new?");
		}
	});

	it("keeps links separate per user, session and file", async () => {
		uploadMock.mockImplementation(async () => jsonResponse(201, { share: { id: "s1", url: "http://l/", name: "chart.png" } }));
		await publishArtifactShare(input, "failed");
		for (const other of [{ userId: "user-2" }, { sessionId: "sess-2" }, { path: "img/other.png", name: "other.png" }]) {
			await publishArtifactShare({ ...input, ...other }, "failed");
		}
		expect(uploadMock.mock.calls.map((call) => call[2])).toEqual(["POST", "POST", "POST", "POST"]);
	});

	it("reports a server without share links even when a link was remembered", async () => {
		uploadMock.mockResolvedValueOnce(jsonResponse(201, { share: { id: "s1", url: "http://l/", name: "chart.png" } }));
		await publishArtifactShare(input, "failed");
		uploadMock.mockResolvedValueOnce(new Response("404 page not found", { status: 404, headers: { "content-type": "text/plain" } }));
		await expect(publishArtifactShare(input, "failed")).rejects.toBeInstanceOf(ArtifactShareUnavailableError);
		expect(uploadMock).toHaveBeenCalledTimes(2);
	});
});
