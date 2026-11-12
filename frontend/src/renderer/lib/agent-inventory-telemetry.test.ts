import { describe, expect, it } from "vitest";
import { buildAgentInventory, buildSetupCheck } from "./agent-inventory-telemetry";
import type { AgentReadiness } from "../hooks/useAgentReadinessQuery";
import { agentReadiness } from "../test/agent-readiness-fixtures";

const catalog = (agents: AgentReadiness["agents"] = []): AgentReadiness => ({ agents });

describe("agent inventory", () => {
	// The open question is whether people ever configure more than one agent.
	// ao.session.spawned only shows which harness ran, so an install with six
	// authorized agents that always picks one looked identical to one that has
	// only that one.
	it("counts installed, authorized, and supported separately", () => {
		const inventory = buildAgentInventory(
			catalog(
				Array.from({ length: 23 }, (_, i) =>
					agentReadiness(`a${i}`, `A${i}`, {
						installation: i < 2 ? "installed" : "not_installed",
						authentication: i === 1 ? "authorized" : "unknown",
					}),
				),
			),
		);
		expect(inventory.installed_count).toBe(2);
		expect(inventory.authorized_count).toBe(1);
		expect(inventory.supported_count).toBe(23);
	});

	// Sorted so the same set of agents always produces the same value and can be
	// grouped in analysis rather than fragmenting by ordering.
	it("reports authorized agent ids sorted and stable", () => {
		const a = buildAgentInventory(catalog([agentReadiness("codex"), agentReadiness("claude-code"), agentReadiness("cursor")]));
		const b = buildAgentInventory(catalog([agentReadiness("cursor"), agentReadiness("codex"), agentReadiness("claude-code")]));
		expect(a.authorized_agents).toBe("claude-code,codex,cursor");
		expect(a.authorized_agents).toBe(b.authorized_agents);
	});

	it("handles an empty or partial catalog without throwing", () => {
		const inventory = buildAgentInventory(catalog());
		expect(inventory).toEqual({
			installed_count: 0, authorized_count: 0, supported_count: 0, authorized_agents: "",
		});
	});

	// Bounded so a registry that grows cannot turn one property into a long string.
	it("bounds the joined id list", () => {
		const many = Array.from({ length: 80 }, (_, i) => agentReadiness(`agent-name-${i}`));
		const inventory = buildAgentInventory(catalog(many));
		expect(inventory.authorized_agents.length).toBeLessThanOrEqual(200);
		expect(inventory.authorized_count).toBe(80);
	});

	it("ignores malformed ids rather than reporting empty entries", () => {
		const inventory = buildAgentInventory(catalog([agentReadiness(""), agentReadiness("codex")]));
		expect(inventory.authorized_agents).toBe("codex");
		expect(inventory.authorized_count).toBe(1);
	});
});

describe("setup check", () => {
	const requirements = [
		{ id: "git", label: "git", required: true, satisfied: true, detail: "/Users/someone/bin/git" },
		{ id: "tmux", label: "tmux", required: true, satisfied: false, detail: "tmux was not found on PATH." },
		{ id: "gh", label: "gh", required: false, satisfied: true, detail: "/opt/homebrew/bin/gh" },
	];

	it("folds the startup check into booleans, counts and registry ids, never details", () => {
		const check = buildSetupCheck(
			requirements,
			catalog([
				agentReadiness("codex", "Codex", { installation: "installed", authentication: "unknown" }),
				agentReadiness("claude-code", "Claude", { installation: "installed", authentication: "authorized" }),
				agentReadiness("cursor", "Cursor", { installation: "not_installed", authentication: "unknown" }),
			]),
			true,
		);
		expect(check).toEqual({
			git: true,
			tmux: false,
			gh: true,
			gh_signed_in: true,
			agents_installed: 2,
			agents_signed_in: 1,
			agents_installed_ids: "claude-code,codex",
			agents_signed_in_ids: "claude-code",
		});
		expect(JSON.stringify(check)).not.toMatch(/Users|homebrew|PATH/);
	});

	it("omits what the startup check did not answer", () => {
		expect(buildSetupCheck([], catalog(), undefined)).toEqual({
			agents_installed: 0, agents_signed_in: 0, agents_installed_ids: "", agents_signed_in_ids: "",
		});
	});
});
