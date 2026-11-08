import { describe, expect, it } from "vitest";
import { repositoryOwnerAvatar } from "./repository-avatar";

describe("repositoryOwnerAvatar", () => {
	it("uses the remote owner when GitHub reports no rename", () => {
		expect(repositoryOwnerAvatar("https://github.com/acme/widgets.git")).toEqual({
			owner: "acme",
			url: "https://avatars.githubusercontent.com/acme?s=64",
		});
	});

	it("uses GitHub's canonical owner for a renamed or transferred repository", () => {
		expect(repositoryOwnerAvatar("https://github.com/Untrivial-ai/agent-orchestrator.git", "OrchestratorInc/agent-orchestrator")).toEqual({
			owner: "OrchestratorInc",
			url: "https://avatars.githubusercontent.com/OrchestratorInc?s=64",
		});
		expect(repositoryOwnerAvatar("git@github.com:Untrivial-ai/agent-orchestrator.git", "OrchestratorInc/agent-orchestrator")?.owner).toBe("OrchestratorInc");
	});

	it("ignores a malformed canonical repository", () => {
		expect(repositoryOwnerAvatar("https://github.com/acme/widgets", "not a repo")?.owner).toBe("acme");
	});

	it("applies a canonical repository only to github.com remotes", () => {
		expect(repositoryOwnerAvatar("https://gitlab.com/acme/widgets", "other/widgets")?.owner).toBe("acme");
		expect(repositoryOwnerAvatar("https://git.internal.example/acme/widgets", "other/widgets")).toBeNull();
	});
});
