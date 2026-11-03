import { describe, expect, it, vi } from "vitest";
import { createControlPlaneUrlResolver, sameControlPlaneUrl } from "./cloud-control-plane-url";

const settings = (cloudControlPlaneUrl: unknown, status = 200) => new Response(JSON.stringify({ cloudControlPlaneUrl }), { status });

describe("createControlPlaneUrlResolver", () => {
	it("reads the control plane from the attached daemon's settings once per attachment", async () => {
		let daemon: { baseUrl: string; key: string } | null = { baseUrl: "http://127.0.0.1:3001", key: "3001:10" };
		const fetchImpl = vi.fn(async (_url: string) => settings("https://staging-api.aoagents.dev"));
		const resolve = createControlPlaneUrlResolver(() => daemon, fetchImpl as unknown as typeof fetch);
		await expect(resolve()).resolves.toBe("https://staging-api.aoagents.dev");
		await expect(resolve()).resolves.toBe("https://staging-api.aoagents.dev");
		expect(fetchImpl).toHaveBeenCalledTimes(1);
		expect(fetchImpl.mock.calls[0][0]).toBe("http://127.0.0.1:3001/api/v1/settings");
		// The same port with a new daemon asks again.
		daemon = { baseUrl: "http://127.0.0.1:3001", key: "3001:11" };
		fetchImpl.mockResolvedValueOnce(settings("https://api.aoagents.dev"));
		await expect(resolve()).resolves.toBe("https://api.aoagents.dev");
		daemon = null;
		await expect(resolve()).resolves.toBeNull();
	});

	it("answers null for a failed read, a missing value, or no daemon, and asks again after a failure", async () => {
		const fetchImpl = vi.fn()
			.mockRejectedValueOnce(new Error("connection refused"))
			.mockResolvedValueOnce(settings("", 200))
			.mockResolvedValueOnce(settings("https://api.aoagents.dev", 500));
		const keys = ["a", "a", "b", "c"];
		const resolve = createControlPlaneUrlResolver(() => ({ baseUrl: "http://127.0.0.1:3001", key: keys.shift() ?? "c" }), fetchImpl as unknown as typeof fetch);
		await expect(resolve()).resolves.toBeNull();
		await expect(resolve()).resolves.toBeNull();
		await expect(resolve()).resolves.toBeNull();
		expect(fetchImpl).toHaveBeenCalledTimes(3);
	});
});

describe("sameControlPlaneUrl", () => {
	it("matches the same origin and path, ignoring trailing slashes and case in the host", () => {
		expect(sameControlPlaneUrl("https://api.aoagents.dev", "https://api.aoagents.dev/")).toBe(true);
		expect(sameControlPlaneUrl("https://API.aoagents.dev/", "https://api.aoagents.dev")).toBe(true);
		expect(sameControlPlaneUrl("https://api.aoagents.dev", "https://api.aoagents.dev.evil.example")).toBe(false);
		expect(sameControlPlaneUrl("https://api.aoagents.dev", "http://api.aoagents.dev")).toBe(false);
		expect(sameControlPlaneUrl("https://api.aoagents.dev", "https://api.aoagents.dev/v2")).toBe(false);
		expect(sameControlPlaneUrl("not a url", "https://api.aoagents.dev")).toBe(false);
	});
});
