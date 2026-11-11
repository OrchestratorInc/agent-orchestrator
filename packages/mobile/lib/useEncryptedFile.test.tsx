import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hostFetch = vi.fn();
vi.mock("./config", () => ({ authHeaders: () => ({ Authorization: "Bearer aod_x" }) }));
vi.mock("./e2ee/host", () => ({
	hostFetch: (...args: unknown[]) => hostFetch(...args),
	hostPath: (url: string) => new URL(url).pathname,
	bytesToDataUri: (bytes: Uint8Array, type: string) => `data:${type};base64,${btoa(String.fromCharCode(...bytes))}`,
}));

const { useEncryptedFile } = await import("./useEncryptedFile");

const config = { host: "h", httpPort: "1", muxPort: "", password: "aod_x", hostKey: "k".repeat(43) };
let result: { uri?: string; failed: boolean } = { failed: false };
function Probe() {
	result = useEncryptedFile(config, "http://h:1/api/v1/sessions/s/artifacts/report.pdf", "v1");
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
	it("reads the file through the channel as a data URI", async () => {
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers({ "content-type": "application/pdf" }), arrayBuffer: async () => Uint8Array.of(37, 80).buffer, body: null });
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(result).toEqual({ key: expect.any(String), uri: "data:application/pdf;base64,JVA=", failed: false });
		expect(hostFetch).toHaveBeenCalledWith(config, "/api/v1/sessions/s/artifacts/report.pdf", { headers: { Authorization: "Bearer aod_x" } });
	});

	it("fails without reading a file over the size cap", async () => {
		const cancel = vi.fn(async () => {});
		const arrayBuffer = vi.fn();
		hostFetch.mockResolvedValue({ ok: true, status: 200, headers: new Headers({ "content-length": String(26 * 1024 * 1024) }), arrayBuffer, body: { cancel } });
		act(() => { renderer = create(<Probe />); });
		await settle();
		expect(result.failed).toBe(true);
		expect(cancel).toHaveBeenCalled();
		expect(arrayBuffer).not.toHaveBeenCalled();
	});
});
