import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { invalidateAgentModelCatalogs, modelCatalogAuthIssue } from "./useAgentModelsQuery";

describe("modelCatalogAuthIssue", () => {
	it("reads the daemon's warning code", () => {
		expect(modelCatalogAuthIssue({ warning: "x", warningCode: "auth_required" })).toBe("required");
		expect(modelCatalogAuthIssue({ warning: "x", warningCode: "auth_expired" })).toBe("expired");
	});

	it("recognizes login wording from daemons that predate warning codes", () => {
		expect(modelCatalogAuthIssue({ warning: "claude-code: model discovery: Anthropic rejected the credential: OAuth access token has expired. Re-authenticate to continue." })).toBe("required");
		expect(modelCatalogAuthIssue({ warning: "Kiro is not signed in; sign in to load its models" })).toBe("required");
	});

	it("leaves other warnings alone", () => {
		expect(modelCatalogAuthIssue({ warning: "codex model discovery returned no models" })).toBeUndefined();
		expect(modelCatalogAuthIssue({ warning: "Models loaded, but AO could not update the model cache." })).toBeUndefined();
		expect(modelCatalogAuthIssue(undefined)).toBeUndefined();
	});
});

describe("invalidateAgentModelCatalogs", () => {
	function seeded() {
		const client = new QueryClient();
		const keys = {
			local: ["agent-models", "claude-code", ""],
			localProject: ["agent-models", "claude-code", "proj-1"],
			otherAgent: ["agent-models", "codex", ""],
			remote: ["agent-models", "box-a", "claude-code", ""],
			pickerRevalidation: ["agent-model-revalidation", "claude-code", "", "t1"],
			composerRevalidation: ["agent-model-revalidation", "", "claude-code", "", "t1"],
			settingsRevalidation: ["agent-model-revalidation", "local", "claude-code", "", "t1"],
			remoteRevalidation: ["agent-model-revalidation", "box-a", "claude-code", "", "t1"],
			otherRevalidation: ["agent-model-revalidation", "", "codex", "", "t1"],
		} as const;
		for (const key of Object.values(keys)) client.setQueryData(key, { models: [] });
		const invalidated = (key: readonly unknown[]) => client.getQueryState(key)?.isInvalidated === true;
		return { client, keys, invalidated };
	}

	it("drops one agent's local catalogs and revalidations only", async () => {
		const { client, keys, invalidated } = seeded();
		await invalidateAgentModelCatalogs(client, "claude-code");
		expect([keys.local, keys.localProject, keys.pickerRevalidation, keys.composerRevalidation, keys.settingsRevalidation].every(invalidated)).toBe(true);
		expect([keys.otherAgent, keys.remote, keys.remoteRevalidation, keys.otherRevalidation].some(invalidated)).toBe(false);
	});

	it("scopes a remote host's invalidation to that host", async () => {
		const { client, keys, invalidated } = seeded();
		await invalidateAgentModelCatalogs(client, "claude-code", "box-a");
		expect(invalidated(keys.remote) && invalidated(keys.remoteRevalidation)).toBe(true);
		expect([keys.local, keys.composerRevalidation, keys.otherRevalidation].some(invalidated)).toBe(false);
	});
});
