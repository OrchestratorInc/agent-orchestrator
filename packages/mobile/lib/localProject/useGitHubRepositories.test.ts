import { describe, expect, it, vi } from "vitest";

vi.mock("../cloud/CloudProvider", () => ({ useCloud: () => ({}) }));
vi.mock("../cloud/client", () => ({ collectPages: vi.fn() }));

import { cloneUrlFor } from "./useGitHubRepositories";

describe("cloneUrlFor", () => {
	it("clones over HTTPS from the repository's page", () => {
		expect(cloneUrlFor({ htmlUrl: "https://github.com/acme/api", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
		expect(cloneUrlFor({ htmlUrl: "https://github.com/acme/api/", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
		expect(cloneUrlFor({ htmlUrl: "", fullName: "acme/api" })).toBe("https://github.com/acme/api.git");
	});
});
