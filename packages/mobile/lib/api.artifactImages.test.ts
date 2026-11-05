import { describe, expect, it, vi } from "vitest";
import { artifactMarkdownImageSource } from "./api";
import type { ServerConfig } from "./config";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

const cfg = { host: "10.0.0.2", httpPort: "3000", muxPort: "", password: "secret", secure: false } as ServerConfig;
const base = "http://10.0.0.2:3000/api/v1/sessions/ao-1/artifact-files";

describe("artifactMarkdownImageSource", () => {
	it("loads a file beside the document with the credential, versioned by its listed time", () => {
		const versionOf = (path: string) => (path === "shots/chart.png" ? "2026-10-30T10:00:00Z" : undefined);
		const source = artifactMarkdownImageSource(cfg, "ao-1", "report.md", "shots/chart.png", versionOf);
		expect(source?.uri).toBe(`${base}/shots/chart.png?v=2026-10-30T10%3A00%3A00Z`);
		expect(source?.headers?.Authorization).toBe("Bearer secret");
	});

	it("takes an absolute path into this session's artifact folder, as an HTML page does", () => {
		expect(artifactMarkdownImageSource(cfg, "ao-1", "notes/report.md", "/Users/me/.ao/data/artifacts/ao-1/shots/a.png")?.uri).toBe(`${base}/shots/a.png`);
		expect(artifactMarkdownImageSource(cfg, "ao-1", "report.md", "/Users/me/.ao/data/artifacts/ao-2/a.png")).toBeUndefined();
		expect(artifactMarkdownImageSource(cfg, "ao-1", "report.md", "../../etc/a.png")).toBeUndefined();
	});

	it("loads a web image as it is, with no credential", () => {
		expect(artifactMarkdownImageSource(cfg, "ao-1", "report.md", "https://example.com/a.png")).toEqual({ uri: "https://example.com/a.png" });
	});
});
