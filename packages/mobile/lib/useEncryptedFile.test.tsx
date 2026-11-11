import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hostFetch = vi.fn();
vi.mock("./config", () => ({ authHeaders: () => ({ Authorization: "Bearer aod_x" }) }));
vi.mock("./e2ee/host", () => ({
	hostFetch: (...args: unknown[]) => hostFetch(...args),
	hostPath: (url: string) => new URL(url).pathname,
}));

// An in-memory stand-in for the app's cache folder.
const { disk } = vi.hoisted(() => ({ disk: new Map<string, Uint8Array>() }));
vi.mock("expo-file-system", () => {
	const join = (...parts: (string | { uri: string })[]) => parts.map((part) => typeof part === "string" ? part : part.uri).join("/");
	return {
		Paths: { cache: { uri: "file:///cache" } },
		Directory: class {
			uri: string;
			constructor(...parts: (string | { uri: string })[]) { this.uri = join(...parts); }
			get exists() { return [...disk.keys()].some((key) => key.startsWith(`${this.uri}/`)); }
			create() {}
			delete() { for (const key of [...disk.keys()]) if (key.startsWith(`${this.uri}/`)) disk.delete(key); }
		},
		File: class {
			uri: string;
			constructor(...parts: (string | { uri: string })[]) { this.uri = join(...parts); }
			write(bytes: Uint8Array) { disk.set(this.uri, bytes); }
			delete() { disk.delete(this.uri); }
		},
	};
});

const { cachedDocument, staysInCache, useEncryptedFile } = await import("./useEncryptedFile");

const config = { host: "h", httpPort: "1", muxPort: "", password: "aod_x", hostKey: "k".repeat(43) };
let result: { uri?: string; readAccess?: string; failed: boolean } = { failed: false };
// A response body that hands out these chunks in order.
function streamed(...chunks: Uint8Array[]) {
	let next = 0;
	const cancel = vi.fn(async () => {});
	return { cancel, getReader: () => ({ read: async () => next < chunks.length ? { done: false, value: chunks[next++] } : { done: true }, cancel, releaseLock: () => {} }) };
}
function Probe({ path = "report.pdf" }: { path?: string }) {
	result = useEncryptedFile(config, `http://h:1/api/v1/sessions/s/artifacts/${path}`, "v1");
	return null;
}

const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
let renderer: ReactTestRenderer;
const errors = console.error;
beforeEach(() => {
	hostFetch.mockReset();
	console.error = (...args: unknown[]) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		errors(...args);
	};
});
afterEach(() => {
	act(() => renderer.unmount());
	console.error = errors;
});

describe("useEncryptedFile", () => {
	it("reads the file through the channel into the cache, and deletes it on unmount", async () => {
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers({ "content-type": "application/pdf" }), body: streamed(Uint8Array.of(37), Uint8Array.of(80)) });
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(hostFetch).toHaveBeenCalledWith(config, "/api/v1/sessions/s/artifacts/report.pdf", { headers: { Authorization: "Bearer aod_x" }, signal: expect.any(AbortSignal) });
		expect(result.uri).toMatch(/^file:\/\/\/cache\/encrypted-files\/.+\.pdf$/);
		expect(result.readAccess).toBe("file:///cache/encrypted-files");
		expect(disk.get(result.uri!)).toEqual(Uint8Array.of(37, 80));
		act(() => renderer.unmount());
		expect(disk.size).toBe(0);
		act(() => { renderer = create(<Probe />); });
	});

	it("names a file the machine sent as HTML .html, as for a large Markdown file", async () => {
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers({ "content-type": "text/html; charset=utf-8" }), body: streamed(Uint8Array.of(60)) });
		act(() => { renderer = create(<Probe path="notes.md" />); });
		await settle();
		expect(result.uri).toMatch(/\.html$/);
	});

	it("stops reading a file without a length once it passes the size cap", async () => {
		const chunk = new Uint8Array(13 * 1024 * 1024);
		const body = streamed(chunk, chunk, chunk);
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers(), body });
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(result.failed).toBe(true);
		expect(body.cancel).toHaveBeenCalled();
		expect(disk.size).toBe(0);
	});

	it("cancels the download when the viewer closes", async () => {
		let signal: AbortSignal | undefined;
		hostFetch.mockImplementation((_config: unknown, _path: string, init: { signal: AbortSignal }) => {
			signal = init.signal;
			return new Promise(() => {});
		});
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(signal?.aborted).toBe(false);
		act(() => renderer.unmount());
		expect(signal?.aborted).toBe(true);
		act(() => { renderer = create(<Probe />); });
	});

	it("fails without reading a file over the size cap", async () => {
		const cancel = vi.fn(async () => {});
		const getReader = vi.fn();
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers({ "content-length": String(26 * 1024 * 1024) }), body: { cancel, getReader } });
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(result.failed).toBe(true);
		expect(cancel).toHaveBeenCalled();
		expect(getReader).not.toHaveBeenCalled();
	});
});

describe("staysInCache", () => {
	it("keeps the viewer on the cached file and off the machine", () => {
		const folder = "file:///cache/encrypted-files";
		expect(staysInCache(`${folder}/a.pdf`, folder)).toBe(true);
		expect(staysInCache(`${folder}/a.pdf#page=2`, folder)).toBe(true);
		expect(staysInCache("http://192.168.1.5:3011/api/v1/sessions/s/artifacts/x?token=1", folder)).toBe(false);
		expect(staysInCache("https://example.com/", folder)).toBe(false);
		expect(staysInCache("file:///etc/hosts", folder)).toBe(false);
	});
});

describe("cachedDocument", () => {
	const text = (bytes: Uint8Array) => new TextDecoder().decode(bytes);
	it("gives a cached page a policy that blocks every request, and shows SVG inside one", () => {
		const page = cachedDocument(new TextEncoder().encode("<h1>Notes</h1><img src=\"http://192.168.1.5:3011/x\">"), "notes.md", "text/html; charset=utf-8");
		expect(page.extension).toBe("html");
		expect(text(page.bytes)).toMatch(/^<meta http-equiv="Content-Security-Policy" content="default-src 'none';/);
		const svg = cachedDocument(new TextEncoder().encode("<svg><image href=\"http://h/logo.png\"/></svg>"), "chart.svg", "image/svg+xml");
		expect(svg.extension).toBe("html");
		expect(text(svg.bytes)).toContain("default-src 'none'");
		expect(text(svg.bytes)).toContain("<svg><image");
		const pdf = cachedDocument(Uint8Array.of(37, 80), "report.pdf", "application/pdf");
		expect(pdf).toEqual({ bytes: Uint8Array.of(37, 80), extension: "pdf" });
	});
});
