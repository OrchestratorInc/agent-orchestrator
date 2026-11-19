import { describe, expect, it, vi } from "vitest";
import { agentConnectionState, cloudTaskTitle, connectedAgents, defaultCloudAgent, isAgentConnected } from "./agents";
import { cloudLifecycleStage, cloudSessionStatus, cloudStartupProblem } from "./lifecycle";
import { defaultOrganizationName, resolveOrganization, selectActiveOrganization } from "./org";
import type { CloudMe, CloudOrganization, CloudProviderConnection, CloudSession } from "./types";

const org = (id: string, kind?: "b2b" | "b2c"): CloudOrganization => ({ id, slug: id, displayName: id, role: "owner", ...(kind ? { offering: { providers: [], defaultProvider: "", billingMode: "off", kind, features: [] } } : {}) });
const me = (organizations: CloudOrganization[], activeOrganizationId?: string): CloudMe => ({ user: { id: "u", email: "ada@example.com", displayName: "" }, organizations, activeOrganizationId });

describe("organization", () => {
	it("chooses the reported active org, then a team org, then the first", () => {
		expect(selectActiveOrganization(me([org("a"), org("b")], "b"))?.id).toBe("b");
		expect(selectActiveOrganization(me([org("a", "b2c"), org("t", "b2b")]))?.id).toBe("t");
		expect(selectActiveOrganization(me([org("a"), org("b")], "gone"))?.id).toBe("a");
		expect(selectActiveOrganization(me([]))).toBeNull();
	});

	it("creates a workspace on a first sign-in, named like the desktop names it", async () => {
		const createOrganization = vi.fn(async (displayName: string) => ({ organization: org(displayName) }));
		await expect(resolveOrganization({ createOrganization }, me([]))).resolves.toMatchObject({ id: "ada" });
		expect(createOrganization).toHaveBeenCalledWith("ada");
		expect(defaultOrganizationName({ user: { id: "u", email: "x@y", displayName: "  Ada Lovelace " } })).toBe("Ada Lovelace");
	});
});

const connection = (provider: string, validationState: string, label = "default"): CloudProviderConnection => ({
	id: provider, provider, label, config: {}, validationState, createdAt: "", updatedAt: "",
});

describe("agent connections", () => {
	it("counts only a valid personal default credential", () => {
		const connections = [connection("codex", "valid"), connection("claude-code", "invalid"), connection("cursor", "valid", "other")];
		expect(isAgentConnected(connections, "codex")).toBe(true);
		expect(agentConnectionState(connections, "claude-code")).toBe("invalid");
		expect(agentConnectionState(connections, "cursor")).toBe("none");
		expect(connectedAgents(connections)).toEqual(["codex"]);
	});

	it("defaults to the preferred agent when connected, else the desktop's order", () => {
		const connections = [connection("claude-code", "valid"), connection("opencode", "valid")];
		expect(defaultCloudAgent(connections, "opencode")).toBe("opencode");
		expect(defaultCloudAgent(connections, "codex")).toBe("claude-code");
		expect(defaultCloudAgent([], "codex")).toBeUndefined();
	});
});

const cloudSession = (overrides: Partial<CloudSession>): CloudSession => ({
	id: "s", orgId: "o", projectId: "p", kind: "worker", harness: "codex", displayName: "", branch: "", mode: "trusted", interfaceMode: "chat",
	activityState: "idle", status: "idle", runtimeConnected: true, isTerminated: false, createdAt: "", updatedAt: "2026-01-01", ...overrides,
});

describe("session lifecycle", () => {
	it("names sandbox stages from desired and observed state", () => {
		expect(cloudLifecycleStage(cloudSession({ desiredState: "paused", observedState: "stopped", runtimeConnected: false }))).toBe("paused");
		expect(cloudLifecycleStage(cloudSession({ desiredState: "running", observedState: "restoring", runtimeConnected: false }))).toBe("resuming");
		expect(cloudLifecycleStage(cloudSession({ observedState: "provisioning", runtimeConnected: false }))).toBe("provisioning");
		expect(cloudLifecycleStage(cloudSession({ observedState: "bootstrapping", runtimeConnected: false }))).toBe("starting");
		expect(cloudLifecycleStage(cloudSession({ observedState: "running" }))).toBe("connected");
		expect(cloudLifecycleStage(cloudSession({ isTerminated: true }))).toBe("archived");
	});

	it("reports a final start failure apart from one still being retried", () => {
		expect(cloudStartupProblem(cloudSession({ runtimeConnected: false, runtimeState: "terminated", startupError: { code: "worker_never_started", message: "Gave up", at: "" } }))).toEqual({ phase: "failed", message: "Gave up", detail: undefined });
		expect(cloudStartupProblem(cloudSession({ runtimeConnected: false, runtimeState: "failed", startupError: { code: "bootstrap_failed", message: "Retrying", at: "" } }))).toEqual({ phase: "retrying", message: "Retrying" });
		// A workspace error the worker reported after connecting is final even while connected.
		expect(cloudStartupProblem(cloudSession({ startupError: { code: "project_setup_failed", message: "npm ci failed", at: "" } }))?.phase).toBe("failed");
	});

	it("names the session's status for its header", () => {
		expect(cloudSessionStatus(cloudSession({ status: "needs_input" }))).toEqual({ label: "Needs input", tone: "attention" });
		expect(cloudSessionStatus(cloudSession({ desiredState: "paused", observedState: "stopped", runtimeConnected: false }))).toEqual({ label: "Paused", tone: "idle" });
	});
});

describe("task titles", () => {
	it("uses the prompt's first line, capped at the contract's 80 characters", () => {
		expect(cloudTaskTitle("  Fix login\nand add tests", "Codex")).toBe("Fix login");
		expect(cloudTaskTitle("", "Codex")).toBe("Codex");
		expect(cloudTaskTitle("x".repeat(120), "Codex")).toHaveLength(80);
	});
});
