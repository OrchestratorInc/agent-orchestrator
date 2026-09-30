import { describe, expect, it } from "vitest";
import type { CloudCpProviderConnection } from "./cloud-cp";
import { cloudAgentInfos } from "./cloud-agents";

function connection(
	provider: string,
	overrides: Partial<CloudCpProviderConnection> = {},
): CloudCpProviderConnection {
	return {
		id: provider,
		provider,
		label: "default",
		config: {},
		validationState: "valid",
		createdAt: "2026-01-01T00:00:00Z",
		updatedAt: "2026-01-01T00:00:00Z",
		...overrides,
	};
}

describe("cloudAgentInfos", () => {
	it("offers exactly Claude Code, Codex, and Cursor on cloud", () => {
		expect(cloudAgentInfos([]).map((agent) => agent.id)).toEqual(["claude-code", "codex", "cursor"]);
	});

	it("marks an agent ready only when its connection is valid", () => {
		const agents = cloudAgentInfos([
			connection("claude-code"),
			connection("codex", { validationState: "invalid" }),
			connection("opencode"),
		]);
		expect(agents.map((agent) => [agent.id, agent.effectiveReadiness])).toEqual([
			["claude-code", "ready"],
			["codex", "not_ready"],
			["cursor", "not_ready"],
		]);
	});
});
