import { describe, expect, it } from "vitest";
import { cloudRoleDefaults, modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged, spawnRequestFingerprint } from "./spawnModel";

it("changes request identity for account, source, model or effort edits", () => {
	const request = { projectId: "p1", prompt: "Inspect", harness: "codex", model: "model", reasoningEffort: "high" as const };
	const fingerprint = spawnRequestFingerprint("scope-one", "cloud/o1", request);
	for (const input of [spawnRequestFingerprint("scope-two", "cloud/o1", request), spawnRequestFingerprint("scope-one", "local/h1", request), spawnRequestFingerprint("scope-one", "cloud/o1", { ...request, model: "other" }), spawnRequestFingerprint("scope-one", "cloud/o1", { ...request, reasoningEffort: "low" })]) expect(input).not.toBe(fingerprint);
});
it("uses only matching project role defaults and rejects invalid effort values", () => {
	const config = { worker: { agent: "codex", agentConfig: { model: " pinned ", reasoningEffort: "high" } } };
	 expect(cloudRoleDefaults(config, "worker", "codex")).toEqual({ model: "pinned", reasoningEffort: "high" });
	 expect(cloudRoleDefaults(config, "worker", "claude-code")).toEqual({});
	 expect(cloudRoleDefaults({ worker: { agent: "codex", agentConfig: { reasoningEffort: "unknown" } } }, "worker", "codex")).toEqual({});
});

describe("spawn agent resolution", () => {
	it("prefers the project's worker agent when it is available", () => {
		expect(resolveSpawnAgent({
			projectWorkerAgent: "codex",
			projectAgent: "claude-code",
			availableAgents: ["claude-code", "codex"],
		})).toBe("codex");
	});

	it("falls back through the project agent and catalog order", () => {
		expect(resolveSpawnAgent({
			projectWorkerAgent: "missing",
			projectAgent: "claude-code",
			availableAgents: ["codex", "claude-code"],
		})).toBe("claude-code");
		expect(resolveSpawnAgent({
			availableAgents: ["codex", "claude-code"],
		})).toBe("codex");
	});
});

describe("spawn model resolution", () => {
	it("prefers the project worker model for its configured worker agent", () => {
		expect(resolveSpawnModel({
			selectedAgent: "codex",
			projectWorkerAgent: "codex",
			projectWorkerModel: "gpt-5",
		})).toBe("gpt-5");
	});

	// The catalog's `isDefault` entry is not a source here. It says what the
	// provider *claims* it will pick, and codex claimed `gpt-6-astra` while a new
	// thread came up on `gpt-5.6-sol` — the sheet named one model and the chat
	// named the other.
	it("ignores another agent's project model", () => {
		expect(resolveSpawnModel({
			selectedAgent: "claude-code",
			projectWorkerAgent: "codex",
			projectWorkerModel: "gpt-5",
		})).toBe("");
	});

	it("leaves automatic selection empty when the project pins nothing", () => {
		expect(resolveSpawnModel({ selectedAgent: "codex" })).toBe("");
		expect(resolveSpawnModel({
			selectedAgent: "codex",
			projectWorkerAgent: "codex",
			projectWorkerModel: "  ",
		})).toBe("");
	});

	// Sending the pick even when it matches the project's own pin is the point:
	// the old rule dropped it, the session stored no model, and the chat then had
	// nothing to resolve and showed whatever the thread was running.
	it("sends a touched value, including one that matches the project's pin", () => {
		expect(modelOverride("opus", true)).toBe("opus");
		expect(modelOverride("sonnet", true)).toBe("sonnet");
		expect(modelOverride("  opus  ", true)).toBe("opus");
	});

	it("sends nothing for the untouched Automatic state", () => {
		expect(modelOverride("opus", false)).toBeUndefined();
		expect(modelOverride("", true)).toBeUndefined();
		expect(modelOverride("__auto__", true)).toBeUndefined();
	});

	it("reloads the model catalog only when its project or agent source changes", () => {
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "codex" },
		)).toBe(false);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-2", agentId: "codex" },
		)).toBe(true);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "claude-code" },
		)).toBe(true);
	});
});
