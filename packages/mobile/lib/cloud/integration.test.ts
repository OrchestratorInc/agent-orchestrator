import { describe, expect, it, vi } from "vitest";
import { boardZoneOf, isArchived } from "../agentsView";
import { rankAgents } from "../agentPicker";
import { hostedProjectSections } from "../hostedRows";
import { workerContextActions } from "../worker-action-model";
import { archiveCloudSession, launchCloudOrchestrator, renameCloudSession, resumeCloudAgent, setCloudSessionPinned, spawnCloudWorker, unsupportedCloudAction, type CloudActionContext } from "./actions";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import { CLOUD_HOST_ID, cloudPullRequest, cloudSessionRow, createCloudEnvironment, isCloudHost } from "./mapping";
import { cloudAgentCatalog, cloudModelCatalog, cloudProjectDetail } from "./spawnSource";
import type { CloudProject, CloudProviderConnection, CloudSession } from "./types";

const session = (overrides: Partial<CloudSession> = {}): CloudSession => ({
	id: "s1", orgId: "o", projectId: "p1", kind: "worker", harness: "codex", displayName: "Fix login", branch: "ao/fix",
	mode: "trusted", interfaceMode: "chat", activityState: "active", status: "working", runtimeConnected: true,
	desiredState: "running", observedState: "running", isTerminated: false, createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-02T00:00:00Z", ...overrides,
});
const project = (overrides: Partial<CloudProject> = {}): CloudProject => ({
	id: "p1", orgId: "o", displayName: "web", repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
	config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } }, createdAt: "", updatedAt: "", ...overrides,
});
const connection = (provider: string): CloudProviderConnection => ({ id: provider, provider, label: "default", config: {}, validationState: "valid", createdAt: "", updatedAt: "" });

describe("cloud rows on the shared screens", () => {
	it("maps a cloud session onto a board row the board groups like a machine's", () => {
		const waiting = cloudSessionRow(session({ status: "needs_input", prs: [{ url: "https://github.com/acme/web/pull/7", number: 7, state: "draft", ci: "failing", review: "review_required", mergeability: "blocked", updatedAt: "" }] }));
		expect(waiting).toMatchObject({ id: "s1", projectId: "p1", status: "needs_input", harness: "codex", mode: "chat", displayName: "Fix login", isTerminated: false });
		expect(waiting.pr).toMatchObject({ number: 7, isDraft: true, ciStatus: "failing", reviewDecision: "pending", mergeability: { state: "blocked", mergeable: false } });
		expect(boardZoneOf(waiting)).toBe("needs_you");
		expect(isArchived(cloudSessionRow(session({ isTerminated: true, status: "terminated" })))).toBe(true);
	});

	it("says what a session not yet usable is waiting on", () => {
		expect(cloudSessionRow(session({ desiredState: "paused", observedState: "stopped", runtimeConnected: false })).displayStatus).toBe("Paused");
		expect(cloudSessionRow(session({ runtimeConnected: false, runtimeState: "terminated", startupError: { code: "worker_never_started", message: "Gave up", at: "" } })).displayStatus).toBe("Failed to start");
		expect(cloudSessionRow(session()).displayStatus).toBeNull();
	});

	it("lists orchestrators apart from workers and keeps unchanged rows identical between polls", () => {
		const build = createCloudEnvironment();
		const orchestrator = session({ id: "o1", kind: "orchestrator", displayName: "Orchestrator", interfaceMode: "tui" });
		const first = build([project()], [session(), orchestrator, session({ id: "gone", kind: "orchestrator", isTerminated: true })]);
		expect(first.hostId).toBe(CLOUD_HOST_ID);
		expect(first.appearance).toEqual({ symbol: "cloud" });
		expect(first.sessions.map((row) => row.id)).toEqual(["s1"]);
		expect(first.orchestrators).toEqual([expect.objectContaining({ id: "o1", projectName: "web", mode: "tui", hasRuntime: true })]);
		const second = build([project()], [{ ...session() }, { ...orchestrator }]);
		expect(second.sessions[0]).toBe(first.sessions[0]);
		expect(second.projects[0]).toBe(first.projects[0]);
		const changed = build([project()], [session({ status: "needs_input" })]);
		expect(changed.sessions[0]).not.toBe(first.sessions[0]);
	});

	it("puts cloud projects in the Projects tab's sections, marked with the cloud environment", () => {
		const cloud = createCloudEnvironment()([project()], [session()]);
		const rows = hostedProjectSections([cloud]).flatMap((section) => section.data);
		expect(rows).toEqual([expect.objectContaining({ project: expect.objectContaining({ id: "p1", name: "web", hostId: CLOUD_HOST_ID, hostName: "Cloud", hostAppearance: { symbol: "cloud" } }) })]);
	});

	it("maps the PR summary like the daemon's", () => {
		expect(cloudPullRequest({ url: "u", number: 1, state: "merged", ci: "passing", review: "approved", mergeability: "mergeable", updatedAt: "" }))
			.toMatchObject({ state: "merged", ciStatus: "passing", reviewDecision: "approved", mergeability: { mergeable: true } });
		expect(isCloudHost(CLOUD_HOST_ID)).toBe(true);
		expect(isCloudHost("h_123")).toBe(false);
	});

	it("offers pin and rename on a cloud row as on a local one", () => {
		const ids = (cloud: boolean, pinned = false) => workerContextActions({ pinned, terminated: false, stopped: false, hasPr: true, cloud }).map((action) => action.id);
		expect(ids(true)).toEqual(["open", "pin", "rename", "openPr", "delete"]);
		expect(ids(true)).toEqual(ids(false));
		expect(ids(true, true)).toContain("unpin");
	});

	it("carries the person's pin onto the board row", () => {
		expect(cloudSessionRow(session({ pinned: true, pinnedAt: "2026-11-05T10:00:00Z" }))).toMatchObject({ isPinned: true, pinnedAt: "2026-11-05T10:00:00Z" });
		expect(cloudSessionRow(session())).toMatchObject({ isPinned: false, pinnedAt: null });
	});
});

function context(overrides: Partial<CloudActionContext> = {}, client: Partial<CloudClient> = {}): CloudActionContext {
	return {
		client: client as CloudClient,
		org: { id: "o", slug: "o", displayName: "Acme", role: "owner" },
		projects: [project()],
		connections: [connection("claude-code"), connection("codex")],
		chatHarnesses: ["claude-code", "codex", "cursor"],
		upsertSession: vi.fn(),
		refreshSessions: vi.fn(async () => {}),
		...overrides,
	};
}

describe("board actions on cloud rows", () => {
	it("spawns a worker with the sheet's request id, Chat where the agent supports it", async () => {
		const createSession = vi.fn(async (..._args: unknown[]) => ({ session: session({ id: "new" }) }));
		const ctx = context({}, { createSession: createSession as never });
		const row = await spawnCloudWorker(ctx, { projectId: "p1", prompt: "Fix the login bug\nand add a test", harness: "codex", mode: "chat", clientRequestId: "req-1" });
		expect(createSession).toHaveBeenCalledWith("o", { projectId: "p1", kind: "worker", harness: "codex", displayName: "Fix the login bug", prompt: "Fix the login bug\nand add a test", interfaceMode: "chat" }, "req-1");
		expect(row.id).toBe("new");
		expect(ctx.upsertSession).toHaveBeenCalled();
	});

	it("defaults to the project's worker agent and to the terminal for an agent without Chat", async () => {
		const createSession = vi.fn(async (..._args: unknown[]) => ({ session: session() }));
		await spawnCloudWorker(context({ connections: [connection("opencode"), connection("claude-code")] }, { createSession: createSession as never }), { projectId: "p1", prompt: "x" });
		expect(createSession.mock.calls[0][1]).toMatchObject({ harness: "claude-code", interfaceMode: "chat" });
		await spawnCloudWorker(context({}, { createSession: createSession as never }), { projectId: "p1", prompt: "x", harness: "opencode" });
		expect(createSession.mock.calls[1][1]).toMatchObject({ harness: "opencode", interfaceMode: "tui" });
	});

	it("retries without interfaceMode on a control plane that refuses it", async () => {
		const createSession = vi.fn()
			.mockRejectedValueOnce(new CloudError("bad", { status: 400, code: "invalid_request" }))
			.mockResolvedValueOnce({ session: session() });
		await spawnCloudWorker(context({}, { createSession: createSession as never }), { projectId: "p1", prompt: "x", harness: "codex", clientRequestId: "k" });
		expect(createSession.mock.calls[1][1]).not.toHaveProperty("interfaceMode");
		expect(createSession.mock.calls[1][2]).toBe("k:legacy");
	});

	it("refuses what a cloud task cannot take, in words a person can act on", async () => {
		await expect(spawnCloudWorker(context(), { projectId: "p1", attachments: [{}] })).rejects.toThrow(/attachments/);
		await expect(spawnCloudWorker(context(), {})).rejects.toThrow(/cloud project/);
		await expect(spawnCloudWorker(context({ connections: [] }), { projectId: "p1" })).rejects.toThrow(/Connect a coding agent/);
		await expect(spawnCloudWorker(context({ org: null }), { projectId: "p1" })).rejects.toThrow(/Sign in/);
		expect(() => unsupportedCloudAction("merge")).toThrow(/GitHub/);
	});

	it("renames through the control plane and keeps its answer", async () => {
		const renamed = session({ displayName: "Fix the login" });
		const renameSession = vi.fn(async () => ({ session: renamed }));
		const upsertSession = vi.fn();
		await renameCloudSession(context({ upsertSession }, { renameSession: renameSession as never }), "s1", "Fix the login");
		expect(renameSession).toHaveBeenCalledWith("o", "s1", "Fix the login");
		expect(upsertSession).toHaveBeenCalledWith(renamed);
	});

	it("moves a pinned row at once, and back when the control plane refuses", async () => {
		const listed = session();
		const upsertSession = vi.fn();
		const pinSession = vi.fn(async () => ({ sessionId: listed.id, pinned: true, pinnedAt: "2026-11-05T10:00:00Z" }));
		await setCloudSessionPinned(context({ sessions: [listed], upsertSession }, { pinSession: pinSession as never }), listed.id, true);
		expect(upsertSession.mock.calls[0][0]).toMatchObject({ pinned: true });
		expect(upsertSession.mock.calls.at(-1)?.[0]).toMatchObject({ pinned: true, pinnedAt: "2026-11-05T10:00:00Z" });

		upsertSession.mockClear();
		const unpinSession = vi.fn(async () => { throw new CloudError("Not a member.", { status: 403, code: "forbidden" }); });
		const pinnedRow = session({ pinned: true, pinnedAt: "2026-11-05T10:00:00Z" });
		await expect(setCloudSessionPinned(context({ sessions: [pinnedRow], upsertSession }, { unpinSession: unpinSession as never }), pinnedRow.id, false)).rejects.toThrow("Not a member.");
		expect(upsertSession.mock.calls[0][0]).toMatchObject({ pinned: false });
		expect(upsertSession.mock.calls.at(-1)?.[0]).toBe(pinnedRow);
	});

	it("turns a control-plane refusal into its message, never a pairing complaint", async () => {
		const deleteSession = vi.fn(async () => { throw new CloudError("Not a member.", { status: 403, code: "forbidden" }); });
		const failure = await archiveCloudSession(context({}, { deleteSession: deleteSession as never }), "s1").catch((error: unknown) => error);
		expect(failure).toBeInstanceOf(Error);
		expect(failure).not.toBeInstanceOf(CloudError);
		expect((failure as Error).message).toBe("Not a member.");
		expect(failure).not.toHaveProperty("status");
	});

	// The board's Resume agent, for an agent that exited or crashed in a live
	// session: waking the sandbox alone left the agent stopped.
	it("resumes an exited agent by restarting it in its woken sandbox", async () => {
		const calls: string[] = [];
		const resumeSession = vi.fn(async () => { calls.push("wake"); return { session: { id: "s1" } }; });
		const restartSessionAgent = vi.fn(async () => { calls.push("restart"); return { requested: true }; });
		const ctx = context({}, { resumeSession: resumeSession as never, restartSessionAgent: restartSessionAgent as never });
		await resumeCloudAgent(ctx, "s1");
		expect(calls).toEqual(["wake", "restart"]);
		expect(restartSessionAgent).toHaveBeenCalledWith("o", "s1");
		expect(ctx.refreshSessions).toHaveBeenCalled();

		const running = context({}, { resumeSession: resumeSession as never, restartSessionAgent: vi.fn(async () => ({ requested: false })) as never });
		await expect(resumeCloudAgent(running, "s1")).rejects.toThrow("The agent is already running.");
		expect(running.refreshSessions).toHaveBeenCalled();
	});

	it("starts a project's orchestrator with its configured agent", async () => {
		const createSession = vi.fn(async (..._args: unknown[]) => ({ session: session({ id: "o1", kind: "orchestrator" }) }));
		const link = await launchCloudOrchestrator(context({}, { createSession: createSession as never }), "p1", "chat");
		expect(createSession.mock.calls[0][1]).toEqual({ projectId: "p1", kind: "orchestrator", harness: "codex", displayName: "Orchestrator", prompt: "", interfaceMode: "chat" });
		expect(link).toMatchObject({ id: "o1", projectName: "web" });
	});
});

describe("spawn sheet sources", () => {
	it("offers only the agents this account has connected", () => {
		const catalog = cloudAgentCatalog([connection("codex"), connection("claude-code")]);
		expect(catalog.supported.map((agent) => agent.id)).toEqual(["claude-code", "codex", "cursor", "opencode"]);
		expect(catalog.authorized.map((agent) => agent.id)).toEqual(["claude-code", "codex"]);
		// The picker lists selectable agents only: an unconnected one is not offered.
		expect(rankAgents(catalog).filter((agent) => agent.selectable).map((agent) => agent.id)).toEqual(["claude-code", "codex"]);
		expect(rankAgents(catalog).find((agent) => agent.id === "cursor")).toMatchObject({ availability: "needs-auth", selectable: false });
	});

	it("reads a project's worker defaults and the agent's models", () => {
		expect(cloudProjectDetail(project({ config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } } }))).toMatchObject({ agent: "codex", config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } } });
		expect(cloudModelCatalog("codex", { models: [{ id: "m", displayName: "Model", default: true }] }, new Date(0))).toMatchObject({
			agentId: "codex", selectionMode: "catalog", models: [{ id: "m", label: "Model", isDefault: true }],
		});
	});
});
