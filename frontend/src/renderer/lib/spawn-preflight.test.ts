import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import {
	checkSpawnPreflight,
	runPreflightInstall,
	setWorkspaceRepoDefaultBranch,
	ensureSpawnPreflight,
	isSpawnPreflightCancelled,
	setProjectDefaultBranch,
	setProjectOrchestratorAgent,
	useSpawnPreflightStore,
	type SpawnPreflightReport,
} from "./spawn-preflight";

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PUT: vi.fn() },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

const post = vi.mocked(apiClient.POST);
const get = vi.mocked(apiClient.GET);
const put = vi.mocked(apiClient.PUT);

const ready: SpawnPreflightReport = { ready: true, canProceed: true, kind: "orchestrator", harness: "claude-code", checks: [] };
const blocked: SpawnPreflightReport = {
	ready: false,
	canProceed: false,
	kind: "orchestrator",
	harness: "codex",
	checks: [{ code: "agent_not_installed", blocking: true, command: "npm install -g @openai/codex" }],
};

function respond(report: unknown) {
	post.mockResolvedValueOnce({ data: report, error: undefined, response: new Response() } as never);
}

beforeEach(() => {
	vi.clearAllMocks();
	useSpawnPreflightStore.setState({ pending: null });
});

describe("ensureSpawnPreflight", () => {
	it("resolves at once with the resolved harness when every check passes", async () => {
		respond(ready);
		await expect(ensureSpawnPreflight({ projectId: "p1", kind: "orchestrator" })).resolves.toEqual({ harness: "claude-code" });
		expect(post).toHaveBeenCalledWith("/api/v1/sessions/preflight", { body: { projectId: "p1", kind: "orchestrator" } });
		expect(useSpawnPreflightStore.getState().pending).toBeNull();
	});

	it("never checks a remote host", async () => {
		await expect(ensureSpawnPreflight({ projectId: "p1", kind: "worker", harness: "codex", hostId: "box-b" })).resolves.toEqual({ harness: "codex" });
		expect(post).not.toHaveBeenCalled();
	});

	it("spawns unchecked when the pre-flight cannot run or answers malformed", async () => {
		post.mockRejectedValueOnce(new Error("offline"));
		await expect(ensureSpawnPreflight({ projectId: "p1", kind: "worker", harness: "codex" })).resolves.toEqual({ harness: "codex" });
		post.mockResolvedValueOnce({ data: undefined, error: { code: "NOT_IMPLEMENTED" }, response: new Response() } as never);
		await expect(ensureSpawnPreflight({ projectId: "p1", kind: "worker" })).resolves.toEqual({ harness: undefined });
		respond({ ready: false });
		await expect(ensureSpawnPreflight({ projectId: "p1", kind: "worker" })).resolves.toEqual({ harness: undefined });
		expect(useSpawnPreflightStore.getState().pending).toBeNull();
	});

	it("waits on the fix dialog when blocked and resolves once it settles", async () => {
		respond(blocked);
		const outcome = ensureSpawnPreflight({ projectId: "p1", kind: "orchestrator" });
		await vi.waitFor(() => expect(useSpawnPreflightStore.getState().pending?.report).toEqual(blocked));
		const pending = useSpawnPreflightStore.getState().pending!;
		useSpawnPreflightStore.getState().settle(pending.id, { harness: "claude-code" });
		await expect(outcome).resolves.toEqual({ harness: "claude-code" });
		expect(useSpawnPreflightStore.getState().pending).toBeNull();
	});

	it("rejects with a cancellation when the dialog is dismissed or replaced", async () => {
		respond(blocked);
		const first = ensureSpawnPreflight({ projectId: "p1", kind: "orchestrator" });
		await vi.waitFor(() => expect(useSpawnPreflightStore.getState().pending).not.toBeNull());
		respond(blocked);
		const second = ensureSpawnPreflight({ projectId: "p2", kind: "orchestrator" });
		const firstError = await first.catch((error: unknown) => error);
		expect(isSpawnPreflightCancelled(firstError)).toBe(true);
		await vi.waitFor(() => expect(useSpawnPreflightStore.getState().pending?.input.projectId).toBe("p2"));
		useSpawnPreflightStore.getState().settle(useSpawnPreflightStore.getState().pending!.id, null);
		expect(isSpawnPreflightCancelled(await second.catch((error: unknown) => error))).toBe(true);
	});
});

describe("useSpawnPreflightStore", () => {
	it("ignores updates and settlements for a launch that was replaced", () => {
		const first = { resolve: vi.fn(), reject: vi.fn() };
		const second = { resolve: vi.fn(), reject: vi.fn() };
		const store = useSpawnPreflightStore.getState();
		const firstId = store.open({ input: { projectId: "a", kind: "orchestrator" }, report: blocked, ...first });
		const secondId = store.open({ input: { projectId: "b", kind: "orchestrator" }, report: blocked, ...second });
		expect(first.reject).toHaveBeenCalledOnce();
		expect(store.isCurrent(firstId)).toBe(false);

		store.update(firstId, ready, { projectId: "a", kind: "worker" });
		store.settle(firstId, { harness: "codex" });
		expect(useSpawnPreflightStore.getState().pending).toMatchObject({ id: secondId, report: blocked, input: { projectId: "b" } });
		expect(second.resolve).not.toHaveBeenCalled();
		expect(first.resolve).not.toHaveBeenCalled();

		store.settle(secondId, { harness: "claude-code" });
		expect(second.resolve).toHaveBeenCalledWith({ harness: "claude-code" });
		// A settled launch cannot be settled again.
		store.settle(secondId, null);
		expect(second.reject).not.toHaveBeenCalled();
	});
});

describe("checkSpawnPreflight", () => {
	it("reports the previous blockers and the fix the user applied", async () => {
		respond(ready);
		await checkSpawnPreflight(
			{ projectId: "p1", kind: "worker", harness: "codex", mode: "tui" },
			{ blockers: ["agent_not_installed"], method: "install" },
		);
		expect(post).toHaveBeenCalledWith("/api/v1/sessions/preflight", {
			body: {
				projectId: "p1",
				kind: "worker",
				harness: "codex",
				mode: "tui",
				resolution: { blockers: ["agent_not_installed"], method: "install" },
			},
		});
	});
});

describe("project config fixes", () => {
	const project = {
		status: "ok",
		project: {
			id: "p1",
			config: {
				defaultBranch: "auto",
				orchestrator: { agent: "codex", agentConfig: { model: "gpt-5", mode: "fast", permissions: "auto" } },
				worker: { agent: "codex" },
			},
		},
	};

	beforeEach(() => {
		get.mockResolvedValue({ data: project, error: undefined, response: new Response() } as never);
		put.mockResolvedValue({ data: {}, error: undefined, response: new Response() } as never);
	});

	it("switches the orchestrator agent, keeping only harness-neutral permissions", async () => {
		await setProjectOrchestratorAgent("p1", "claude-code");
		expect(put).toHaveBeenCalledWith("/api/v1/projects/{id}/config", {
			params: { path: { id: "p1" } },
			body: {
				config: {
					defaultBranch: "auto",
					orchestrator: { agent: "claude-code", agentConfig: { permissions: "auto" } },
					worker: { agent: "codex" },
				},
			},
		});
	});

	it("sets the default branch without touching other config", async () => {
		await setProjectDefaultBranch("p1", "trunk");
		const body = put.mock.calls[0]?.[1] as { body: { config: Record<string, unknown> } };
		expect(body.body.config).toEqual({ ...project.project.config, defaultBranch: "trunk" });
	});

	it("surfaces a failed save", async () => {
		put.mockResolvedValueOnce({ data: undefined, error: { code: "INVALID" }, response: new Response() } as never);
		await expect(setProjectDefaultBranch("p1", "trunk")).rejects.toThrow("Could not save the project");
	});
});

describe("runPreflightInstall", () => {
	it("installs an agent with the recommended method and polls until the job ends", async () => {
		post.mockResolvedValueOnce({ data: { target: "codex", status: "installing" }, error: undefined, response: new Response() } as never);
		get
			.mockResolvedValueOnce({ data: undefined, error: { code: "TRANSIENT" }, response: new Response() } as never)
			.mockResolvedValueOnce({ data: { target: "codex", status: "verifying" }, error: undefined, response: new Response() } as never)
			.mockResolvedValueOnce({ data: { target: "codex", status: "succeeded" }, error: undefined, response: new Response() } as never);
		const job = await runPreflightInstall({ code: "agent_not_installed" }, "codex", { intervalMs: 0 });
		expect(job.status).toBe("succeeded");
		expect(post).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } }, body: { operation: "install" } });
		expect(get).toHaveBeenCalledTimes(3);
		expect(get).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", { params: { path: { agent: "codex" } } });
	});

	it("installs tmux through the system install route", async () => {
		post.mockResolvedValueOnce({ data: { target: "tmux", status: "succeeded" }, error: undefined, response: new Response() } as never);
		await expect(runPreflightInstall({ code: "runtime_prerequisite_missing" }, "claude-code")).resolves.toMatchObject({ status: "succeeded" });
		expect(post).toHaveBeenCalledWith("/api/v1/system/install/{target}", { params: { path: { target: "tmux" } } });
		expect(get).not.toHaveBeenCalled();
	});

	it("rejects when the install cannot start or there is nothing to install", async () => {
		post.mockResolvedValueOnce({ data: undefined, error: { code: "INSTALL_UNSUPPORTED" }, response: new Response() } as never);
		await expect(runPreflightInstall({ code: "agent_not_installed" }, "codex")).rejects.toThrow();
		await expect(runPreflightInstall({ code: "default_branch_unresolved" }, "codex")).rejects.toThrow("Nothing to install");
	});
});

describe("setWorkspaceRepoDefaultBranch", () => {
	it("writes the child repo's own branch", async () => {
		put.mockResolvedValueOnce({ data: { project: { id: "ws" } }, error: undefined, response: new Response() } as never);
		await setWorkspaceRepoDefaultBranch("ws", "web", "trunk");
		expect(put).toHaveBeenCalledWith("/api/v1/projects/{id}/workspace-repos/{name}/default-branch", {
			params: { path: { id: "ws", name: "web" } },
			body: { defaultBranch: "trunk" },
		});
	});

	it("rejects when the daemon refuses the branch", async () => {
		put.mockResolvedValueOnce({ data: undefined, error: { code: "INVALID_BRANCH", message: "bad" }, response: new Response() } as never);
		await expect(setWorkspaceRepoDefaultBranch("ws", "web", "bad..name")).rejects.toThrow();
	});
});
