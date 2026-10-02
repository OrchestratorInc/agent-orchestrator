import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { NewTaskDialog } from "./NewTaskDialog";

const { deleteMock, getMock, postMock, ensureAgentReadinessMock } = vi.hoisted(() => ({
	deleteMock: vi.fn(),
	getMock: vi.fn(),
	postMock: vi.fn(),
	ensureAgentReadinessMock: vi.fn(),
}));

vi.mock("../hooks/useAgentReadinessQuery", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentReadinessQuery")>();
	return { ...actual, useEnsureAgentReadiness: ensureAgentReadinessMock };
});

vi.mock("../lib/api-client", () => ({
	apiClient: {
		DELETE: (...args: unknown[]) => deleteMock(...args),
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (typeof error === "object" && error !== null && "message" in error) {
			const body = error as { code?: unknown; message: unknown };
			const message = String(body.message);
			return typeof body.code === "string" && body.code !== "" ? `${message} (${body.code})` : message;
		}
		return fallback;
	},
	apiErrorCode: (error: unknown) =>
		typeof error === "object" && error !== null && "code" in error
			? String((error as { code: unknown }).code)
			: undefined,
}));

function renderDialog(client = new QueryClient()) {
	const onCreated = vi.fn();
	const onOpenChange = vi.fn();
	const view = render(
		<QueryClientProvider client={client}>
			<NewTaskDialog open projectId="proj-1" onCreated={onCreated} onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return { ...view, onCreated, onOpenChange };
}

function requestBody() {
	const call = postMock.mock.calls.find(([path]) => path === "/api/v1/orchestrators/delegate");
	if (!call) throw new Error("delegate was never called");
	return (call[1] as { body: Record<string, unknown> }).body;
}

function delegateCalls() {
	return postMock.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate");
}

async function openAccountMenu(user: ReturnType<typeof userEvent.setup>) {
	const trigger = await screen.findByRole("button", { name: "Initial account" });
	await waitFor(() => expect(trigger).toBeEnabled());
	await user.click(trigger);
	return trigger;
}

async function chooseAccount(user: ReturnType<typeof userEvent.setup>, name: string) {
	await openAccountMenu(user);
	const option = await screen.findByRole("menuitem", { name });
	await waitFor(() => expect(option).not.toHaveAttribute("aria-disabled", "true"));
	await user.click(option);
}

const agentInventory = {
	agents: [
		agentReadiness("claude-code", "Claude Code"),
		agentReadiness("cursor", "Cursor"),
		agentReadiness("kiro", "Kiro", { authentication: "unknown" }),
	],
};

const directModelCatalog = {
	agentId: "claude-code",
	models: [],
	selectionMode: "catalog",
	allowCustom: true,
	customModelEntry: "direct",
	source: "command",
	fetchedAt: "2026-08-31T00:00:00Z",
	refreshRecommended: false,
	stale: false,
};

async function waitForAgentCatalog() {
	await waitFor(() => expect(screen.getAllByText("Claude Code").length).toBeGreaterThan(0));
}

beforeEach(() => {
	window.localStorage.removeItem("ao.taskComposer.preferences.v1");
	ensureAgentReadinessMock.mockReset();
	deleteMock.mockReset().mockResolvedValue({ data: undefined, error: undefined });
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/sessions/account-selection") {
			return { data: { initialSelection: false }, error: undefined };
		}
		if (path === "/api/v1/agents/readiness") {
			return { data: agentInventory, error: undefined };
		}
		if (path === "/api/v1/agents/{agent}/models") {
			return { data: directModelCatalog, error: undefined };
		}
		return {
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "careerops",
					repo: "github.com/team/careerops",
					defaultBranch: "main",
					path: "/work/careerops",
					workspaceRepos: [{ name: "api", relativePath: "api", repo: "github.com/team/careerops-api" }],
					config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } },
				},
			},
			error: undefined,
		};
	});
	postMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents/readiness/ensure") return { data: agentInventory, error: undefined };
		if (path === "/api/v1/projects/{id}/tasks/prepare") {
			return { data: { ok: true, taskPreparation: "prep-token" }, error: undefined };
		}
		return { data: { ok: true, workerId: "worker-1", orchestratorId: "orch-1" }, error: undefined };
	});
});

afterEach(() => vi.restoreAllMocks());

function managedAccountDiscovery(defaultAgent = "cursor") {
	const capability = { initialSelection: true };
	const agents = { agents: [agentReadiness("cursor", "Cursor"), agentReadiness("codex", "Codex", { authentication: "unauthorized" })] };
	const inventory = { revision: 1, availability: "ready", stale: false, routing: [], oauthSessions: [], accounts: [
		{ id: "account-a", provider: "codex", label: "Work", status: "active", verification: "verified", disabled: false, unavailable: false, quotaSupported: false },
	] };
	const fallback = getMock.getMockImplementation();
	getMock.mockImplementation(async (path: string, options?: unknown) => {
		if (path === "/api/v1/sessions/account-selection") return { data: capability };
		if (path === "/api/v1/accounts-manager/accounts") return { data: inventory };
		if (path === "/api/v1/accounts-manager/accounts/{accountId}/models") return { data: { models: [{ id: "explicit-model", displayName: "Managed model" }] } };
		if (path === "/api/v1/agents/readiness") return { data: agents };
		if (path === "/api/v1/projects/{id}") return { data: { status: "ok", project: { id: "proj-1", name: "scratch", config: { worker: { agent: defaultAgent } } } } };
		return fallback?.(path, options);
	});
	return { agents, inventory, capability };
}

describe("managed account discovery", () => {
	it("offers an installed harness without a device login and preserves an explicit account choice", async () => {
		const { agents } = managedAccountDiscovery();
		const { onCreated } = renderDialog();
		const user = userEvent.setup();
		await waitFor(() => expect(screen.getByRole("button", { name: "Agent" })).toHaveTextContent("Cursor"));
		await user.click(screen.getByRole("button", { name: "Agent" }));
		await user.click(await screen.findByRole("menuitem", { name: /Codex/ }));
		const picker = await screen.findByRole("button", { name: "Initial account" });
		expect(picker).toHaveTextContent(/^Account$/);
		await user.type(screen.getByLabelText("Task"), "Use the selected account");
		expect(screen.getByRole("button", { name: "Start task" })).toBeDisabled();
		await chooseAccount(user, "Work (account-a)");
		await user.click(await screen.findByRole("button", { name: "Model" }));
		await user.click(await screen.findByRole("menuitem", { name: "Managed model" }));
		await user.click(screen.getByRole("button", { name: "Start task" }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("worker-1"));
		expect(requestBody()).toMatchObject({ agent: "codex", account: { mode: "managed", accountId: "account-a" } });
		expect(agents.agents[1].authentication.state).toBe("unauthorized");
		expect(postMock.mock.calls.some(([path]) => path === "/api/v1/agents/readiness/ensure")).toBe(false);
	});

	it.each(["not installed", "installation stale", "no capability", "stale inventory", "disabled account", "unverified account", "wrong provider", "no accounts"])("does not unlock a managed harness with %s", async failure => {
		const { agents, inventory, capability } = managedAccountDiscovery();
		switch (failure) {
			case "not installed": agents.agents[1].installation.state = "not_installed"; break;
			case "installation stale": agents.agents[1].installation.freshness = "stale"; break;
			case "no capability": capability.initialSelection = false; break;
			case "stale inventory": inventory.stale = true; break;
			case "disabled account": inventory.accounts[0].disabled = true; break;
			case "unverified account": inventory.accounts[0].verification = "unverified"; break;
			case "wrong provider": inventory.accounts[0].provider = "other"; break;
			case "no accounts": inventory.accounts = []; break;
		}
		renderDialog();
		const user = userEvent.setup();
		await waitFor(() => expect(screen.getByRole("button", { name: "Agent" })).toHaveTextContent("Cursor"));
		await user.click(screen.getByRole("button", { name: "Agent" }));
		await screen.findByRole("menuitem", { name: /Cursor/ });
		expect(screen.queryByRole("menuitem", { name: /Codex/ })).not.toBeInTheDocument();
		expect(delegateCalls()).toHaveLength(0);
	});
});

describe("account model isolation", () => {
	it("shows all account emails and quota without making an unavailable account selectable", async () => {
		const { inventory } = managedAccountDiscovery("codex");
		Object.assign(inventory.accounts[0], { email: "one@example.test", quotaSupported: true });
		inventory.accounts.push(Object.assign({}, inventory.accounts[0], { id: "account-b", email: "two@example.test" }));
		inventory.accounts.push(Object.assign({}, inventory.accounts[0], { id: "account-c", email: "three@example.test", unavailable: true, status: "error" }));
		const fallback = getMock.getMockImplementation();
		getMock.mockImplementation(async (path: string, options?: unknown) => {
			if (path.endsWith("/quota")) return { data: { observedAt: "2026-09-29T15:00:00Z", groups: [{ displayName: "Account", buckets: [{ window: "five_hour", remainingFraction: 0 }] }] } };
			return fallback?.(path, options);
		});
		renderDialog();
		const user = userEvent.setup();
		const picker = await openAccountMenu(user);
		for (const email of ["one", "two", "three"]) expect(await screen.findByRole("menuitem", { name: new RegExp(`${email}@example.test.*0% remaining`) })).toBeInTheDocument();
		const unavailable = screen.getByRole("menuitem", { name: /three@example.test/ });
		expect(unavailable).toHaveAttribute("aria-disabled", "true");
		await user.click(unavailable);
		await user.keyboard("{Escape}");
		expect(picker).toHaveTextContent(/^Account$/);
		expect(screen.getByRole("button", { name: "Start task" })).toBeDisabled();
		expect(delegateCalls()).toHaveLength(0);
	});

	it("ignores late models from the previous account and resets model and effort on account changes", async () => {
		const { inventory } = managedAccountDiscovery("codex");
		inventory.accounts.push({ ...inventory.accounts[0], id: "account-b", label: "Personal" });
		let finishA!: (value: unknown) => void;
		const fallback = getMock.getMockImplementation();
		getMock.mockImplementation(async (path: string, options?: { params?: { path?: { accountId?: string } } }) => {
			if (path.endsWith("/models") && path.startsWith("/api/v1/accounts-manager/")) {
				if (options?.params?.path?.accountId === "account-a") return new Promise(resolve => { finishA = resolve; });
				return { data: { models: [{ id: "b-model", displayName: "B model", efforts: ["low", "high"] }] } };
			}
			return fallback?.(path, options);
		});
		renderDialog();
		const user = userEvent.setup();
		await chooseAccount(user, "Work (account-a)");
		await waitFor(() => expect(finishA).toBeDefined());
		await chooseAccount(user, "Personal (account-b)");
		await user.click(await screen.findByRole("button", { name: "Model" }));
		await user.click(await screen.findByRole("menuitem", { name: "B model" }));
		await user.click(screen.getByRole("button", { name: "Effort" }));
		await user.click(screen.getByRole("menuitem", { name: "High" }));
		await act(async () => finishA({ data: { models: [{ id: "a-private-model", displayName: "A model" }] } }));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("B model");
		expect(screen.queryByText("A model")).not.toBeInTheDocument();
		await chooseAccount(user, "Native credentials");
		expect(screen.getByRole("button", { name: "Model" })).not.toHaveTextContent("B model");
		expect(screen.queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
	});

	it("keeps account choice after model and effort in the compact controls", async () => {
		const { inventory } = managedAccountDiscovery("codex");
		inventory.accounts.push({ ...inventory.accounts[0], id: "account-b", label: "Personal" });
		const fallback = getMock.getMockImplementation();
		getMock.mockImplementation(async (path: string, options?: unknown) => {
			if (path === "/api/v1/accounts-manager/accounts/{accountId}/models") return { data: { models: [{ id: "managed-model", displayName: "Managed model", efforts: ["low", "high"] }] } };
			return fallback?.(path, options);
		});
		renderDialog();
		const user = userEvent.setup();
		const account = await screen.findByRole("button", { name: "Initial account" });
		const controls = screen.getByRole("group", { name: "Runs with" });
		expect(controls).toContainElement(account);
		expect(account.compareDocumentPosition(screen.getByRole("button", { name: "Model" })) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
		await chooseAccount(user, "Work (account-a)");
		await user.click(await screen.findByRole("button", { name: "Model" }));
		await user.click(await screen.findByRole("menuitem", { name: "Managed model" }));
		expect(controls).toContainElement(screen.getByRole("button", { name: "Effort" }));
		expect(account.compareDocumentPosition(screen.getByRole("button", { name: "Effort" })) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Effort" }));
		await user.click(screen.getByRole("menuitem", { name: "High" }));
		await user.type(screen.getByLabelText("Task"), "Use this exact account, model and effort");
		await user.click(screen.getByRole("button", { name: "Start task" }));
		await waitFor(() => expect(delegateCalls()).toHaveLength(1));
		expect(requestBody()).toMatchObject({ account: { mode: "managed", accountId: "account-a" }, model: "managed-model", effort: "high" });
		expect(getMock.mock.calls.some(([path]) => path === "/api/v1/agents/{agent}/models")).toBe(false);
	});

	it("does not discover or inherit a cached device catalog before a managed choice", async () => {
		managedAccountDiscovery("codex");
		const client = new QueryClient();
		client.setQueryData(["agent-models", "codex", "proj-1"], {
			...directModelCatalog, agentId: "codex", refreshRecommended: true,
			models: [{ id: "device-only-default", label: "Device model", isDefault: true }],
		});
		const { onCreated } = renderDialog(client);
		const user = userEvent.setup();
		await openAccountMenu(user);
		const managed = await screen.findByRole("menuitem", { name: "Work (account-a)" });
		await waitFor(() => expect(managed).not.toHaveAttribute("aria-disabled", "true"));
		await user.keyboard("{Escape}");
		expect(getMock.mock.calls.some(([path, options]) => path === "/api/v1/agents/{agent}/models" && options?.params?.path?.agent === "codex")).toBe(false);
		expect(postMock.mock.calls.some(([path]) => path === "/api/v1/agents/{agent}/models/refresh")).toBe(false);
		expect(screen.getByRole("button", { name: "Model" })).not.toHaveTextContent("Device model");
		await chooseAccount(user, "Work (account-a)");
		await user.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.queryByRole("button", { name: /Refresh/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("searchbox", { name: "Search model" })).not.toBeInTheDocument();
		await user.click(await screen.findByRole("menuitem", { name: "Managed model" }));
		await user.type(screen.getByLabelText("Task"), "Use only account A");
		await user.click(screen.getByRole("button", { name: "Start task" }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("worker-1"));
		expect(requestBody()).toMatchObject({ model: "explicit-model", account: { mode: "managed", accountId: "account-a" } });
		expect(getMock.mock.calls.some(([path, options]) => path === "/api/v1/agents/{agent}/models" && options?.params?.path?.agent === "codex")).toBe(false);
	});

	it("retains model discovery and device preflight for an explicit native choice", async () => {
		const { agents } = managedAccountDiscovery("codex");
		agents.agents[1] = agentReadiness("codex", "Codex");
		renderDialog();
		const user = userEvent.setup();
		await openAccountMenu(user);
		await screen.findByRole("menuitem", { name: "Work (account-a)" });
		expect(getMock.mock.calls.some(([path]) => path === "/api/v1/agents/{agent}/models")).toBe(false);
		await user.click(screen.getByRole("menuitem", { name: "Native credentials" }));
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", expect.objectContaining({ params: expect.objectContaining({ path: { agent: "codex" } }) })));
		await user.type(screen.getByLabelText("Task"), "Use device credentials explicitly");
		await user.click(screen.getByRole("button", { name: "Start task" }));
		await waitFor(() => expect(delegateCalls()).toHaveLength(1));
		expect(requestBody()).toMatchObject({ account: { mode: "native" } });
		expect(postMock).toHaveBeenCalledWith("/api/v1/agents/readiness/ensure", { body: { agentIds: ["codex"], purpose: "launch" } });
	});
});

describe("NewTaskDialog", () => {
	it("renders one continuous composer surface with a visible settings-style title", async () => {
		renderDialog();
		await waitForAgentCatalog();
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/tasks/prepare", {
				params: { path: { id: "proj-1" } },
			}),
		);

		const dialog = screen.getByRole("dialog", { name: "Create a new task" });
		expect(dialog.querySelector(".composer-prompt-surface")).not.toBeNull();
		expect(screen.getByText("Create a new task")).toHaveClass("settings-dialog-title");
		expect(screen.queryByText("Runs with")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Close new task dialog" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Agent" })).toHaveTextContent("Claude Code");
		expect(screen.queryByTestId("execution-context")).not.toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Select model");
		expect(screen.getByRole("button", { name: "Add file" })).toBeInTheDocument();
		expect(screen.getByLabelText("Task").getAttribute("placeholder")).toBeTruthy();
		expect(screen.queryByLabelText("Title")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Branch")).not.toBeInTheDocument();
	});

	it("dismisses the chrome-free card with Escape", async () => {
		const { onOpenChange } = renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.keyboard("{Escape}");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("cancels an unused prepared worktree when the composer closes", async () => {
		const { unmount } = renderDialog();
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/tasks/prepare", {
				params: { path: { id: "proj-1" } },
			}),
		);
		unmount();
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/task-preparations/{token}", {
			params: { path: { token: "prep-token" } },
		});
	});

	it("cancels a preparation that resolves while an unprepared task is submitting", async () => {
		let resolvePreparation!: (value: unknown) => void;
		let resolveDelegate!: (value: unknown) => void;
		postMock.mockImplementation((path: string) => {
			if (path === "/api/v1/projects/{id}/tasks/prepare") {
				return new Promise((resolve) => {
					resolvePreparation = resolve;
				});
			}
			if (path === "/api/v1/orchestrators/delegate") {
				return new Promise((resolve) => {
					resolveDelegate = resolve;
				});
			}
			return Promise.resolve({ data: agentInventory, error: undefined });
		});
		const { onCreated } = renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();
		await user.type(screen.getByLabelText("Task"), "Fix the race");
		await user.click(screen.getByRole("button", { name: "Start task" }));
		await waitFor(() => expect(delegateCalls()).toHaveLength(1));
		expect(requestBody()).not.toHaveProperty("taskPreparation");

		await act(async () => {
			resolvePreparation({
				data: { ok: true, taskPreparation: "late-prep" },
				error: undefined,
			});
		});
		resolveDelegate({
			data: { ok: true, workerId: "worker-1" },
			error: undefined,
		});
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("worker-1"));
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/task-preparations/{token}", {
			params: { path: { token: "late-prep" } },
		});
	});

	it("starts the original task naming the preselected project-default agent and optional model", async () => {
		const { onCreated, onOpenChange } = renderDialog();
		const user = userEvent.setup();
		const brief = "  Restore the fallback renderer after WebGL init fails.  ";

		await waitForAgentCatalog();
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/projects/{id}/tasks/prepare", {
				params: { path: { id: "proj-1" } },
			}),
		);

		await user.type(screen.getByLabelText("Task"), brief);
		await user.click(await screen.findByRole("button", { name: "Model" }));
		await user.type(screen.getByRole("searchbox", { name: "Search model" }), "placeholder-model");
		await user.click(screen.getByRole("menuitem", { name: "Use “placeholder-model” as a custom model" }));
		await user.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(requestBody).not.toThrow());
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", {
			body: {
				projectId: "proj-1",
				brief,
				// The dialog preselects the project's worker agent, so the delegate
				// call names it instead of relying on a server-side fallback.
				agent: "claude-code",
				model: "placeholder-model",
				taskPreparation: "prep-token",
			},
		});
		expect(requestBody()).not.toHaveProperty("issueId");
		expect(requestBody()).not.toHaveProperty("branch");
		expect(requestBody()).not.toHaveProperty("harness");
		expect(onCreated).toHaveBeenCalledWith("worker-1");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	}, 20_000);

	it("offers an explicit Terminal UI retry when Chat preflight fails", async () => {
		let delegateAttempts = 0;
		postMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: agentInventory, error: undefined };
			if (path === "/api/v1/projects/{id}/tasks/prepare") {
				return { data: { ok: true, taskPreparation: "prep-token" }, error: undefined };
			}
			delegateAttempts += 1;
			if (delegateAttempts === 1) {
				return {
					data: undefined,
					error: { code: "CHAT_AUTH_REQUIRED", message: "Claude Code needs login" },
				};
			}
			return { data: { ok: true, workerId: "worker-tui" }, error: undefined };
		});
		const { onCreated } = renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Task"), "Fix it");
		await user.click(screen.getByRole("button", { name: "Start task" }));

		const fallback = await screen.findByRole("button", { name: "Create as Terminal UI" });
		expect(requestBody()).not.toHaveProperty("mode");
		await user.click(fallback);

		await waitFor(() => expect(delegateCalls()).toHaveLength(2));
		const retryBody = (delegateCalls()[1][1] as { body: Record<string, unknown> }).body;
		expect(retryBody.mode).toBe("tui");
		expect(onCreated).toHaveBeenCalledWith("worker-tui");
	});

	it("sends the chosen agent when the user overrides the default", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Task"), "B");

		await user.click(screen.getByRole("button", { name: "Agent" }));
		await user.click(await screen.findByRole("menuitem", { name: "Cursor" }));

		await user.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(requestBody).not.toThrow());
		expect(requestBody().agent).toBe("cursor");
	});

	it("hides agents with unknown auth and offers agent management without changing the selection", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.click(screen.getByRole("button", { name: "Agent" }));
		const options = await screen.findAllByRole("menuitem");
		expect(options.map((option) => option.textContent)).toEqual(["Claude Code", "Cursor", "Manage agents…"]);
		expect(screen.queryByRole("menuitem", { name: /Kiro/ })).not.toBeInTheDocument();
		await user.keyboard("{Escape}");

		await user.type(screen.getByLabelText("Task"), "B");
		await user.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(requestBody).not.toThrow());
		expect(requestBody().agent).toBe("claude-code");
	});

	it("starts an untitled task without an initial prompt", async () => {
		const { onCreated, onOpenChange } = renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(requestBody).not.toThrow());
		expect(requestBody()).toMatchObject({
			projectId: "proj-1",
			brief: "",
			agent: "claude-code",
		});
		expect(onCreated).toHaveBeenCalledWith("worker-1");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("shows an empty Model field for scratch projects and omits it from delegation", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/account-selection") {
				return { data: { initialSelection: false }, error: undefined };
			}
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [agentReadiness("claude-code", "Claude Code")],
					},
					error: undefined,
				};
			}
			if (path === "/api/v1/agents/{agent}/models") {
				return { data: directModelCatalog, error: undefined };
			}
			return {
				data: {
					status: "ok",
					project: { id: "proj-1", kind: "scratch", config: { worker: { agent: "claude-code" } } },
				},
				error: undefined,
			};
		});

		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		expect(screen.queryByLabelText("Branch")).not.toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Select model");

		await user.type(screen.getByLabelText("Task"), "Build a quick prototype in scratch.");
		await user.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(requestBody).not.toThrow());
		expect(requestBody()).not.toHaveProperty("branch");
		expect(requestBody().model).toBeUndefined();
	});

	it("submits on Enter and inserts a newline on Shift+Enter in the task", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		const task = screen.getByLabelText("Task");
		await user.type(task, "First line");
		// Shift+Enter must NOT submit — it adds a newline.
		await user.keyboard("{Shift>}{Enter}{/Shift}");
		await user.type(task, "Second line");
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.anything());

		// Plain Enter submits the task.
		await user.keyboard("{Enter}");
		await waitFor(() => expect(requestBody).not.toThrow());
		expect(requestBody().brief).toContain("\n");
	});

	it("does not submit on Alt+Enter or Shift+Enter but does on plain Enter in the task", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		const task = screen.getByLabelText("Task");
		await user.type(task, "Line");

		// Alt+Enter must NOT submit — Alt is excluded so it can't submit by accident.
		await user.keyboard("{Alt>}{Enter}{/Alt}");
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.anything());

		// Shift+Enter must NOT submit — it inserts a newline.
		await user.keyboard("{Shift>}{Enter}{/Shift}");
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.anything());

		// Plain Enter submits the task.
		await user.keyboard("{Enter}");
		await waitFor(() => expect(delegateCalls()).toHaveLength(1));
	});

	it.each([
		{
			code: "UNKNOWN_HARNESS",
			message: "Unknown requested agent",
		},
		{
			code: "INTERNAL",
			message: "task start failed",
		},
	])("displays daemon start errors for $code", async ({ code, message }) => {
		postMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/projects/{id}/tasks/prepare") {
				return { data: { ok: true, taskPreparation: "prep-token" }, error: undefined };
			}
			if (path === "/api/v1/agents/readiness/ensure") {
				return { data: agentInventory, error: undefined };
			}
			return {
				data: undefined,
				error: { code, message },
			};
		});
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Task"), "Restore fallback renderer.");
		await user.click(screen.getByRole("button", { name: "Start task" }));

		expect(await screen.findByText(`${message} (${code})`)).toBeInTheDocument();
	});
});
