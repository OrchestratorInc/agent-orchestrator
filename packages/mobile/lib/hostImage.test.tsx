import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ Image: "Image", Platform: { OS: "ios" } }));

const hostFetch = vi.fn();
vi.mock("./e2ee/host", () => ({
	hostFetch: (...args: unknown[]) => hostFetch(...args),
	hostPath: (url: string) => new URL(url).pathname,
	bytesToDataUri: (bytes: Uint8Array, type: string) => `data:${type};base64,${btoa(String.fromCharCode(...bytes))}`,
}));

const { useHostImage } = await import("./hostImage");

const config = { host: "h", httpPort: "1", muxPort: "", password: "aod_x", hostKey: "k".repeat(43) };
const image = (status: number) => ({
	ok: status === 200,
	status,
	headers: new Headers({ "content-type": "image/png" }),
	arrayBuffer: async () => Uint8Array.of(1, 2).buffer,
});

let renders = 0;
let shown: { uri: string } | undefined;

function Probe({ uri, attempt, token = "aod_x" }: { uri: string; attempt: number; token?: string }) {
	renders++;
	// A new source object every render, as MarkdownImage's resolver returns.
	shown = useHostImage(Object.assign({ uri, headers: {} }, { via: { config, headers: { Authorization: `Bearer ${token}` } } }), attempt);
	return null;
}

const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });

let renderer: ReactTestRenderer;
const errors = console.error;
beforeEach(() => {
	renders = 0;
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

describe("useHostImage", () => {
	it("reads the image once and settles though the source is rebuilt on every render", async () => {
		hostFetch.mockResolvedValue(image(200));
		act(() => { renderer = create(<Probe uri="http://h:1/a.png" attempt={0} />); });
		await settle();
		await settle();
		expect(shown?.uri).toBe("data:image/png;base64,AQI=");
		expect(hostFetch).toHaveBeenCalledOnce();
		expect(renders).toBeLessThan(4);
	});

	it("reads a failed image again with a new credential", async () => {
		hostFetch.mockResolvedValueOnce(image(401)).mockResolvedValueOnce(image(200));
		act(() => { renderer = create(<Probe uri="http://h:1/c.png" attempt={0} token="aod_old" />); });
		await settle();
		expect(shown?.uri).toBe("data:,");
		act(() => renderer.update(<Probe uri="http://h:1/c.png" attempt={0} token="aod_new" />));
		await settle();
		expect(shown?.uri).toBe("data:image/png;base64,AQI=");
		expect(hostFetch).toHaveBeenLastCalledWith(config, "/c.png", { headers: { Authorization: "Bearer aod_new" } });
	});

	it("reads a failed image again on the next attempt", async () => {
		hostFetch.mockResolvedValueOnce(image(500)).mockResolvedValueOnce(image(200));
		act(() => { renderer = create(<Probe uri="http://h:1/b.png" attempt={0} />); });
		await settle();
		expect(shown?.uri).toBe("data:,");
		act(() => renderer.update(<Probe uri="http://h:1/b.png" attempt={1} />));
		await settle();
		expect(shown?.uri).toBe("data:image/png;base64,AQI=");
		expect(hostFetch).toHaveBeenCalledTimes(2);
	});
});
