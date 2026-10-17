import { beforeEach, describe, expect, it, vi } from "vitest";

const post = vi.fn();
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: vi.fn(), POST: (...args: unknown[]) => post(...args) },
	apiErrorMessage: (error: unknown) => String(error),
}));

describe("refreshAgentsIfStale", () => {
	beforeEach(() => {
		vi.resetModules();
		post.mockReset();
	});

	it("allows an immediate retry after a failed refresh", async () => {
		const { refreshAgentsIfStale } = await import("./useAgentsQuery");
		post.mockResolvedValueOnce({ error: "daemon booting" });
		expect(await refreshAgentsIfStale()).toBeUndefined();

		const catalog = { supported: [], installed: [], authorized: [] };
		post.mockResolvedValueOnce({ data: catalog });
		expect(await refreshAgentsIfStale()).toEqual(catalog);
		expect(post).toHaveBeenCalledTimes(2);
	});

	it("throttles after a successful refresh", async () => {
		const { refreshAgentsIfStale } = await import("./useAgentsQuery");
		post.mockResolvedValue({ data: { supported: [], installed: [], authorized: [] } });
		await refreshAgentsIfStale();
		expect(await refreshAgentsIfStale()).toBeUndefined();
		expect(post).toHaveBeenCalledTimes(1);
	});
});
