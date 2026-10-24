import type { RedactedProviderConnection } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { createCloudProjectSetupCache, type ProjectGitHubSetup } from "./projectSetupCache";

const github: ProjectGitHubSetup = {
	installations: [{ id: "installation-1", githubInstallationId: "1", accountLogin: "alice", accountType: "User", status: "active", repositorySelection: "selected", syncStatus: "ready", createdAt: "2026-10-20T00:00:00Z", updatedAt: "2026-10-20T00:00:00Z" }],
	repositories: [{ githubRepositoryId: "1", name: "app", fullName: "alice/app", htmlUrl: "https://github.com/alice/app", defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "active", grantedAt: "2026-10-20T00:00:00Z" }],
};
const connections: RedactedProviderConnection[] = [{ id: "connection-1", provider: "codex", label: "default", config: {}, validationState: "valid", createdAt: "2026-10-20T00:00:00Z", updatedAt: "2026-10-20T00:00:00Z" }];

describe("Cloud project setup cache", () => {
	it("returns completed setup data synchronously for a reopened owner", () => {
		const cache = createCloudProjectSetupCache();
		expect(cache.read("alice")).toEqual({});
		cache.setGitHub("alice", github);
		cache.setConnections("alice", connections);
		expect(cache.read("alice")).toEqual({ github: { value: github, fresh: true }, connections: { value: connections, fresh: true } });
	});

	it("uses the current default clock when Date.now changes after creation", () => {
		const cache = createCloudProjectSetupCache();
		const clock = vi.spyOn(Date, "now").mockReturnValue(1_000);
		try {
			cache.setGitHub("alice", github);
			clock.mockReturnValue(31_000);
			expect(cache.read("alice").github).toEqual({ value: github, fresh: false });
		} finally { clock.mockRestore(); }
	});

	it("keeps stale values while GitHub and connections expire independently", () => {
		let now = 1_000;
		const cache = createCloudProjectSetupCache({ now: () => now });
		cache.setGitHub("alice", github);
		cache.setConnections("alice", connections);
		now = 30_999;
		expect(cache.read("alice").github).toEqual({ value: github, fresh: true });
		now = 31_000;
		expect(cache.read("alice")).toEqual({ github: { value: github, fresh: false }, connections: { value: connections, fresh: true } });
		now = 60_999;
		expect(cache.read("alice").connections).toEqual({ value: connections, fresh: true });
		now = 61_000;
		expect(cache.read("alice")).toEqual({ github: { value: github, fresh: false }, connections: { value: connections, fresh: false } });
	});

	it("refreshes only the resource that was updated", () => {
		let now = 0;
		const cache = createCloudProjectSetupCache({ now: () => now });
		cache.setGitHub("alice", github);
		cache.setConnections("alice", connections);
		now = 60_000;
		cache.setGitHub("alice", github);
		expect(cache.read("alice")).toEqual({ github: { value: github, fresh: true }, connections: { value: connections, fresh: false } });
	});

	it.each(["other-api/alice/org/1", "api/bob/org/1", "api/alice/other-org/1", "api/alice/org/2"])("isolates the owner %s", (otherOwner) => {
		const cache = createCloudProjectSetupCache();
		cache.setGitHub("api/alice/org/1", github);
		cache.setConnections("api/alice/org/1", connections);
		expect(cache.read(otherOwner)).toEqual({});
		cache.setConnections(otherOwner, []);
		expect(cache.read("api/alice/org/1").connections).toEqual({ value: connections, fresh: true });
		expect(cache.read(otherOwner)).toEqual({ connections: { value: [], fresh: true } });
	});

	it("replaces previous data with successful empty results", () => {
		const cache = createCloudProjectSetupCache();
		cache.setGitHub("alice", github);
		cache.setConnections("alice", connections);
		cache.setGitHub("alice", { installations: [], repositories: [] });
		cache.setConnections("alice", []);
		expect(cache.read("alice")).toEqual({ github: { value: { installations: [], repositories: [] }, fresh: true }, connections: { value: [], fresh: true } });
	});

	it("invalidates resources independently without changing another owner", () => {
		const cache = createCloudProjectSetupCache();
		cache.setGitHub("alice", github);
		cache.setConnections("alice", connections);
		cache.setGitHub("bob", github);
		cache.invalidate("alice", "github");
		expect(cache.read("alice")).toEqual({ connections: { value: connections, fresh: true } });
		expect(cache.read("bob")).toEqual({ github: { value: github, fresh: true } });
		cache.invalidate("alice", "connections");
		expect(cache.read("alice")).toEqual({});
	});
	it("invalidates personal connection metadata across the current account's organizations only", () => {
		const cache = createCloudProjectSetupCache({ maxEntries: 8 });
		const sameAccount = [JSON.stringify(["https://cloud.test", "alice", "org-1", 7]), JSON.stringify(["https://cloud.test", "alice", "org-2", 7])];
		const otherOwners = [JSON.stringify(["https://other.test", "alice", "org-1", 7]), JSON.stringify(["https://cloud.test", "bob", "org-1", 7]), JSON.stringify(["https://cloud.test", "alice", "org-1", 8])];
		for (const key of [...sameAccount, ...otherOwners]) { cache.setGitHub(key, github); cache.setConnections(key, connections); }
		cache.invalidateAccountConnections(JSON.stringify(["https://cloud.test", "alice", 7]));
		for (const key of sameAccount) expect(cache.read(key)).toEqual({ github: { value: github, fresh: true } });
		for (const key of otherOwners) expect(cache.read(key)).toEqual({ github: { value: github, fresh: true }, connections: { value: connections, fresh: true } });
	});

	it("ignores malformed account identities without invalidating another owner's cache", () => {
		const cache = createCloudProjectSetupCache();
		const owner = JSON.stringify(["https://cloud.test", "alice", "org-1", 7]);
		cache.setConnections(owner, connections);
		for (const malformed of ["alice", "[]", '["https://cloud.test","alice"]', '["https://cloud.test",null,7]', '["https://cloud.test","alice","7"]']) cache.invalidateAccountConnections(malformed);
		expect(cache.read(owner)).toEqual({ connections: { value: connections, fresh: true } });
	});

	it("retains four recently used owners by default", () => {
		const cache = createCloudProjectSetupCache();
		for (const owner of ["one", "two", "three", "four"]) cache.setGitHub(owner, github);
		expect(cache.read("one").github?.value).toEqual(github);
		cache.setConnections("five", connections);
		expect(cache.read("two")).toEqual({});
		expect(cache.read("one").github?.value).toEqual(github);
		expect(cache.read("three").github?.value).toEqual(github);
		expect(cache.read("four").github?.value).toEqual(github);
		expect(cache.read("five").connections?.value).toEqual(connections);
	});

	it("applies a custom owner limit across both resources", () => {
		const cache = createCloudProjectSetupCache({ maxEntries: 2 });
		cache.setGitHub("one", github);
		cache.setConnections("two", connections);
		cache.setGitHub("three", github);
		expect(cache.read("one")).toEqual({});
		expect(cache.read("two").connections?.value).toEqual(connections);
		expect(cache.read("three").github?.value).toEqual(github);
	});
});
