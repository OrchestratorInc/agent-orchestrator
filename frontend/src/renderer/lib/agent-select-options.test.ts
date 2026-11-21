import { describe, expect, it } from "vitest";
import type { components } from "../../api/schema";
import {
	buildRankedAgentOptions,
	defaultAuthorizedAgentForRole,
	defaultProjectAgents,
	isLaunchableAgent,
	preferredAgentList,
	preferredAgentNeedsChecks,
	type RoleSession,
} from "./agent-select-options";

type Agent = components["schemas"]["AgentReadinessSnapshot"];

function agent(
	id: string,
	installation: Agent["installation"]["state"] = "installed",
	authentication: Agent["authentication"]["state"] = "authorized",
	usageCount = 0,
	lastUsedAt?: string,
): Agent {
	return {
		id,
		label: id === "claude-code" ? "Claude Code" : "Codex",
		installation: {
			state: installation,
			freshness: "fresh",
			checkedAt: null,
			attemptedAt: null,
			reasonCode: "test",
			reason: "test",
		},
		authentication: {
			state: authentication,
			freshness: "fresh",
			checkedAt: null,
			attemptedAt: null,
			reasonCode: "test",
			reason: "test",
		},
		effectiveReadiness: installation === "installed" && authentication === "authorized" ? "ready" : "unknown",
		usageCount,
		lastUsedAt,
	};
}

const priorityRank = new Map([
	["claude-code", 0],
	["codex", 1],
]);

describe("buildRankedAgentOptions", () => {
	it("ranks selectable agents by frequency before the static cold-start priority", () => {
		const agents = [
			agent("claude-code", "installed", "authorized", 1),
			agent("codex", "installed", "authorized", 4),
		];

		const options = buildRankedAgentOptions({
			agents,
			priorityRank,
			fallbackAgents: [],
		});

		expect(options.map((agent) => agent.id)).toEqual(["codex", "claude-code"]);
	});

	it("uses most recent usage to break frequency ties", () => {
		const agents = [
			agent("claude-code", "installed", "authorized", 2, "2026-08-18T10:00:00Z"),
			agent("codex", "installed", "authorized", 2, "2026-08-19T10:00:00Z"),
		];

		const options = buildRankedAgentOptions({
			agents,
			priorityRank,
			fallbackAgents: [],
		});

		expect(options.map((agent) => agent.id)).toEqual(["codex", "claude-code"]);
	});

	it("keeps unavailable agents below selectable agents regardless of usage", () => {
		const agents = [
			agent("claude-code", "installed", "authorized", 1),
			agent("codex", "not_installed", "unknown", 10),
		];

		const options = buildRankedAgentOptions({
			agents,
			priorityRank,
			fallbackAgents: [],
		});

		expect(options.map((agent) => agent.id)).toEqual(["claude-code", "codex"]);
	});

	it("allows unknown observations with warnings and blocks definite failures", () => {
		const options = buildRankedAgentOptions({
			agents: [
				agent("claude-code", "unknown", "unknown"),
				agent("codex", "installed", "unauthorized"),
			],
			priorityRank,
			fallbackAgents: [],
		});

		expect(options[0]).toMatchObject({ id: "claude-code", disabled: false, status: "Install unknown" });
		expect(options[1]).toMatchObject({ id: "codex", disabled: true, status: "Needs auth" });
	});

	it("keeps harnesses without installation checks selectable and launchable", () => {
		const unchecked = agent("codex", "unknown", "unknown");
		unchecked.installation.reasonCode = "install_check_unsupported";
		const options = buildRankedAgentOptions({ agents: [unchecked], priorityRank, fallbackAgents: [] });
		expect(options[0]).toMatchObject({ disabled: false, status: "Install unknown" });
		expect(isLaunchableAgent(unchecked)).toBe(true);
	});

	it("keeps stale known-good agents selectable while checking", () => {
		const knownGood = agent("codex");
		knownGood.installation.freshness = "checking";
		knownGood.authentication.freshness = "stale";

		const [option] = buildRankedAgentOptions({
			agents: [knownGood],
			priorityRank,
			fallbackAgents: [],
		});

		expect(option).toMatchObject({ disabled: false, status: "" });
	});

	it.each(["fresh", "stale", "checking"] as const)("omits unknown authentication badges while %s and keeps the agent launchable", (freshness) => {
		const unchecked = agent("codex", "installed", "unknown");
		unchecked.authentication.freshness = freshness;
		const [option] = buildRankedAgentOptions({ agents: [unchecked], priorityRank, fallbackAgents: [] });

		expect(option).toMatchObject({ disabled: false, status: "", statusTone: "muted" });
		expect(isLaunchableAgent(unchecked)).toBe(true);
	});
});

describe("configured but unverified credentials", () => {
	// The whole point of the "configured" state is that it must not look like
	// a working credential. Falling through to the known-good branch would
	// paint an unvalidated — possibly revoked — credential green.
	it.each(["mimo-code", "hermes", "claude-code"])("omits the verification badge for %s while keeping a neutral tone", (id) => {
		const [option] = buildRankedAgentOptions({
			agents: [agent(id, "installed", "configured")],
			priorityRank,
			fallbackAgents: [],
		});
		expect(option.statusTone).not.toBe("success");
		expect(option.status).toBe("");
	});

	// I1: never block. An unverified credential is not a disproved one.
	it("keeps an unverified agent selectable and ranked with unknown, not below it", () => {
		const options = buildRankedAgentOptions({
			agents: [
				agent("claude-code", "installed", "configured"),
				agent("codex", "installed", "unknown"),
			],
			priorityRank,
			fallbackAgents: [],
		});
		const unverified = options.find((option) => option.id === "claude-code")!;
		const unknown = options.find((option) => option.id === "codex")!;
		expect(unverified.disabled).toBe(false);
		expect(unverified.rank).toBe(unknown.rank);
	});

	it("still disables an agent the provider definitely rejected", () => {
		const [option] = buildRankedAgentOptions({
			agents: [agent("claude-code", "installed", "unauthorized")],
			priorityRank,
			fallbackAgents: [],
		});
		expect(option.disabled).toBe(true);
	});
});

describe("defaultAuthorizedAgentForRole", () => {
	const agents = [agent("claude-code"), agent("codex")];

	function hoursAgo(hours: number): string {
		return new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
	}

	function session(
		provider: RoleSession["provider"],
		kind: "worker" | "orchestrator" | undefined,
		createdAt: string,
		id = `${provider}-${kind ?? "unknown"}-${createdAt}`,
	): RoleSession {
		return { id, provider, kind, createdAt };
	}

	it("infers each role from its own history", () => {
		const sessions = [
			session("codex", "worker", hoursAgo(5)),
			session("codex", "worker", hoursAgo(4)),
			session("claude-code", "worker", hoursAgo(3)),
			session("claude-code", "orchestrator", hoursAgo(2)),
		];

		expect(defaultAuthorizedAgentForRole(agents, sessions, "worker")).toBe("codex");
		expect(defaultAuthorizedAgentForRole(agents, sessions, "orchestrator")).toBe("claude-code");
	});

	it("breaks equal counts by the newest session", () => {
		const sessions = [session("claude-code", "worker", hoursAgo(2)), session("codex", "worker", hoursAgo(1))];

		expect(defaultAuthorizedAgentForRole(agents, sessions, "worker")).toBe("codex");
	});

	it("ignores sessions older than 48 hours", () => {
		const sessions = [
			session("codex", "worker", hoursAgo(72)),
			session("codex", "worker", hoursAgo(100)),
			session("codex", "worker", hoursAgo(200)),
			session("claude-code", "worker", hoursAgo(1)),
		];

		expect(defaultAuthorizedAgentForRole(agents, sessions, "worker")).toBe("claude-code");
		expect(defaultAuthorizedAgentForRole(agents, [session("codex", "worker", hoursAgo(72))], "worker")).toBe(
			"claude-code",
		);
	});

	it("ignores sessions without timestamps", () => {
		expect(
			defaultAuthorizedAgentForRole(agents, [{ id: "w1", provider: "codex", kind: "worker" }], "worker"),
		).toBe("claude-code");
	});

	it("counts legacy orchestrator ids and kind-less sessions like the board does", () => {
		expect(
			defaultAuthorizedAgentForRole(
				agents,
				[{ id: "abc-orchestrator", provider: "codex", createdAt: hoursAgo(2) }],
				"orchestrator",
			),
		).toBe("codex");
		expect(
			defaultAuthorizedAgentForRole(
				agents,
				[session("codex", undefined, hoursAgo(2)), session("codex", undefined, hoursAgo(1))],
				"worker",
			),
		).toBe("codex");
	});

	it("skips unavailable historical winners and falls back to Claude Code", () => {
		const sessions = [
			session("goose", "worker", hoursAgo(3)),
			session("goose", "worker", hoursAgo(2)),
			session("codex", "worker", hoursAgo(1)),
		];

		expect(defaultAuthorizedAgentForRole(agents, sessions, "worker")).toBe("codex");
		expect(defaultAuthorizedAgentForRole(agents, [], "worker")).toBe("claude-code");
	});

	it("stays usable when Claude Code is unavailable", () => {
		expect(defaultAuthorizedAgentForRole([agent("codex")], [], "worker")).toBe("codex");
		expect(defaultAuthorizedAgentForRole([], [], "worker")).toBe("");
	});
});

describe("preferredAgentList", () => {
	it("orders ready agents by how well AO integrates them", () => {
		expect(preferredAgentList([agent("aider"), agent("codex"), agent("claude-code")])).toEqual([
			"claude-code",
			"codex",
			"aider",
		]);
	});

	it("puts installed, signed-in agents ahead of ones AO has not checked yet", () => {
		expect(preferredAgentList([agent("claude-code", "unknown", "unknown"), agent("codex")])).toEqual([
			"codex",
			"claude-code",
		]);
	});

	it("counts a configured credential as signed in", () => {
		expect(preferredAgentList([agent("claude-code", "installed", "unknown"), agent("codex", "installed", "configured")])).toEqual([
			"codex",
			"claude-code",
		]);
	});

	it("leaves out agents that are missing or signed out", () => {
		expect(
			preferredAgentList([agent("claude-code", "not_installed"), agent("codex", "installed", "unauthorized"), agent("cursor")]),
		).toEqual(["cursor"]);
	});

	it("ranks agents outside AO's priority list after the ones in it", () => {
		expect(preferredAgentList([agent("goose"), agent("aider")])).toEqual(["aider", "goose"]);
	});
});

describe("preferredAgentNeedsChecks", () => {
	it("waits for checks when nothing has been checked yet", () => {
		expect(preferredAgentNeedsChecks([agent("claude-code", "unknown", "unknown"), agent("codex", "unknown", "unknown")])).toBe(true);
	});

	it("waits for checks when a more preferred agent could still outrank the confirmed one", () => {
		expect(preferredAgentNeedsChecks([agent("claude-code", "unknown", "unknown"), agent("codex")])).toBe(true);
	});

	it("ranks right away once the most preferred usable agent is confirmed", () => {
		expect(preferredAgentNeedsChecks([agent("claude-code"), agent("codex", "unknown", "unknown")])).toBe(false);
		expect(preferredAgentNeedsChecks([agent("claude-code", "not_installed"), agent("codex", "installed", "configured")])).toBe(false);
		expect(preferredAgentNeedsChecks([])).toBe(false);
	});
});

describe("defaultProjectAgents", () => {
	it("starts every role on the most preferred agent", () => {
		expect(defaultProjectAgents([agent("claude-code", "not_installed"), agent("codex")])).toEqual({
			workerAgent: "codex",
			orchestratorAgent: "codex",
			reviewerAgent: "codex",
		});
	});

	it("reviews with the most preferred unattended-safe reviewer when the top agent is not one", () => {
		expect(defaultProjectAgents([agent("cursor"), agent("opencode"), agent("codex", "unknown", "unknown")])).toEqual({
			workerAgent: "cursor",
			orchestratorAgent: "cursor",
			reviewerAgent: "opencode",
		});
		// A ready reviewer outranks one AO has not checked yet.
		expect(defaultProjectAgents([agent("cursor"), agent("opencode", "unknown", "unknown"), agent("kimchi")]).reviewerAgent).toBe("kimchi");
	});

	it("reviews with Claude Code when no unattended-safe reviewer is usable", () => {
		expect(defaultProjectAgents([agent("cursor"), agent("codex", "not_installed")])).toEqual({
			workerAgent: "cursor",
			orchestratorAgent: "cursor",
			reviewerAgent: "claude-code",
		});
		expect(defaultProjectAgents([agent("goose")]).reviewerAgent).toBe("claude-code");
	});

	it("falls back to Claude Code when no agent is usable", () => {
		expect(defaultProjectAgents([])).toEqual({
			workerAgent: "claude-code",
			orchestratorAgent: "claude-code",
			reviewerAgent: "claude-code",
		});
	});
});
