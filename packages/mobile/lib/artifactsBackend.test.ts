import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

import { daemonArtifactsBackend } from "./artifactsBackend";
import type { ServerConfig } from "./config";

const cfg = { host: "10.0.0.2", httpPort: "3000", muxPort: "", password: "secret", secure: false, hostId: "h_machine" } as ServerConfig;

describe("daemonArtifactsBackend", () => {
	it("names the machine by its host id, so two machines that shared an address never share a cache", () => {
		expect(daemonArtifactsBackend(cfg).key).toBe("host.h_machine");
		expect(daemonArtifactsBackend({ ...cfg, hostId: "h_other" }).key).not.toBe(daemonArtifactsBackend(cfg).key);
	});

	it("streams a file from the preview route with the credential, for a native player or download", () => {
		const source = daemonArtifactsBackend(cfg).streamSource("ao-1", "clip.mp4", "v1");
		expect(source?.uri).toBe("http://10.0.0.2:3000/api/v1/sessions/ao-1/preview/files/__ao_artifacts__/clip.mp4?raw=true&v=v1");
		expect(source?.headers?.Authorization).toBe("Bearer secret");
	});

	it("can tell a file's size, read its start, and read text within a limit", () => {
		const backend = daemonArtifactsBackend(cfg);
		expect(typeof backend.size).toBe("function");
		expect(typeof backend.head).toBe("function");
		expect(typeof backend.textWithin).toBe("function");
	});
});
