import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	cloud: {} as Record<string, unknown>,
	pending: new Map<string, (items: unknown[]) => void>(),
}));
vi.mock("../cloud/CloudProvider", () => ({ useCloud: () => fixtures.cloud }));
vi.mock("../cloud/client", () => ({
	collectPages: (read: () => Promise<{ orgId: string }>) => read().then(({ orgId }) => new Promise((resolve) => fixtures.pending.set(orgId, resolve))),
}));

import { cloneUrlFor, useGitHubRepositories, type GitHubRepositories } from "./useGitHubRepositories";

describe("cloneUrlFor", () => {
	it("clones over HTTPS from the repository's page", () => {
		expect(cloneUrlFor({ htmlUrl: "https://github.com/acme/api", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
		expect(cloneUrlFor({ htmlUrl: "https://github.com/acme/api/", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
		expect(cloneUrlFor({ htmlUrl: "", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
	});
});

const repo = (fullName: string) => ({ githubRepositoryId: fullName, name: fullName.split("/")[1], fullName, htmlUrl: `https://github.com/${fullName}`, defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "admin", grantedAt: "" });
const client = {
	listGitHubInstallations: async () => ({ installations: [{ status: "active" }] }),
	listGitHubRepositories: async (orgId: string) => ({ orgId }),
};
const current: { github?: GitHubRepositories } = {};
let renderer: ReactTestRenderer | undefined;
function Probe() {
	current.github = useGitHubRepositories(true);
	return null;
}
async function flush() {
	for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
}

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.pending.clear();
});

describe("useGitHubRepositories", () => {
	it("never shows one organization's repositories under another", async () => {
		vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
		fixtures.cloud = { status: "ready", client, org: { id: "org-a" } };
		await act(async () => { renderer = create(createElement(Probe)); });
		await flush();
		await act(async () => { fixtures.pending.get("org-a")!([repo("a/one")]); });
		await flush();
		await act(async () => current.github!.setQuery("one"));
		expect(current.github!.matches.map((item) => item.fullName)).toEqual(["a/one"]);

		// Another organization: its list is still loading.
		fixtures.cloud = { status: "ready", client, org: { id: "org-b" } };
		await act(async () => { renderer!.update(createElement(Probe)); });
		await flush();
		expect(current.github!.available).toBe(false);
		expect(current.github!.matches).toEqual([]);

		await act(async () => { fixtures.pending.get("org-b")!([repo("b/one")]); });
		await flush();
		expect(current.github!.matches.map((item) => item.fullName)).toEqual(["b/one"]);
	});
});
