import { afterEach, describe, expect, it, vi } from "vitest";
import { getArtifactSize } from "./api";
import type { ServerConfig } from "./config";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

const cfg = { host: "10.0.0.2", httpPort: "3000", muxPort: "", password: "secret", secure: false } as ServerConfig;

function respond(status: number, headers: Record<string, string> = {}, body = "") {
	const fetchMock = vi.fn(async () => new Response(status === 204 ? null : body, { status, headers }));
	vi.stubGlobal("fetch", fetchMock);
	return fetchMock;
}

afterEach(() => vi.unstubAllGlobals());

describe("getArtifactSize", () => {
	it("reads one byte and takes the size from the range response", async () => {
		const fetchMock = respond(206, { "Content-Range": "bytes 0-0/4182" }, "r");
		await expect(getArtifactSize(cfg, "ao-1", "report/data.csv")).resolves.toBe(4182);
		const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://10.0.0.2:3000/api/v1/sessions/ao-1/preview/files/__ao_artifacts__/report/data.csv?raw=true");
		expect((init.headers as Record<string, string>).Range).toBe("bytes=0-0");
	});

	it("takes the length of a server that sends the whole file", async () => {
		respond(200, { "Content-Length": "12" }, "run,model\n1,");
		await expect(getArtifactSize(cfg, "ao-1", "data.csv")).resolves.toBe(12);
	});

	it("reads an empty file, which the daemon sends whole, as 0 bytes", async () => {
		respond(200, { "Content-Length": "0" });
		await expect(getArtifactSize(cfg, "ao-1", "empty.csv")).resolves.toBe(0);
	});

	it("does not read a whole reply without a length as empty", async () => {
		respond(200, {}, "run,model\n1,alpha\n");
		await expect(getArtifactSize(cfg, "ao-1", "data.csv")).resolves.toBeUndefined();
	});

	it("reads a refused range on an empty file as 0 bytes", async () => {
		respond(416, { "Content-Type": "text/plain" }, "invalid range");
		await expect(getArtifactSize(cfg, "ao-1", "empty.csv")).resolves.toBe(0);
	});

	it("passes on other failures, so the viewer can fall back", async () => {
		respond(404, { "Content-Type": "application/json" }, JSON.stringify({ message: "not found" }));
		await expect(getArtifactSize(cfg, "ao-1", "gone.csv")).rejects.toThrow();
	});
});
