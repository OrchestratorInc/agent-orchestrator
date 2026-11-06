import { describe, expect, it, vi } from "vitest";
import { artifactContentUrl } from "./artifact-content-url";

vi.mock("./api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:3002" }));

const previewUrl = "http://ao-preview-artifact.s1.localhost:3002/chart.png?raw=true";
const cloudUrl = "http://cp.test/api/cloud/v1/artifact-files/token/chart.png";

describe("artifactContentUrl", () => {
	it("reads a saved version through the daemon's preview route", () => {
		expect(artifactContentUrl("s1", "dir/chart a.png", previewUrl, 3)).toBe("http://127.0.0.1:3002/api/v1/sessions/s1/preview/files/dir/chart%20a.png?source=artifact&raw=true&version=3");
	});

	it("reads a saved version from a cloud file's own link", () => {
		expect(artifactContentUrl("s1", "chart.png", cloudUrl, 3)).toBe(`${cloudUrl}?version=3`);
	});

	it("leaves the live file's URL without a version", () => {
		expect(artifactContentUrl("s1", "chart.png", previewUrl)).toBe("http://127.0.0.1:3002/api/v1/sessions/s1/preview/files/chart.png?source=artifact&raw=true");
		expect(artifactContentUrl("s1", "chart.png", cloudUrl)).toBe(cloudUrl);
	});
});
