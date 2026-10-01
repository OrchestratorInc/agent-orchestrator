import { describe, expect, it, vi } from "vitest";
import { loadGrantedRepositories } from "./githubProjectRepositories";

const repo = (id: string, overrides = {}) => ({ githubRepositoryId: id, name: id, fullName: `owner/${id}`, htmlUrl: `https://github.com/owner/${id}`, defaultBranch: "main", isArchived: false, access: "active", ...overrides });

describe("Cloud GitHub repository picker", () => {
	it("loads all pages and omits revoked or archived repositories", async () => {
		const listGitHubRepositories = vi.fn()
			.mockResolvedValueOnce({ items: [repo("one"), repo("archived", { isArchived: true })], page: { hasMore: true, nextCursor: "next" } })
			.mockResolvedValueOnce({ items: [repo("one"), repo("two"), repo("revoked", { access: "revoked" })], page: { hasMore: false } });
		const signal = new AbortController().signal;
		const result = await loadGrantedRepositories({ listGitHubRepositories } as never, "org", signal);
		expect(result.map((item) => item.githubRepositoryId)).toEqual(["one", "two"]);
		expect(listGitHubRepositories).toHaveBeenNthCalledWith(2, "org", { cursor: "next", limit: 100, signal });
	});

	it("fails closed if pagination cannot advance", async () => {
		const listGitHubRepositories = vi.fn().mockResolvedValue({ items: [], page: { hasMore: true } });
		await expect(loadGrantedRepositories({ listGitHubRepositories } as never, "org", new AbortController().signal)).rejects.toThrow("pagination");
	});
});
