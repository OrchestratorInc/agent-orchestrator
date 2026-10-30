import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
	delete: vi.fn(),
	get: vi.fn(),
	post: vi.fn(),
	remoteGet: vi.fn(),
	remotePost: vi.fn(),
	remoteDelete: vi.fn(),
	connectedHostIds: ["box-a"],
	capture: vi.fn(),
	ensureReadiness: vi.fn(),
	ensureTargetedReadiness: vi.fn(),
	agentValues: [] as string[],
	agentCatalog: undefined as { agents: ReturnType<typeof import("../test/agent-readiness-fixtures").agentReadiness>[] } | undefined,
	cloudProjects: [] as Array<{ id: string; displayName: string; repositoryUrl: string; defaultBranch: string; config: Record<string, unknown> }>,
	cloudCreateSession: vi.fn(),
	cloudListUserAgentModels: vi.fn(),
	cloudListProviderConnections: vi.fn(),
	cloudChatHarnesses: ["claude-code", "codex", "cursor"],
	cloudOrgId: "org-1",
	cloudBaseUrl: "https://cloud-a.example.test",
	cloudUserId: "user-1",
}));

vi.mock("../hooks/useAgentReadinessQuery", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentReadinessQuery")>();
	return {
		...actual,
		ensureAgentReadiness: h.ensureTargetedReadiness,
		useAgentReadinessQuery: () => ({ data: h.agentCatalog, isFetching: false, isSuccess: h.agentCatalog !== undefined }),
		useEnsureAgentReadiness: h.ensureReadiness,
	};
});

vi.mock("./CreateProjectAgentSheet", () => ({
	RequiredAgentField: ({
		value,
		onChange,
		triggerClassName,
		disabled,
		manageView,
	}: {
		value: string;
		onChange: (value: string) => void;
		triggerClassName?: string;
		disabled?: boolean;
		manageView?: "local" | "cloud";
	}) => {
		h.agentValues.push(value);
		return (
			<button
				type="button"
				aria-label="Agent"
				className={triggerClassName}
				data-testid="agent-field"
				data-value={value}
				data-manage-view={manageView}
				disabled={disabled}
				onClick={() => onChange(value === "codex" ? "claude-code" : "codex")}
			/>
		);
	},
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		DELETE: h.delete,
		GET: h.get,
		POST: h.post,
	},
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (error: { message?: string }, fallback = "err") => error?.message ?? fallback,
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: () => ({ GET: h.remoteGet, POST: h.remotePost, DELETE: h.remoteDelete }),
	connectedHosts: () => h.connectedHostIds,
	subscribeConnectedHosts: () => () => undefined,
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: h.capture }));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => ({ data: h.cloudProjects }),
	cloudProjectsQueryKey: ["cloud-projects"] as const,
	useCloudSessionsQuery: () => ({ data: [] }),
	cloudSessionsQueryKey: ["cloud-sessions"] as const,
	seedCreatedFreestyleSession: vi.fn(),
}));

vi.mock("../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: { id: h.cloudOrgId } }),
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		baseUrl: h.cloudBaseUrl,
		userId: h.cloudUserId,
		client: { createSession: h.cloudCreateSession, listUserAgentModels: h.cloudListUserAgentModels, listUserProviderConnections: h.cloudListProviderConnections },
	}),
}));

vi.mock("../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ chatHarnesses: h.cloudChatHarnesses }),
}));

import { TaskComposer, useTaskLaunch } from "./TaskComposer";
import { CloudCpError } from "../lib/cloud-cp";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { providerConnectionsQueryKey } from "../hooks/useProviderConnections";
import { cacheAgentModels, clearCachedAgentModels } from "../lib/agent-model-cache";
import { useUiStore } from "../stores/ui-store";

function Wrap({ children, queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }) }: {
	children: ReactNode;
	queryClient?: QueryClient;
}) {
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const task = () => screen.getByRole("textbox", { name: "Task" });
const startTask = () => screen.getByRole("button", { name: "Start task" });
const localModelReads = () => h.get.mock.calls.filter(([path]) => String(path).includes("/models"));

/** A cloud project whose worker runs `agent`, connected for Cloud unless `connections` says otherwise. */
function cloudFixture(
	agent: string,
	agentConfig: Record<string, string> = {},
	connections: Array<Record<string, unknown>> = [
		{ id: `${agent}-connection`, provider: agent, label: "default", validationState: "valid", updatedAt: "2026-10-24T00:00:00Z", config: {} },
	],
) {
	h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent, agentConfig } } });
	h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(providerConnectionsQueryKey, connections);
	return queryClient;
}

function releaseCatalog(models: Array<Record<string, unknown>>, cliVersion = "2.1.287") {
	return { models, source: "release", cliVersion };
}

/** Picks a listed Cloud model, which nothing selected for the user beforehand. */
async function chooseCloudModel(label: string) {
	const picker = await screen.findByRole("button", { name: "Model" });
	await waitFor(() => expect(picker).toHaveTextContent("Project or agent default"));
	await userEvent.click(picker);
	await userEvent.click(await screen.findByRole("menuitem", { name: label }));
	expect(picker).toHaveTextContent(label);
}

async function enterCloudModel(id: string) {
	await userEvent.click(screen.getByRole("button", { name: "Model" }));
	await userEvent.type(screen.getByRole("searchbox"), id);
	await userEvent.click(screen.getByRole("menuitem", { name: new RegExp(id) }));
}

async function waitForTaskReady() {
	await waitFor(() => expect(startTask()).toBeEnabled());
}

beforeEach(() => {
	useUiStore.setState({ developerMode: true, remoteHosts: true });
	// An older control plane, unless a test says otherwise.
	h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
	h.cloudListProviderConnections.mockRejectedValue(new CloudCpError("Signed out", { status: 401 }));
	h.get.mockImplementation(async (path: string) => {
		if (path.includes("/models")) {
			return {
				data: {
					agent: "codex",
					selectionMode: "text",
					models: [],
					allowCustom: true,
					refreshRecommended: false,
				},
			};
		}
		return { data: { status: "ok", project: { config: {} } } };
	});
});

afterEach(() => {
	cleanup();
	useUiStore.setState({ developerMode: false, remoteHosts: false });
	h.delete.mockReset();
	h.get.mockReset();
	h.post.mockReset();
	h.remoteGet.mockReset();
	h.remotePost.mockReset();
	h.remotePost.mockResolvedValue({ data: {} });
	h.remoteDelete.mockReset();
	h.capture.mockReset();
	h.ensureReadiness.mockReset();
	h.ensureTargetedReadiness.mockReset();
	h.agentCatalog = undefined;
	h.cloudProjects.length = 0;
	h.cloudCreateSession.mockReset();
	h.cloudListUserAgentModels.mockReset();
	h.cloudListProviderConnections.mockReset();
	h.cloudChatHarnesses = ["claude-code", "codex", "cursor"];
	h.cloudOrgId = "org-1";
	h.cloudBaseUrl = "https://cloud-a.example.test";
	h.cloudUserId = "user-1";
	vi.unstubAllGlobals();
	h.agentValues.length = 0;
	window.localStorage.removeItem("ao.taskComposer.preferences.v1");
	clearCachedAgentModels("codex");
});

describe("TaskComposer", () => {
	it("does not launch a remote project task without a ready agent", async () => {
		h.agentCatalog = { agents: [] };
		h.remoteGet.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: [] } }
			: { data: { status: "ok", project: { id: "project-a", config: {} } } });
		h.remotePost.mockImplementation(async (path: string) => path === "/api/v1/agents/readiness/ensure"
			? { data: { agents: [] } }
			: { data: {} });
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText("No agent is ready on this host. Configure one there first.");
		expect(startTask()).toBeDisabled();
		expect(h.remotePost.mock.calls.some(([path]) => path === "/api/v1/orchestrators/delegate")).toBe(false);
	});

	it("does not start a remote task with the project's uninstalled default agent", async () => {
		h.agentCatalog = { agents: [
			agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
			agentReadiness("opencode", "OpenCode", { authentication: "unknown" }),
		] };
		h.remoteGet.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } }
			: { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "claude-code" } } } } });
		h.remotePost.mockImplementation(async (path: string) => path === "/api/v1/agents/readiness/ensure"
			? { data: { agents: [
				agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
				agentReadiness("opencode", "OpenCode", { authentication: "unknown" }),
			] } }
			: { data: {} });
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		await waitFor(() => expect(screen.getByTestId("agent-field")).toBeEnabled());
		expect(startTask()).toBeDisabled();
		expect(screen.queryByText("No agent is ready on this host. Configure one there first.")).not.toBeInTheDocument();
	});

	it("creates a remote project task through its host, not the local daemon", async () => {
		h.agentCatalog = { agents: [agentReadiness("opencode", "OpenCode", { authentication: "unknown" })] };
		const onCreated = vi.fn();
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agent: "opencode", selectionMode: "text", models: [], allowCustom: true } };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } };
			return { data: { status: "ok", project: { id: "project-a", config: { worker: { agent: "opencode" } } } } };
		});
		h.remotePost.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: { agents: [agentReadiness("opencode", "OpenCode", { authentication: "unknown" })] } };
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "" } };
			if (path === "/api/v1/orchestrators/delegate") return { data: { workerId: "remote-task" } };
			return { data: {} };
		});
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={onCreated} /></Wrap>);
		await waitFor(() => expect(startTask()).toBeEnabled());
		expect(screen.queryByText("No agent is ready on this host. Configure one there first.")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Fix the issue" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("remote-task"));
		expect(h.remotePost).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.objectContaining({
			body: expect.objectContaining({ projectId: "project-a", brief: "Fix the issue" }),
		}));
		expect(h.post.mock.calls.some(([path]) => path === "/api/v1/orchestrators/delegate")).toBe(false);
	});

	it("reuses a task request id after an uncertain response and changes it with the draft", async () => {
		h.agentCatalog = { agents: [agentReadiness("opencode", "OpenCode")] };
		h.remoteGet.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agent: "opencode", selectionMode: "text", models: [], allowCustom: true } };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "chat", chatHarnesses: ["opencode"] } };
			return {
				data: {
					status: "ok",
					project: { id: "project-a", config: { worker: { agent: "opencode" } } },
				},
			};
		});
		h.remotePost.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness/ensure") return { data: { agents: [agentReadiness("opencode", "OpenCode")] } };
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "" } };
			if (path === "/api/v1/orchestrators/delegate") return { error: { code: "SPAWN_TIMEOUT", message: "response lost" } };
			return { data: {} };
		});
		render(<Wrap><TaskComposer hostId="box-a" projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Fix it" } });
		fireEvent.click(startTask());
		await screen.findByText("response lost");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")).toHaveLength(2));
		const requests = h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate");
		const firstID = requests[0][1].body.clientRequestId;
		expect(firstID).toBeTruthy();
		expect(requests[1][1].body.clientRequestId).toBe(firstID);
		await screen.findByText("response lost");
		fireEvent.change(task(), { target: { value: "Fix something else" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")).toHaveLength(3));
		const changed = h.remotePost.mock.calls.filter(([path]) => path === "/api/v1/orchestrators/delegate")[2][1].body.clientRequestId;
		expect(changed).not.toBe(firstID);
	});

	it("preselects the highest-ranked ready agent for a standalone task", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("cursor", "Cursor", { authentication: "unauthorized" }),
			],
		};

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		await waitFor(() => expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument());
	});

	it("preserves an explicitly selected standalone agent when readiness rankings refresh", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};

		const { rerender } = render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.click(screen.getByLabelText("Agent"));
		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code");

		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code"),
				agentReadiness("codex", "Codex", { usageCount: 10, lastUsedAt: "2026-09-21T12:00:00Z" }),
			],
		};
		rerender(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() =>
			expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code"),
		);
	});

	it("restores a standalone agent's draft model after switching away and back", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				return request?.params?.path?.agent === "claude-code"
					? {
							data: {
								agent: "claude-code",
								selectionMode: "text",
								models: [{ id: "fable-5.1", label: "Fable 5.1", isDefault: true }],
								allowCustom: true,
							},
						}
					: {
							data: {
								agent: "codex",
								selectionMode: "text",
								models: [
									{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
									{ id: "gpt-5.5", label: "GPT-5.5" },
								],
								allowCustom: true,
							},
						};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT-6-Astra"));
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		expect(model).toHaveTextContent("GPT-5.5");

		fireEvent.click(screen.getByLabelText("Agent"));
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1"));
		fireEvent.click(screen.getByLabelText("Agent"));

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.5"));
	});

	it("restores the last successfully spawned standalone harness and model in a new composer", async () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" }),
				agentReadiness("claude-code", "Claude Code"),
			],
		};
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				return request?.params?.path?.agent === "claude-code"
					? {
							data: {
								agent: "claude-code",
								selectionMode: "text",
								models: [{ id: "fable-5.1", label: "Fable 5.1", isDefault: true }],
								allowCustom: true,
							},
						}
					: {
							data: {
								agent: "codex",
								selectionMode: "text",
								models: [
									{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
									{ id: "gpt-5.5", label: "GPT-5.5" },
								],
								allowCustom: true,
							},
						};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-remembered" } } });

		const first = render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		fireEvent.click(await screen.findByLabelText("Agent"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1");
		fireEvent.change(task(), { target: { value: "Remember this setup" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		first.unmount();

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "claude-code"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Fable 5.1");
	});

	it("persists successful model choices per project without leaking them to another project", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
							{ id: "gpt-5.5", label: "GPT-5.5" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "project-remembered" } });

		const first = render(<Wrap><TaskComposer projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		fireEvent.change(task(), { target: { value: "Remember this project setup" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		first.unmount();

		const remembered = render(<Wrap><TaskComposer projectId="project-a" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.5"));
		remembered.unmount();

		render(<Wrap><TaskComposer projectId="project-b" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-6-Astra"));
	});

	it("does not persist a standalone preference when task creation fails", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
							{ id: "gpt-5.5", label: "GPT-5.5" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ error: { code: "SPAWN_FAILED", message: "Could not start task" } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		fireEvent.change(task(), { target: { value: "This spawn will fail" } });
		fireEvent.click(screen.getByText("Start task"));

		expect(await screen.findByText("Could not start task")).toBeInTheDocument();
		expect(window.localStorage.getItem("ao.taskComposer.preferences.v1")).toBeNull();
	});

	it("falls back when remembered standalone harness and model choices are no longer valid", async () => {
		window.localStorage.setItem("ao.taskComposer.preferences.v1", JSON.stringify({
			__standalone__: {
				lastAgent: "removed-agent",
				agents: {
					codex: { model: "retired-model", mode: "" },
					"removed-agent": { model: "removed-model", mode: "" },
				},
			},
		}));
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true }],
						allowCustom: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-6-Astra");
	});

	it("keeps a standalone task unselected when no agent is ready", () => {
		h.agentCatalog = {
			agents: [
				agentReadiness("claude-code", "Claude Code", { installation: "not_installed" }),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
			],
		};

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "");
		expect(screen.getByLabelText("Model")).toHaveTextContent("Select agent");
		expect(screen.getByLabelText("Model")).toHaveAttribute("aria-disabled", "true");
	});

	it("does not show another view's failed lookup of the standalone sentinel as a Scratchpad error", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// A Scratchpad chat elsewhere asked the daemon for the sentinel as a project.
		await queryClient.prefetchQuery({
			queryKey: ["project", "__standalone__"],
			queryFn: async () => { throw new Error("Project id failed storage-path validation"); },
		});
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };

		render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		expect(screen.queryByText("Project id failed storage-path validation")).not.toBeInTheDocument();
		expect(screen.getByText("Start task")).toBeEnabled();
	});

	it("starts a standalone worker without loading or sending a project", async () => {
		const onCreated = vi.fn();
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={onCreated} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.change(task(), { target: { value: "Research release options" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("standalone-1"));
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/sessions",
			expect.objectContaining({
				body: expect.objectContaining({
					kind: "worker",
					harness: "codex",
					prompt: "Research release options",
				}),
			}),
		);
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("projectId");
		expect(h.get.mock.calls.some(([path]) => path === "/api/v1/projects/{id}")).toBe(false);
	});

	it("sends the visible default model when starting a standalone worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-6-astra", label: "GPT-6 Astra", isDefault: true },
							{ id: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-6 Astra"));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ model: "gpt-6-astra" }) }),
			),
		);
	});

	it("sends the visible default mode when starting a standalone mode-list worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("amp", "Amp")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "amp",
						selectionMode: "mode",
						models: [
							{ id: "high", label: "High", isDefault: true },
							{ id: "low", label: "Low" },
						],
						allowCustom: false,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("High"));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ model: "high" }) }),
			),
		);
	});

	it("sends the visible default effort when starting a standalone worker", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-5.6-sol",
							label: "GPT-5.6 Sol",
							isDefault: true,
							efforts: ["low", "high"],
							defaultEffort: "high",
						}],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);

		expect(await screen.findByRole("button", { name: "Effort" })).toHaveTextContent("High");
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({
					body: expect.objectContaining({ model: "gpt-5.6-sol", effort: "high" }),
				}),
			),
		);
	});

	it("sends the selected effort when starting a standalone worker", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["high"] }],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		fireEvent.click(screen.getByLabelText("Agent"));
		const effort = await screen.findByRole("button", { name: "Effort" });
		await userEvent.click(effort);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ effort: "high" }) }),
			),
		);
	});

	it("selects and sends a middle effort when the model reports levels but no default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["low", "medium", "high", "xhigh"] }],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { session: { id: "standalone-1" } } });

		render(
			<Wrap>
				<TaskComposer projectId="__standalone__" onCreated={vi.fn()} />
			</Wrap>,
		);

		fireEvent.click(screen.getByLabelText("Agent"));
		const effort = await screen.findByRole("button", { name: "Effort" });
		expect(effort).toHaveTextContent(/^Medium$/);
		await userEvent.click(effort);
		expect(screen.getByRole("menuitemradio", { name: "Medium" })).toHaveAttribute("aria-checked", "true");
		await userEvent.keyboard("{Escape}");
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/sessions",
				expect.objectContaining({ body: expect.objectContaining({ effort: "medium" }) }),
			),
		);
	});

	it("does not run provider readiness before Start", async () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByLabelText("Task")).toBeInTheDocument());
		expect(h.ensureReadiness).not.toHaveBeenCalled();
	});

	it("submits a gateway-backed Claude project when refreshed global readiness is unauthorized", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "claude-code",
						config: { env: { ANTHROPIC_BASE_URL: "https://gateway.example" } },
					},
				},
			};
		});
		const unauthorized = agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" });
		h.ensureTargetedReadiness.mockResolvedValueOnce({ agents: [unauthorized] });
		h.post.mockResolvedValueOnce({ data: { workerId: "worker-1" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("worker-1"));
		expect(h.ensureTargetedReadiness).toHaveBeenCalledWith(["claude-code"], "launch", undefined);
	});

	it("keeps submission enabled for a gateway-backed Claude project when cached global readiness is unauthorized", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "claude-code",
						config: { env: { ANTHROPIC_BASE_URL: "https://gateway.example" } },
					},
				},
			};
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		h.agentCatalog = {
			agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })],
		};
		h.ensureTargetedReadiness.mockResolvedValueOnce({
			agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })],
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "worker-1" } });

		render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		const submit = screen.getByRole("button", { name: "Start task" });
		expect(submit).toBeEnabled();
		fireEvent.click(submit);

		await waitFor(() => expect(h.ensureTargetedReadiness).toHaveBeenCalledWith(["claude-code"], "launch", undefined));
		await waitFor(() => expect(h.post).toHaveBeenCalled());
	});

	it("waits for project context before allowing a local task to start", async () => {
		let resolveProject!: (value: unknown) => void;
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			if (path === "/api/v1/projects/{id}") {
				return new Promise((resolve) => {
					resolveProject = resolve;
				});
			}
			return { data: undefined };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(startTask()).toBeDisabled();
		expect(screen.queryByTestId("execution-context")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "should wait" } });
		fireEvent.keyDown(task(), { key: "Enter", shiftKey: false, altKey: false });
		expect(h.post).not.toHaveBeenCalled();

		await act(async () =>
			resolveProject({ data: { status: "ok", project: { name: "my-app", repo: "acme/my-app", defaultBranch: "main", path: "/repo", config: {} } } }),
		);
		await waitForTaskReady();
	});

	it.each([
		["binary launch failure", "AGENT_BINARY_NOT_FOUND"],
		["project credential rejection", "AGENT_AUTH_REQUIRED"],
	] as const)("waits for and caches targeted readiness after a %s", async (_name, errorCode) => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post.mockResolvedValueOnce({
			error: { code: errorCode, message: "Codex is not ready" },
		});
		const stale = agentReadiness("codex", "Codex", { freshness: "stale" });
		const completed = agentReadiness("codex", "Codex", { installation: "not_installed" });
		let finishReadiness!: (value: { agents: ReturnType<typeof agentReadiness>[] }) => void;
		h.ensureTargetedReadiness
			.mockResolvedValueOnce({ agents: [stale] })
			.mockReturnValueOnce(
			new Promise((resolve) => {
				finishReadiness = resolve;
			}),
			);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(agentReadinessQueryKey, { agents: [stale] });

		render(
			<Wrap queryClient={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "codex"));
		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.ensureTargetedReadiness).toHaveBeenCalledTimes(2));
		expect(h.ensureTargetedReadiness).toHaveBeenLastCalledWith(["codex"], "launch", undefined);
		expect(screen.queryByText("Codex is not ready")).not.toBeInTheDocument();

		await act(async () => finishReadiness({ agents: [completed] }));
		expect(await screen.findByText("Codex is not ready")).toBeInTheDocument();
		expect(queryClient.getQueryData(agentReadinessQueryKey)).toEqual({ agents: [completed] });
	});

	it("starts a promptless worker when the task is empty", async () => {
		const onCreated = vi.fn();
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-empty" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);

		expect(task().getAttribute("placeholder")).toBeTruthy();
		expect(task()).toHaveClass("min-h-[calc(3lh+1.125rem)]");
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ projectId: "proj-1", brief: "" }) }),
			),
		);
		expect(onCreated).toHaveBeenCalledWith("sess-empty");
	});

	it("keeps prompt guidance in the field instead of adding a separate footer row", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(task().getAttribute("placeholder")).toBeTruthy();
		expect(screen.queryByText("Start now — details can come later.")).not.toBeInTheDocument();
		expect(screen.queryByText("Shift+Enter for a new line")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Investigate the failure" } });
		expect(task()).toHaveValue("Investigate the failure");
	});

	it("does not rerender the agent control for every prompt keystroke", async () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toBeInTheDocument());
		h.agentValues.length = 0;

		fireEvent.change(task(), { target: { value: "a" } });
		fireEvent.change(task(), { target: { value: "ab" } });
		fireEvent.change(task(), { target: { value: "abc" } });

		expect(h.agentValues).toHaveLength(1);
	});

	it("uses 2:2:1 toolbar tracks when the selected model advertises effort", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: {
					agent: "codex",
					selectionMode: "catalog",
					models: [{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"] }],
					allowCustom: true,
				} };
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await screen.findByRole("button", { name: "Effort" });
		const runControls = screen.getByRole("group", { name: "Runs with" });
		expect(runControls).toHaveClass("composer-run-controls", "composer-run-controls-with-effort");
		expect(runControls.closest(".composer-toolbar")).not.toBeNull();
		expect(runControls.querySelectorAll(".composer-toolbar-slot")).toHaveLength(3);
		expect(screen.getByTestId("agent-field").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(screen.getByLabelText("Model").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(screen.getByLabelText("Effort").closest(".composer-toolbar-effort-slot")).not.toBeNull();
	});

	it("keeps the file attach control in the bottom action row", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(screen.getByRole("button", { name: "Add file" }).closest(".composer-toolbar")).not.toBeNull();
	});

	it("emits busy state around an in-flight create and reports the new session", async () => {
		const onSubmittingChange = vi.fn();
		const onCreated = vi.fn();
		let resolveCreate!: (value: { data: { workerId: string } }) => void;
		h.post.mockReturnValueOnce(new Promise((resolve) => (resolveCreate = resolve)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "Do the thing" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(true));
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.not.objectContaining({ attachments: expect.anything() }),
			}),
		);
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.objectContaining({ projectId: "proj-1", brief: "Do the thing" }),
			}),
		);

		await act(async () => resolveCreate({ data: { workerId: "sess-1" } }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-1"));
		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(false));
	});

	it("locks agent and model selection while task creation is in flight, then unlocks them after failure", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		let rejectCreate!: (error: Error) => void;
		h.post.mockReturnValueOnce(new Promise((_resolve, reject) => (rejectCreate = reject)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const agent = await screen.findByTestId("agent-field");
		await waitFor(() => expect(agent).toHaveAttribute("data-value", "codex"));
		const model = await screen.findByRole("button", { name: "Model" });
		const prompt = task();
		expect(agent).toBeEnabled();
		expect(model).toBeEnabled();
		expect(prompt).toBeEnabled();

		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(agent).toBeDisabled();
		expect(model).toBeDisabled();
		expect(prompt).toBeDisabled();

		await act(async () => rejectCreate(new Error("creation failed")));
		await screen.findByText("creation failed");
		expect(agent).toBeEnabled();
		expect(model).toBeEnabled();
		expect(prompt).toBeEnabled();
	});

	it.each([
		{
			name: "mode",
			catalog: {
				agent: "codex",
				selectionMode: "mode",
				models: [{ id: "plan", label: "Plan", isDefault: true }],
				customModelEntry: "none",
				allowCustom: false,
			},
			controls: async () => [await screen.findByRole("button", { name: "Model" })],
		},
		{
			name: "catalog",
			catalog: {
				agent: "codex",
				selectionMode: "catalog",
				models: [{ id: "gpt-5", label: "GPT-5", isDefault: true }],
				customModelEntry: "none",
				allowCustom: false,
			},
			controls: async () => [await screen.findByRole("button", { name: "Model" })],
		},
	])("locks the $name selector while creating and restores it after failure", async ({ catalog, controls }) => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: catalog };
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		let rejectCreate!: (error: Error) => void;
		h.post.mockReturnValueOnce(new Promise((_resolve, reject) => (rejectCreate = reject)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(catalog.models[0].label));
		const modelControls = await controls();
		for (const control of modelControls) expect(control).toBeEnabled();

		fireEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		for (const control of modelControls) expect(control).toBeDisabled();

		await act(async () => rejectCreate(new Error("creation failed")));
		await screen.findByText("creation failed");
		for (const control of modelControls) expect(control).toBeEnabled();
	});

	it("attaches a selected file and sends it in the delegate body", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
		fireEvent.change(input, { target: { files: [file] } });

		expect(await screen.findByText("notes.txt")).toBeInTheDocument();

		fireEvent.change(task(), { target: { value: "Use the notes" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		const body = h.post.mock.calls[0][1].body as {
			attachments?: Array<{ mimeType: string; data: string }>;
		};
		expect(h.post.mock.calls[0][1].headers).toEqual({ "X-AO-Attachment-Upload": "1" });
		expect(body.attachments).toHaveLength(1);
		expect(body.attachments?.[0].mimeType).toBe("text/plain");
		expect(body.attachments?.[0].data.length).toBeGreaterThan(0);
	});

	it("manages cloud harness connections from a cloud project's agent picker", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: {} });
		const { unmount } = render(<Wrap><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		// Only connected harnesses, with "Manage harness connections…" (Harness settings' Cloud view).
		expect(await screen.findByLabelText("Agent")).toHaveAttribute("data-manage-view", "cloud");
		unmount();

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		expect(await screen.findByLabelText("Agent")).toHaveAttribute("data-manage-view", "local");
	});

	it("rejects cloud task attachments instead of silently dropping them", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: {} });
		const onCreated = vi.fn();
		const { container } = render(<Wrap><TaskComposer projectId="cloud-1" onCreated={onCreated} /></Wrap>);
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, { target: { files: [new File(["notes"], "notes.txt", { type: "text/plain" })] } });
		expect(await screen.findByText("notes.txt")).toBeInTheDocument();

		fireEvent.change(task(), { target: { value: "Read the notes" } });
		await waitForTaskReady();
		fireEvent.click(startTask());

		expect(await screen.findByRole("alert")).toHaveTextContent("File attachments are not supported for cloud tasks yet.");
		expect(onCreated).not.toHaveBeenCalled();
		expect(h.post).not.toHaveBeenCalled();
		expect(h.capture).toHaveBeenCalledWith("ao.renderer.task_create_failed", { project_id: "cloud-1" });
	});

	it("starts a cloud task in the default session mode, as a local task does", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent: "claude-code" } } });
		h.get.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: ["claude-code"] } }
			: { data: { agent: "claude-code", selectionMode: "catalog", models: [{ id: "claude-sonnet-test", label: "Sonnet", isDefault: true }], allowCustom: true } });
		h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
		render(<Wrap><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Do the work" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ interfaceMode: "chat" }), { idempotencyKey: expect.any(String) }));
	});

	it("starts a cloud task in the terminal when the control plane has no Chat for its agent, as local does", async () => {
		h.cloudChatHarnesses = [];
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent: "claude-code" } } });
		h.get.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: ["claude-code"] } }
			: { data: { agent: "claude-code", selectionMode: "catalog", models: [{ id: "claude-sonnet-test", label: "Sonnet", isDefault: true }], allowCustom: true } });
		h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
		render(<Wrap><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Do the work" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ interfaceMode: "tui" }), { idempotencyKey: expect.any(String) }));
	});

	it("offers to create a refused cloud Chat task in the terminal, as local does", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent: "claude-code" } } });
		h.get.mockImplementation(async (path: string) => path === "/api/v1/settings"
			? { data: { defaultSessionMode: "chat", chatHarnesses: ["claude-code"] } }
			: { data: { agent: "claude-code", selectionMode: "catalog", models: [{ id: "claude-sonnet-test", label: "Sonnet", isDefault: true }], allowCustom: true } });
		h.cloudCreateSession.mockRejectedValueOnce(new CloudCpError("claude-code can't run in Chat in AO Cloud. Start it in the terminal.", { status: 409, code: "SESSION_MODE_UNSUPPORTED" }));
		h.cloudCreateSession.mockResolvedValueOnce({ session: { id: "session-1" } });
		render(<Wrap><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Do the work" } });
		fireEvent.click(startTask());
		fireEvent.click(await screen.findByRole("button", { name: "Create as Terminal UI" }));
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenLastCalledWith("org-1", expect.objectContaining({ interfaceMode: "tui" }), { idempotencyKey: expect.any(String) }));
	});

	// An older control plane lists this device's models, whose first one the cloud VM may not run.
	it("leaves an unreported cloud default model to the cloud VM", async () => {
		const queryClient = cloudFixture("codex");
		h.get.mockImplementation(async () => ({ data: { agent: "codex", selectionMode: "catalog", models: [
			{ id: "gpt-local-only", label: "Local only" },
			{ id: "gpt-other", label: "Other" },
		], allowCustom: true } }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/This Cloud server can't list its models/);
		fireEvent.change(task(), { target: { value: "Do the work" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledOnce());
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("model");
	});

	it("defaults cloud effort to medium and sends explicit effort choices", async () => {
		const queryClient = cloudFixture("claude-code");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "claude-sonnet-test", displayName: "Sonnet", default: true, efforts: ["low", "medium", "high"] },
		]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Sonnet");
		const picker = await screen.findByRole("button", { name: "Effort" });
		await waitFor(() => expect(picker).toHaveTextContent("Medium"));
		await userEvent.click(picker);
		expect(screen.queryByRole("menuitem", { name: "Use agent effort" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.change(task(), { target: { value: "Do the work" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ reasoningEffort: "medium" }), { idempotencyKey: expect.any(String) }));
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		fireEvent.change(task(), { target: { value: "Another task" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenLastCalledWith("org-1", expect.objectContaining({ reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("starts a cloud task in the terminal when that is the default", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: {} });
		h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
		const onCreated = vi.fn();
		render(<Wrap><TaskComposer projectId="cloud-1" onCreated={onCreated} /></Wrap>);

		fireEvent.change(task(), { target: { value: "Fix the bug" } });
		await waitForTaskReady();
		fireEvent.click(startTask());

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("session-1"));
		// A task carries the interface it starts in, never the default an
		// orchestrator create carries.
		expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ interfaceMode: "tui" }), { idempotencyKey: expect.any(String) });
		expect(h.cloudCreateSession.mock.calls[0]?.[1]).not.toHaveProperty("defaultInterfaceMode");
	});

	it("leaves the sandbox provider to the project and org default on the control plane", async () => {
		h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { sandboxProvider: "coder" } });
		h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
		const onCreated = vi.fn();
		render(<Wrap><TaskComposer projectId="cloud-1" onCreated={onCreated} /></Wrap>);

		fireEvent.change(task(), { target: { value: "Fix the bug" } });
		await waitForTaskReady();
		fireEvent.click(startTask());

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("session-1"));
		expect(h.cloudCreateSession.mock.calls[0]?.[1]).not.toHaveProperty("provider");
	});

	it("waits for a selected file read before submitting", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });
		let finishRead!: () => void;
		class SlowFileReader {
			error: Error | null = null;
			result: string | ArrayBuffer | null = null;
			onerror: (() => void) | null = null;
			onload: (() => void) | null = null;

			readAsDataURL(file: File) {
				finishRead = () => {
					this.result = `data:${file.type};base64,AQID`;
					this.onload?.();
				};
			}
		}
		vi.stubGlobal("FileReader", SlowFileReader);

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([1, 2, 3])], "slow.txt", { type: "text/plain" })] },
		});
		fireEvent.change(task(), { target: { value: "Use the slow file" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		expect(h.post).not.toHaveBeenCalled();

		await act(async () => finishRead());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({
			attachments: [{ mimeType: "text/plain", data: "AQID" }],
		});
	});

	it("waits for both rapidly selected file batches before delegating", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });
		const pendingReads: Array<() => void> = [];
		class SlowFileReader {
			error: Error | null = null;
			result: string | ArrayBuffer | null = null;
			onerror: (() => void) | null = null;
			onload: (() => void) | null = null;

			readAsDataURL(file: File) {
				pendingReads.push(() => {
					this.result = `data:${file.type};base64,${file.name === "first.txt" ? "AQ==" : "Ag=="}`;
					this.onload?.();
				});
			}
		}
		vi.stubGlobal("FileReader", SlowFileReader);
		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([1])], "first.txt", { type: "text/plain" })] },
		});
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([2])], "second.txt", { type: "text/plain" })] },
		});
		fireEvent.change(task(), { target: { value: "Use both files" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));
		expect(h.post).not.toHaveBeenCalled();

		await act(async () => pendingReads.shift()?.());
		await waitFor(() => expect(pendingReads).toHaveLength(1));
		expect(h.post).not.toHaveBeenCalled();
		await act(async () => pendingReads.shift()?.());

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({
			attachments: [
				{ mimeType: "text/plain", data: "AQ==" },
				{ mimeType: "text/plain", data: "Ag==" },
			],
		});
	});

	it("removes a selected file before submitting", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
		fireEvent.change(input, { target: { files: [file] } });

		expect(await screen.findByText("notes.txt")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Remove notes.txt" }));
		await waitFor(() => expect(screen.queryByText("notes.txt")).not.toBeInTheDocument());

		fireEvent.change(task(), { target: { value: "No attachment now" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].headers).toBeUndefined();
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("attachments");
	});

	it("clears busy state when a create rejects", async () => {
		const onSubmittingChange = vi.fn();
		h.post.mockRejectedValueOnce(new Error("nope"));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "B" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(screen.getByText("nope")).toBeInTheDocument());
		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(false));
	});

	it("silently routes agents without Chat support to Terminal UI", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "chat", chatHarnesses: ["codex"] } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "grok",
						selectionMode: "catalog",
						models: [{ id: "grok-4.6", label: "grok-4.6", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "grok", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-grok" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("grok-4.6");
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({ body: expect.objectContaining({ agent: "grok", mode: "tui" }) }),
		));
	});

	it("preserves Codex effort when retrying in Terminal UI", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "chat", chatHarnesses: ["codex"] } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-test",
							label: "GPT Test",
							isDefault: true,
							efforts: ["low", "high"],
						}],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		h.post
			.mockResolvedValueOnce({ error: { code: "CHAT_DRIVER_UNAVAILABLE" } })
			.mockResolvedValueOnce({ data: { workerId: "sess-tui" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await userEvent.click(await screen.findByRole("button", { name: "Effort" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "High" }));
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		await waitForTaskReady();
		fireEvent.click(screen.getByText("Start task"));

		const fallback = await screen.findByRole("button", { name: "Create as Terminal UI" });
		fireEvent.click(fallback);
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-tui"));
		expect(h.post).toHaveBeenLastCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({ body: expect.objectContaining({ effort: "high", mode: "tui" }) }),
		);
	});

	it("refreshes a stale model list and retries once when the effort cannot be checked", async () => {
		const catalog = { agent: "codex", selectionMode: "catalog", models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["low", "high"] }], allowCustom: false };
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: catalog }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		const unavailable = { error: { code: "MODEL_CAPABILITIES_UNAVAILABLE", message: "spawn: model capabilities unavailable for model \"gpt-5\"" } };
		const delegate = vi.fn()
			.mockResolvedValueOnce(unavailable)
			.mockResolvedValueOnce({ data: { workerId: "sess-after-refresh" } });
		h.post.mockImplementation(async (path: string, request: unknown) => {
			if (path === "/api/v1/agents/{agent}/models/refresh") return { data: catalog };
			if (path === "/api/v1/orchestrators/delegate") return delegate(path, request);
			return { data: {} };
		});
		const onCreated = vi.fn();

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={onCreated} /></Wrap>);
		await waitForTaskReady();
		fireEvent.change(task(), { target: { value: "Retry past a stale catalog" } });
		fireEvent.click(startTask());

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-after-refresh"));
		expect(h.post).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", expect.anything());
		expect(delegate).toHaveBeenCalledTimes(2);
		// One request id for both tries, so a lost first response cannot create a second session.
		expect(delegate.mock.calls[1][1].body.clientRequestId).toBe(delegate.mock.calls[0][1].body.clientRequestId);
	});

	it("offers an explicit approval-less retry from structured capability details", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "cursor", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "cursor", config: {} } } };
		});
		h.post
			.mockResolvedValueOnce({
				error: {
					code: "SESSION_MODE_UNSUPPORTED",
					message: "This provider cannot satisfy the selected approval policy",
					details: {
						missingCapabilities: ["approvals"],
						allowedApprovalModes: ["bypass-permissions"],
					},
				},
			})
			.mockResolvedValueOnce({ data: { workerId: "sess-pi" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "cursor"));
		fireEvent.change(task(), { target: { value: "Use approval-less Chat" } });
		fireEvent.click(screen.getByText("Start task"));

		const fallback = await screen.findByRole("button", { name: "Start without approvals" });
		fireEvent.click(fallback);
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-pi"));
		expect(h.post).toHaveBeenLastCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.objectContaining({ approvalMode: "bypass-permissions" }),
			}),
		);
		expect(h.post.mock.calls[1][1].body).not.toHaveProperty("mode");
	});

	it("starts a standalone Unreal task in Chat after approval-less retry", async () => {
		h.agentCatalog = { agents: [agentReadiness("unreal-agent", "Unreal Agent")] };
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "tui", chatHarnesses: ["unreal-agent"] } };
			}
			if (path.includes("/models")) {
				return { data: { agent: "unreal-agent", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { config: {} } } };
		});
		h.post
			.mockResolvedValueOnce({
				error: {
					code: "SESSION_MODE_UNSUPPORTED",
					message: "This provider cannot satisfy the selected approval policy",
					details: { missingCapabilities: ["approvals"], allowedApprovalModes: ["bypass-permissions"] },
				},
			})
			.mockResolvedValueOnce({ data: { session: { id: "sess-unreal" } } });
		const onCreated = vi.fn();

		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={onCreated} /></Wrap>);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "unreal-agent"));
		fireEvent.change(task(), { target: { value: "Say hello" } });
		fireEvent.click(startTask());
		fireEvent.click(await screen.findByRole("button", { name: "Start without approvals" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-unreal"));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ harness: "unreal-agent", mode: "chat" });
		expect(h.post.mock.calls[1][1].body).toMatchObject({
			harness: "unreal-agent", mode: "chat", approvalMode: "bypass-permissions",
		});
	});

	it("reports a standalone create failure", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		h.post.mockResolvedValue({ error: { code: "SESSION_CREATE_FAILED", message: "no session" } });
		render(<Wrap><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByLabelText("Agent")).toHaveAttribute("data-value", "codex"));
		fireEvent.change(task(), { target: { value: "Say hello" } });
		fireEvent.click(screen.getByText("Start task"));
		expect(await screen.findByRole("alert")).toHaveTextContent("no session");
		expect(h.capture).toHaveBeenCalledWith("ao.renderer.task_create_failed", { scope: "standalone" });
	});

	it("reports dirty then clears it on unmount", () => {
		const onDirtyChange = vi.fn();
		const { unmount } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onDirtyChange={onDirtyChange} />
			</Wrap>,
		);
		fireEvent.change(task(), { target: { value: "T" } });
		expect(onDirtyChange).toHaveBeenLastCalledWith(true);
		unmount();
		expect(onDirtyChange).toHaveBeenLastCalledWith(false);
	});

	it("preselects the project worker agent and spawns with it", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: { status: "ok", project: { agent: "claude-code", config: { worker: { agent: "codex" } } } },
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-3" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "codex"));

		fireEvent.change(task(), { target: { value: "Ship it" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ agent: "codex" }) }),
			),
		);
	});

	it("renders a known default agent without an empty intermediate selection", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["project", "proj-1"], { agent: "codex", config: {} });

		render(
			<QueryClientProvider client={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</QueryClientProvider>,
		);

		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.6 Sol"));
		expect(h.agentValues).not.toContain("");
	});

	it("falls back to the global default agent when the project sets no worker agent", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
	});

	it("exposes effort for any harness whose selected model advertises it", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "claude-code",
						selectionMode: "catalog",
						models: [{ id: "sonnet", label: "Sonnet", isDefault: true, efforts: ["low", "high"] }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "claude-worker" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("Sonnet");
		expect(await screen.findByRole("button", { name: "Effort" })).toBeInTheDocument();
	});

	it("omits the repeated Claude prefix from Claude Code model labels", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "claude-code",
						selectionMode: "catalog",
						models: [
							{ id: "claude-sonnet-4-5", label: "Claude Sonnet 4.5", isDefault: true },
							{ id: "claude-opus-4-7", label: "Claude Opus 4.7" },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("Sonnet 4.5");
		expect(picker).not.toHaveTextContent("Claude");

		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Sonnet 4.5" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Opus 4.7" })).toBeInTheDocument();
		expect(screen.queryByText(/^Claude /)).not.toBeInTheDocument();
	});

	it("preselects the agent's default model when the project configures none", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5-codex", label: "GPT-5 Codex", isDefault: true },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5 Codex");
	});

	it.each([
		{ name: "the newest Opus when nothing was run", agent: "claude-code", lastUsed: {}, expected: "Opus 5.5" },
		{ name: "the most recently run model, ahead of Opus", agent: "claude-code", lastUsed: { "claude-fable-5-1": "2026-10-01T12:00:00Z", "claude-sonnet-5-5": "2026-10-05T12:00:00Z" }, expected: "Sonnet 5.5" },
		{ name: "the project model, ahead of the last run", agent: "claude-code", lastUsed: { "claude-sonnet-5-5": "2026-10-05T12:00:00Z" }, projectModel: "claude-opus-4-8", expected: "Opus 4.8" },
		{ name: "the first listed model for another agent", agent: "codex", lastUsed: {}, expected: "Claude Fable 5.1" },
	])("opens $agent on $name", async ({ agent, lastUsed, projectModel, expected }) => {
		const models = ["fable-5-1", "opus-4-8", "opus-5-5", "sonnet-5-5"].map((slug) => ({
			id: `claude-${slug}`,
			label: `Claude ${slug.replace(/^(\w)/, (c) => c.toUpperCase()).replace(/-(\d)-(\d)/, " $1.$2")}`,
			lastUsedAt: (lastUsed as Record<string, string>)[`claude-${slug}`],
		}));
		h.get.mockImplementation(async (path: string) =>
			path.includes("/models")
				? { data: { agent, selectionMode: "text", models, allowCustom: false } }
				: { data: { status: "ok", project: { agent, config: { worker: { agent, agentConfig: projectModel ? { model: projectModel } : {} } } } } },
		);

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent(expected);
	});

	it("shows the saved frontend models immediately and updates the picker from the DB without a loader", async () => {
		const cached = {
			agentId: "codex", selectionMode: "catalog" as const, customModelEntry: "none" as const,
			models: [{ id: "cached-model", label: "GPT Cached", isDefault: true }],
			allowCustom: false, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z",
		};
		cacheAgentModels(cached, "codex", "");
		const updated = { ...cached, models: [{ id: "fresh-model", label: "GPT Fresh", isDefault: true }] };
		let finishDB!: (value: { data: typeof cached }) => void;
		const dbResponse = new Promise<{ data: typeof cached }>((resolve) => { finishDB = resolve; });
		h.get.mockImplementation(async (path: string, options?: { params?: { query?: { catalogOnly?: boolean } } }) => {
			if (path.includes("/models")) return options?.params?.query?.catalogOnly ? dbResponse : { data: updated };
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["project", "proj-1"], { agent: "codex", config: {} });
		const view = render(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		try {
			expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT Cached");
			expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
			await act(async () => finishDB({ data: updated }));
			await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT Fresh"));
			expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
		} finally {
			finishDB({ data: updated });
			view.unmount();
			queryClient.clear();
		}
	});

	it("keeps the picker visible without a loader while a new agent's DB catalog resolves", async () => {
		let resolveClaudeCatalog!: (value: {
			data: {
				agent: string;
				selectionMode: "text";
				models: Array<{ id: string; label: string; isDefault: boolean }>;
				allowCustom: boolean;
			};
		}) => void;
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				if (request?.params?.path?.agent === "claude-code") {
					return new Promise((resolve) => {
						resolveClaudeCatalog = resolve;
					});
				}
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.6 Sol");
		fireEvent.click(screen.getByTestId("agent-field"));

		expect(screen.getByLabelText("Model")).not.toHaveTextContent("GPT-5.6 Sol");
		expect(screen.queryByRole("status", { name: "Loading models…" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Select model");

		await act(async () => {
			resolveClaudeCatalog({
				data: {
					agent: "claude-code",
					selectionMode: "text",
					models: [{ id: "opus[1m]", label: "opus[1m]", isDefault: true }],
					allowCustom: true,
				},
			});
		});
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("opus[1m]");
	});

	it("selects the first listed model when the provider reports no default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5", label: "GPT-5" }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("GPT-5");

		h.post.mockResolvedValueOnce({ data: { workerId: "sess-unreported-model" } });
		fireEvent.change(task(), { target: { value: "Use the first model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalled());
		expect(h.post.mock.calls[0][1].body).toHaveProperty("model", "gpt-5");
	});

	it("shows Memcode's native model list and sends an explicit choice", async () => {
		h.agentCatalog = { agents: [agentReadiness("memcode", "memcode", { authentication: "configured" })] };
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: { agentId: "memcode", selectionMode: "catalog", models: [{ id: "sonnet", label: "Sonnet 5" }, { id: "opus", label: "Opus 5.5" }], customModelEntry: "none", allowCustom: false } };
			return { data: { status: "ok", project: { agent: "memcode", config: { worker: { agent: "memcode" } } } } };
		});

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

		const picker = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Sonnet 5" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Opus 5.5" }));
		expect(picker).toHaveTextContent("Opus 5.5");
		h.post.mockResolvedValueOnce({ data: { workerId: "memcode-task" } });
		fireEvent.change(task(), { target: { value: "Use the native model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalled());
		expect(h.post.mock.calls[0][1].body).toHaveProperty("model", "opus");
	});

	it("inherits the project worker model without saving it as a task override", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5-codex", label: "GPT-5 Codex", isDefault: true },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						agent: "codex",
						config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } },
					},
				},
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-default-model" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5");
		fireEvent.change(task(), { target: { value: "Use project default model" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).toEqual(expect.objectContaining({ agent: "codex" }));
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex.model).toBe("");
	});

	it("forwards model refresh metadata to the task picker", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5", label: "GPT-5", isDefault: true }],
						allowCustom: true,
						refreshState: "error",
						refreshError: "Provider temporarily unavailable",
						retryAt: "2026-09-23T08:05:00Z",
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await userEvent.click(await screen.findByRole("button", { name: "Model" }));
		expect(await screen.findByTitle("Provider temporarily unavailable")).toBeInTheDocument();
	});

	it("does not render free text when models must be configured in the agent", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agentId: "opencode",
						selectionMode: "catalog",
						models: [],
						customModelEntry: "configured",
						allowCustom: false,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "opencode", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(screen.queryByRole("textbox", { name: "Model" })).not.toBeInTheDocument();
		await userEvent.click(picker);
		expect(screen.getByText("Configure the model in opencode, then refresh.")).toBeInTheDocument();
	});

	it("uses the project worker model as the new task model default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5.1", label: "GPT-5.1" },
						],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } },
					},
				},
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-2" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const model = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(model);
		expect(screen.queryByRole("menuitem", { name: "Use agent model" })).not.toBeInTheDocument();
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "GPT-5.1" }));
		fireEvent.change(task(), { target: { value: "Use the selected model" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({
					body: expect.objectContaining({ model: "gpt-5.1" }),
				}),
			),
		);
	});

	function mockDisabledModels(disabledModels: string[]) {
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "tui", chatHarnesses: [], harnessDefaults: { codex: { disabledModels } } } };
			}
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-default", label: "GPT Default", isDefault: true },
							{ id: "gpt-other", label: "GPT Other" },
						],
						allowCustom: false,
						refreshRecommended: false,
					},
				};
			}
			return { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } };
		});
	}

	it("opens on an offered model when Settings hides the catalog default", async () => {
		mockDisabledModels(["gpt-default"]);
		h.post.mockResolvedValue({ data: { workerId: "sess-offered" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT Other"));
		await userEvent.click(model);
		expect(screen.queryByRole("menuitem", { name: "GPT Default" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.change(task(), { target: { value: "Use an offered model" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ model: "gpt-other" });
	});

	it("keeps the composer usable when the catalog response has no model list", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { status: "ok" } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.get).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", expect.anything()));
		expect(task()).toBeVisible();
		fireEvent.change(task(), { target: { value: "Use the native model" } });
		expect(task()).toHaveValue("Use the native model");
	});

	// A model is always selected: with no reported default, FX starts on its first offered model.
	it.each([false, true])("selects FX's first offered model when no default is reported (unrelated model hidden: %s)", async (hideUnrelated) => {
		h.agentCatalog = { agents: [agentReadiness("fx", "FX")] };
		h.get.mockImplementation(async (path: string) => {
			if (path === "/api/v1/settings") {
				return { data: { defaultSessionMode: "tui", chatHarnesses: [], harnessDefaults: { fx: {
					disabledModels: hideUnrelated ? ["fx:gateway:beta"] : [],
				} } } };
			}
			if (path.includes("/models")) {
				return { data: { agent: "fx", selectionMode: "catalog", allowCustom: true, refreshRecommended: false, models: [
					{ id: "fx:codex:alpha", label: "Codex Alpha", provider: "codex" },
					{ id: "fx:gateway:beta", label: "Gateway Beta", provider: "gateway" },
					{ id: "fx:gateway:zeta", label: "Gateway Zeta", provider: "gateway" },
				] } };
			}
			return { data: { status: "ok", project: { config: { worker: { agent: "fx" } } } } };
		});
		h.post.mockResolvedValue({ data: { workerId: "sess-fx-native" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "fx"));
		const picker = await screen.findByRole("button", { name: "Model" });
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Gateway Zeta" })).toBeInTheDocument();
		if (hideUnrelated) {
			expect(screen.queryByRole("menuitem", { name: "Gateway Beta" })).not.toBeInTheDocument();
		} else {
			expect(screen.getByRole("menuitem", { name: "Gateway Beta" })).toBeInTheDocument();
		}
		await userEvent.keyboard("{Escape}");
		expect(picker).toHaveTextContent("Codex Alpha");
		fireEvent.change(task(), { target: { value: "Start FX on its first model" } });
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ agent: "fx", model: "fx:codex:alpha" });
	});

	it("keeps the catalog default when Settings switches off no model", async () => {
		mockDisabledModels([]);
		h.post.mockResolvedValue({ data: { workerId: "sess-all" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT Default"));
		fireEvent.change(task(), { target: { value: "Use the default model" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({ model: "gpt-default" });
	});

	it("inherits worker effort visually but sends only explicit task overrides", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{
							id: "gpt-test",
							label: "GPT Test",
							isDefault: true,
							efforts: ["default", "low", "high"],
							defaultEffort: "low",
						}],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: { status: "ok", project: { config: { worker: { agent: "codex", agentConfig: {
					model: "gpt-test", effort: "high",
				} } } } },
			};
		});
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("GPT Test");
		const effortPicker = await screen.findByRole("button", { name: "Effort" });
		expect(effortPicker).toHaveTextContent("High");

		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("effort");

		await userEvent.click(effortPicker);
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "Low" }));
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(2));
		expect(h.post.mock.calls[1][1].body).toEqual(expect.objectContaining({ effort: "low" }));

		await userEvent.click(effortPicker);
		expect(screen.queryByRole("menuitem", { name: "Default" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "High" }));
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(3));
		expect(h.post.mock.calls[2][1].body).not.toHaveProperty("effort");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex).not.toHaveProperty("effort");
	});

	it("returns to the reported agent effort without pinning it", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"], defaultEffort: "low" },
			], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Effort" });
		await waitFor(() => expect(picker).toHaveTextContent("Low"));
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Low" }));
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("effort");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex).not.toHaveProperty("effort");
	});

	it("offers only concrete effort choices when the agent reports no preferred level", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"] },
			], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex" } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-tuned" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Effort" });
		await userEvent.click(picker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await userEvent.click(picker);
		expect(screen.queryByRole("menuitem", { name: "Use agent effort" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).toEqual(expect.objectContaining({ effort: "high" }));
	});

	it("shows a stored implicit model as the catalog choice without pinning it", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test", isDefault: true }], allowCustom: true } }
			: { data: { status: "ok", project: { config: { worker: { agent: "codex", agentConfig: { model: "default" } } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		expect(await screen.findByRole("button", { name: "Model" })).toHaveTextContent("GPT Test");
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("model");
		expect(JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex.model).toBe("");
	});

	it.each([
		{ selectionMode: "catalog", preferred: "GPT New", alternative: "GPT Old", preferenceField: "model", expectedModel: "preferred" },
		{ selectionMode: "mode", preferred: "Plan", alternative: "Fast", preferenceField: "mode", expectedModel: undefined },
	] as const)("returns $selectionMode to the provider's choice after a custom selection", async ({ selectionMode, preferred, alternative, preferenceField, expectedModel }) => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode, models: [
				{ id: "preferred", label: preferred, isDefault: true },
				{ id: "alternative", label: alternative },
			], allowCustom: selectionMode === "catalog" } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(preferred));
		const picker = screen.getByRole("button", { name: "Model" });
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: alternative }));
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body.model).toBe("alternative");

		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: preferred }));
		expect(picker).toHaveTextContent(preferred);
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(2));
		expect(h.post.mock.calls[1][1].body.model).toBe(expectedModel);
		const saved = JSON.parse(window.localStorage.getItem("ao.taskComposer.preferences.v1") ?? "{}")["proj-1"].agents.codex;
		expect(saved[preferenceField]).toBe(expectedModel ?? "");
	});

	it("overrides an explicitly configured project model when choosing the provider's model", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-new", label: "GPT New", isDefault: true },
				{ id: "gpt-old", label: "GPT Old" },
			], allowCustom: true } }
			: { data: { status: "ok", project: { agent: "codex", config: { worker: { agent: "codex", agentConfig: { model: "gpt-old" } } } } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("GPT Old");
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT New" }));
		expect(picker).toHaveTextContent("GPT New");
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body.model).toBe("gpt-new");
	});

	it("selects the first mode when the agent reports no preferred mode", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "mode", models: [{ id: "fast", label: "Fast" }, { id: "deep", label: "Deep" }], allowCustom: false } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });

		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		// The mode picker replaces the model picker once the catalog says it lists modes.
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Fast"));
		fireEvent.change(task(), { target: { value: "Use the first mode" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledOnce());
		expect(h.post.mock.calls[0][1].body).toHaveProperty("model", "fast");
	});

	describe("model login errors", () => {
		const expiredDetail = "claude-code model discovery: claude-code: model discovery: Anthropic rejected the credential: OAuth access token has expired. Re-authenticate to continue.";
		function claudeCatalog(extra: Record<string, unknown>) {
			return {
				agent: "claude-code",
				selectionMode: "catalog",
				models: [
					{ id: "sonnet", label: "Sonnet 5.5" },
					{ id: "opus", label: "Opus 5.5" },
				],
				allowCustom: true,
				stale: true,
				...extra,
			};
		}
		function serveClaude(catalog: () => Record<string, unknown>) {
			h.get.mockImplementation(async (path: string) => path.includes("/models")
				? { data: catalog() }
				: { data: { status: "ok", project: { agent: "claude-code", config: {} } } });
		}

		afterEach(() => {
			useUiStore.setState({ settingsModal: null });
		});

		it("replaces the raw provider error with a login shortcut and an inline retry", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			serveClaude(() => claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			h.post.mockImplementation(async (path: string) => path === "/api/v1/agents/{agent}/models/refresh"
				? { data: claudeCatalog({ stale: false }) }
				: { data: {} });

			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);

			const notice = await screen.findByTestId("model-catalog-notice");
			expect(notice).toHaveTextContent("Claude Code isn't logged in on this computer, so its models couldn't load.");
			expect(screen.queryByText(expiredDetail)).not.toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Details" }));
			expect(screen.getByText(expiredDetail)).toBeInTheDocument();

			await userEvent.click(screen.getByRole("button", { name: "Log in" }));
			expect(useUiStore.getState().settingsModal).toEqual(expect.objectContaining({
				scope: "global", section: "harness", focusAgentId: "claude-code", harnessView: "local", startLogin: true,
			}));

			await userEvent.click(screen.getByRole("button", { name: "Retry" }));
			await waitFor(() => expect(h.post).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", expect.objectContaining({
				params: expect.objectContaining({ path: { agent: "claude-code" } }),
			})));
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});

		it("explains an expired login that Claude Code renews on its own", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "configured" })] };
			serveClaude(() => claudeCatalog({ warning: "the saved Claude Code login token expired", warningCode: "auth_expired" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			expect(await screen.findByTestId("model-catalog-notice")).toHaveTextContent("Claude Code's saved login has expired.");
			expect(screen.getByRole("button", { name: "Log in again" })).toBeInTheDocument();
		});

		it("recognizes a login error from a daemon that predates warning codes", async () => {
			serveClaude(() => claudeCatalog({ warning: expiredDetail }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			expect(await screen.findByTestId("model-catalog-notice")).toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Log in" })).toBeInTheDocument();
		});

		it("keeps other model warnings as plain text", async () => {
			serveClaude(() => claudeCatalog({ warning: "provider temporarily unavailable" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			expect(await screen.findByText("provider temporarily unavailable")).toBeInTheDocument();
			expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument();
		});

		// A cloud task runs with the cloud credential and the control plane's
		// catalog, so this computer's login never reads as a problem.
		it("keeps a cloud project startable without consulting the local login", async () => {
			h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent: "claude-code" } } });
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			h.get.mockImplementation(async () => ({ data: claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }) }));
			h.cloudCreateSession.mockResolvedValue({ session: { id: "session-1" } });
			const onCreated = vi.fn();
			render(<Wrap><TaskComposer projectId="cloud-1" onCreated={onCreated} /></Wrap>);

			await screen.findByRole("textbox", { name: "Task" });
			expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument();
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			fireEvent.change(task(), { target: { value: "Do the work" } });
			await waitForTaskReady();
			fireEvent.click(startTask());
			await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		});

		it("shows no local-login notice on a cloud project whose agent is connected for cloud", async () => {
			h.cloudProjects.push({ id: "cloud-1", displayName: "Cloud", repositoryUrl: "https://example.com/repo", defaultBranch: "main", config: { worker: { agent: "claude-code" } } });
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			h.get.mockImplementation(async () => ({ data: claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }) }));
			h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([{ id: "sonnet", displayName: "Sonnet", default: true }]));
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			queryClient.setQueryData(providerConnectionsQueryKey, [
				{ id: "c-1", provider: "claude-code", label: "default", validationState: "valid", config: { credentialType: "oauth" } },
			]);
			render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
			await screen.findByRole("textbox", { name: "Task" });
			await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalled());
			expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument();
			expect(h.get.mock.calls.filter(([path]) => String(path).includes("/models"))).toEqual([]);
		});

		it("re-reads the catalog by itself once the agent reads as logged in", async () => {
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "unauthorized" })] };
			let loggedIn = false;
			serveClaude(() => loggedIn ? claudeCatalog({ stale: false }) : claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			const view = render(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await screen.findByTestId("model-catalog-notice");

			loggedIn = true;
			h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code", { authentication: "authorized" })] };
			view.rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});

		it("re-reads the catalog when the window regains focus while a login error shows", async () => {
			let loggedIn = false;
			serveClaude(() => loggedIn ? claudeCatalog({ stale: false }) : claudeCatalog({ warning: expiredDetail, warningCode: "auth_required" }));
			render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
			await screen.findByTestId("model-catalog-notice");
			loggedIn = true;
			act(() => { window.dispatchEvent(new Event("focus")); });
			await waitFor(() => expect(screen.queryByTestId("model-catalog-notice")).not.toBeInTheDocument());
		});
	});
});

describe("TaskComposer Cloud model catalog", () => {
	it("lists the release catalog without a live worker and starts on the default", async () => {
		const queryClient = cloudFixture("claude-code");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "claude-sonnet-test", displayName: "Sonnet", default: true, efforts: ["low", "medium", "high"] },
			{ id: "claude-opus-test", displayName: "Opus", default: false },
		]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledWith("org-1", "claude-code", { signal: expect.any(AbortSignal) }));
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Sonnet" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Use project or agent default" })).toBeInTheDocument();
		expect(screen.getByText("Models for Claude Code 2.1.287")).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		// The catalog's own default is the agent's, not a pick made for the user.
		expect(picker).toHaveTextContent("Project or agent default");
		expect(screen.queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Use the default" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("model");
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("reasoningEffort");
		expect(localModelReads()).toEqual([]);
	});

	it("says when Cloud workers verified the catalog and sends the chosen model and effort", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValue({ source: "worker", cliVersion: "0.130.0", models: [
			{ id: "gpt-5.6-sol", displayName: "GPT-5.6 Sol", default: true, efforts: ["low", "high"] },
		] });
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("GPT-5.6 Sol");
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.getByText("Verified on your Cloud workers")).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		fireEvent.change(task(), { target: { value: "Use the verified model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "gpt-5.6-sol", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
		expect(screen.queryByText(/may not run/)).not.toBeInTheDocument();
	});

	it("never lists this computer's models beside Cloud's", async () => {
		const queryClient = cloudFixture("codex");
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [{ id: "gpt-6-luna", label: "GPT-6 Luna", isDefault: true }], allowCustom: true } }
			: { data: { status: "ok", project: { config: {} } } });
		let finishCatalog!: (catalog: ReturnType<typeof releaseCatalog>) => void;
		h.cloudListUserAgentModels.mockReturnValue(new Promise((resolve) => { finishCatalog = resolve; }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		const picker = await screen.findByRole("button", { name: "Model" });
		// Usable on the default while Cloud answers.
		expect(picker).toBeEnabled();
		expect(picker).toHaveTextContent("Project or agent default");
		act(() => finishCatalog(releaseCatalog([{ id: "gpt-5.6-sol", displayName: "GPT-5.6 Sol", default: true, efforts: ["low", "high"] }], "0.130.0")));
		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "GPT-5.6 Sol" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "GPT-6 Luna" })).not.toBeInTheDocument();
		expect(screen.getByText("Models for Codex 0.130.0")).toBeInTheDocument();
		expect(localModelReads()).toEqual([]);
	});

	it("keeps OpenCode's credential-scoped discovery when Cloud has no catalog for it", async () => {
		const queryClient = cloudFixture("opencode", {}, [
			{ id: "o", provider: "opencode", label: "default", validationState: "valid", updatedAt: "2026-10-24T00:00:00Z", config: { credentialType: "anthropic_api_key" } },
		]);
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		h.get.mockResolvedValue({ data: { agent: "opencode", selectionMode: "catalog", models: [{ id: "anthropic/sonnet", label: "Connected Sonnet", isDefault: true }], allowCustom: true } });
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Connected Sonnet");
		expect(h.cloudListUserAgentModels).toHaveBeenCalledWith("org-1", "opencode", { signal: expect.any(AbortSignal) });
		expect(h.get).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", expect.objectContaining({ params: expect.objectContaining({ query: { projectId: "@cred:anthropic_api_key" } }) }));
		// Only the credential's own catalog, never this device's.
		expect(localModelReads().every(([, init]) => init.params.query.projectId === "@cred:anthropic_api_key")).toBe(true);
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.getByText("From your connected provider's model list")).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		expect(screen.queryByText(/Cloud has no model list/)).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Use the connected provider" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "anthropic/sonnet" }), { idempotencyKey: expect.any(String) }));
	});

	it.each([
		{ agent: "cursor", config: {} },
		{ agent: "opencode", config: {} },
	])("offers the default and any model ID when Cloud has no $agent catalog", async ({ agent, config }) => {
		const queryClient = cloudFixture(agent, {}, [{ id: "c", provider: agent, label: "default", validationState: "valid", updatedAt: "2026-10-24T00:00:00Z", config }]);
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Cloud has no model list for/);
		const picker = screen.getByRole("button", { name: "Model" });
		expect(picker).toBeEnabled();
		expect(picker).toHaveTextContent("Project or agent default");
		await enterCloudModel("explicit-cloud-model");
		fireEvent.change(task(), { target: { value: "Run the explicit model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "explicit-cloud-model" }), { idempotencyKey: expect.any(String) }));
		expect(localModelReads()).toEqual([]);
	});

	it.each([404, 501])("lists this computer's models as unverified suggestions for an older control plane (%s)", async (status) => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("Old server", { status }));
		h.get.mockResolvedValue({ data: { agent: "codex", selectionMode: "catalog", models: [{ id: "legacy-model", label: "Legacy model", isDefault: true }], allowCustom: true } });
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/This Cloud server can't list its models/);
		await chooseCloudModel("Legacy model");
	});

	it.each(["codex", "claude-code"])("does not start an older control plane's %s task on a suggestion", async (agent) => {
		const queryClient = cloudFixture(agent);
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
		h.get.mockResolvedValue({ data: { agent, selectionMode: "catalog", models: [{ id: "desktop-opus", label: "Desktop Opus", isDefault: true }], allowCustom: true } });
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/This Cloud server can't list its models/);
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expect(await screen.findByRole("menuitem", { name: "Desktop Opus" })).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Project or agent default");
		fireEvent.change(task(), { target: { value: "Use native default" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("model");
	});

	it.each([{ status: 404, code: "not_found" }, { status: 501, code: "not_implemented" }])("does not take a handler's $code for an older control plane", async ({ status, code }) => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("Handler error", { status, code }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Couldn't load Cloud's models for Codex/);
		expect(localModelReads()).toEqual([]);
	});

	it.each([
		{ status: 502, code: "BAD_GATEWAY" },
		{ status: 504, code: "WORKER_TIMEOUT" },
		{ status: 429, code: "TOO_MANY_REQUESTS" },
	])("keeps the default and manual entry when the catalog fails with $status", async ({ status, code }) => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("Unavailable", { status, code }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Couldn't load Cloud's models for Codex/);
		const picker = screen.getByRole("button", { name: "Model" });
		expect(picker).toBeEnabled();
		expect(picker).toHaveTextContent("Project or agent default");
		await enterCloudModel("explicit-worker-model");
		fireEvent.change(task(), { target: { value: "Run explicit choice" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "explicit-worker-model" }), { idempotencyKey: expect.any(String) }));
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Use project or agent default" }));
		fireEvent.change(task(), { target: { value: "Run native default" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledTimes(2));
		expect(h.cloudCreateSession.mock.calls[1]?.[1]).not.toHaveProperty("model");
		expect(h.cloudCreateSession.mock.calls[1]?.[1]).not.toHaveProperty("reasoningEffort");
		expect(localModelReads()).toEqual([]);
	});

	it("drops an effort the newly chosen listed model does not take", async () => {
		const queryClient = cloudFixture("claude-code");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "sonnet", displayName: "Sonnet", default: false, efforts: ["low", "medium", "high"] },
			{ id: "haiku", displayName: "Haiku", default: false },
		]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Sonnet");
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Low" }));
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Haiku" }));
		fireEvent.change(task(), { target: { value: "Use Haiku" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "haiku" }), { idempotencyKey: expect.any(String) }));
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("reasoningEffort");
	});

	it("keeps a configured model the catalog does not list, and warns that it may not run", async () => {
		const queryClient = cloudFixture("codex", { model: "configured-model", effort: "high" });
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "gpt-5.6-sol", displayName: "GPT-5.6 Sol", default: true, efforts: ["low", "high"] },
		], "0.130.0"));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		const warning = "configured-model isn't in Cloud's model list for Codex, so it may not run.";
		await screen.findByText(warning);
		const picker = screen.getByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("configured-model");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.6 Sol" }));
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
		// Still listed, so it can be chosen again.
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: "configured-model" }));
		expect(screen.getByText(warning)).toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Use the configured model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "configured-model", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("preserves a configured model and effort when Cloud has no catalog", async () => {
		const queryClient = cloudFixture("codex", { model: "configured-model", effort: "high" });
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Cloud has no model list for Codex/);
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("configured-model");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		fireEvent.change(task(), { target: { value: "Use configured choices" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "configured-model", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("retains an explicit model and effort through a failed refetch and a changed catalog", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValueOnce(releaseCatalog([{ id: "chosen", displayName: "Chosen", default: false, efforts: ["high"] }]))
			.mockRejectedValueOnce(new CloudCpError("Timeout", { status: 504, code: "WORKER_TIMEOUT" }))
			.mockResolvedValue(releaseCatalog([{ id: "other", displayName: "Other", default: true }]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Chosen" }));
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await act(() => queryClient.invalidateQueries({ queryKey: ["cloud-agent-models"] }));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Chosen");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		expect(screen.queryByText(/Couldn't load/)).not.toBeInTheDocument();
		await act(() => queryClient.invalidateQueries({ queryKey: ["cloud-agent-models"] }));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("chosen");
		expect(screen.getByText(/chosen isn't in Cloud's model list/)).toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Retain my choices" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "chosen", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("waits for Cloud's catalog before starting, so the effort matches the model", async () => {
		const queryClient = cloudFixture("claude-code", { model: "haiku", effort: "high" });
		let finishCatalog!: (value: ReturnType<typeof releaseCatalog>) => void;
		h.cloudListUserAgentModels.mockReturnValue(new Promise((resolve) => { finishCatalog = resolve; }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalled());
		fireEvent.change(task(), { target: { value: "Use Haiku" } });
		expect(startTask()).toBeDisabled();
		await act(async () => finishCatalog(releaseCatalog([{ id: "haiku", displayName: "Haiku", default: false }])));
		await waitFor(() => expect(startTask()).toBeEnabled());
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "haiku" }), { idempotencyKey: expect.any(String) }));
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("reasoningEffort");
	});

	it("drops another model's effort when the default is chosen", async () => {
		const queryClient = cloudFixture("claude-code");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "opus", displayName: "Opus", default: false, efforts: ["low", "high", "max"] },
		]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Opus");
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Max" }));
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Use project or agent default" }));
		fireEvent.change(task(), { target: { value: "Use the default" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("model");
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("reasoningEffort");
	});

	it("keeps Scratchpad's choices when the composer leaves a Cloud project", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([{ id: "gpt-6-luna", displayName: "GPT-6-Luna", default: true }], "0.165.0"));
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex", { usageCount: 3, lastUsedAt: "2026-09-20T12:00:00Z" })] };
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "text", allowCustom: true, models: [
				{ id: "gpt-6-astra", label: "GPT-6-Astra", isDefault: true },
				{ id: "gpt-5.5", label: "GPT-5.5" },
			] } }
			: { data: { status: "ok", project: { config: {} } } });
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		const model = await screen.findByRole("button", { name: "Model" });
		await waitFor(() => expect(model).toHaveTextContent("GPT-6-Astra"));
		await userEvent.click(model);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5.5" }));
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalled());
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="__standalone__" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("GPT-5.5"));
	});

	it("keeps the configured effort for an entered model the catalog does not list", async () => {
		const queryClient = cloudFixture("codex", { model: "configured-model", effort: "high" });
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([{ id: "listed-no-levels", displayName: "Listed without levels", default: false }]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalled());
		await enterCloudModel("manual-unlisted");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		fireEvent.change(task(), { target: { value: "Retain configured effort" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "manual-unlisted", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("sends no configured effort with a listed model that reports no levels", async () => {
		const queryClient = cloudFixture("codex", { model: "configured-model", effort: "high" });
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([{ id: "listed-no-levels", displayName: "Listed without levels", default: false }]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalled());
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Listed without levels" }));
		fireEvent.change(task(), { target: { value: "Use the listed model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "listed-no-levels" }), { idempotencyKey: expect.any(String) }));
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("reasoningEffort");
	});

	it("sends a listed model with one of its advertised efforts", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([
			{ id: "gpt-5.6-sol", displayName: "GPT-5.6 Sol", default: true, efforts: ["low", "high"] },
			{ id: "gpt-5.6-luna", displayName: "GPT-5.6 Luna", default: false, efforts: ["medium"] },
		]));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("GPT-5.6 Luna");
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Medium" }));
		fireEvent.change(task(), { target: { value: "Use the supported model" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "gpt-5.6-luna", reasoningEffort: "medium" }), { idempotencyKey: expect.any(String) }));
	});

	it("passes the cancellation signal and reuses a fresh catalog on reopen", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValue(releaseCatalog([{ id: "listed", displayName: "Listed", default: true }]));
		const first = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledOnce());
		expect(h.cloudListUserAgentModels.mock.calls[0][2].signal).toBeInstanceOf(AbortSignal);
		first.unmount();
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Listed");
		expect(h.cloudListUserAgentModels).toHaveBeenCalledOnce();
	});

	it("waits for the provider connections and keeps a choice made meanwhile", async () => {
		const queryClient = cloudFixture("codex", { effort: "high" });
		queryClient.removeQueries({ queryKey: providerConnectionsQueryKey });
		h.cloudListProviderConnections.mockReturnValue(new Promise(() => undefined));
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await enterCloudModel("pending-choice");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		expect(h.cloudListUserAgentModels).not.toHaveBeenCalled();
		act(() => queryClient.setQueryData(providerConnectionsQueryKey, [{ id: "loaded-connection", provider: "codex", label: "default", validationState: "valid", updatedAt: "2026-10-24T00:00:00Z", config: {} }]));
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledOnce());
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("pending-choice");
		fireEvent.change(task(), { target: { value: "Keep choices through hydration" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "pending-choice", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
	});

	it("keeps a manual choice when the project turns out to be a cloud project", async () => {
		const queryClient = cloudFixture("codex", { effort: "high" });
		const project = h.cloudProjects.pop()!;
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", models: [{ id: "manual-choice", label: "Manual choice" }], allowCustom: true, customModelEntry: "direct", selectionMode: "catalog" } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await userEvent.click(await screen.findByRole("button", { name: "Model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Manual choice" }));
		h.cloudProjects.push(project);
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Cloud has no model list for Codex/);
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("manual-choice");
	});

	it("remembers each agent's explicit Cloud choice across agent switches", async () => {
		const queryClient = cloudFixture("codex", { effort: "high" }, ["codex", "claude-code"].map((provider) => ({ id: provider, provider, label: "default", validationState: "valid", updatedAt: "2026-10-24T00:00:00Z", config: {} })));
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		for (const model of ["codex-choice", "claude-choice"]) {
			await enterCloudModel(model);
			await userEvent.click(screen.getByRole("button", { name: "Agent" }));
		}
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("codex-choice");
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		fireEvent.change(task(), { target: { value: "Use remembered Codex choice" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "codex-choice", reasoningEffort: "high" }), { idempotencyKey: expect.any(String) }));
		await userEvent.click(screen.getByRole("button", { name: "Agent" }));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("claude-choice");
	});

	it("starts over and asks again when the signed-in Cloud user changes", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await enterCloudModel("user-one-choice");
		h.cloudUserId = "user-2";
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledTimes(2));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Project or agent default");
		fireEvent.change(task(), { target: { value: "Use new identity defaults" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		expect(h.cloudCreateSession.mock.calls[0][1]).not.toHaveProperty("model");
	});

	it("does not reuse another organization's catalog", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValueOnce(releaseCatalog([{ id: "org-a-model", displayName: "Org A model", default: true }]))
			.mockRejectedValue(new CloudCpError("Unavailable", { status: 502, code: "BAD_GATEWAY" }));
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Org A model");
		h.cloudOrgId = "org-2";
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await screen.findByText(/Couldn't load Cloud's models/);
		expect(h.cloudListUserAgentModels).toHaveBeenLastCalledWith("org-2", "codex", { signal: expect.any(AbortSignal) });
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Project or agent default");
	});

	it("asks the new control plane after a server switch with equal organization and credential IDs", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels.mockResolvedValueOnce(releaseCatalog([{ id: "endpoint-a-model", displayName: "Endpoint A model", default: true }]))
			.mockResolvedValueOnce(releaseCatalog([{ id: "endpoint-b-model", displayName: "Endpoint B model", default: true }]));
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Endpoint A model");
		h.cloudBaseUrl = "https://cloud-b.example.test";
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Endpoint B model");
		expect(h.cloudListUserAgentModels.mock.calls.map((args) => args.slice(0, 2))).toEqual([["org-1", "codex"], ["org-1", "codex"]]);
	});

	it("ignores a late answer from the previous control plane", async () => {
		const queryClient = cloudFixture("codex");
		let finishOldCatalog!: (value: ReturnType<typeof releaseCatalog>) => void;
		h.cloudListUserAgentModels.mockReturnValueOnce(new Promise((resolve) => { finishOldCatalog = resolve; }))
			.mockResolvedValueOnce(releaseCatalog([{ id: "endpoint-b-model", displayName: "Endpoint B model", default: true }]));
		const { rerender } = render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledOnce());
		h.cloudBaseUrl = "https://cloud-b.example.test";
		rerender(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(h.cloudListUserAgentModels).toHaveBeenCalledTimes(2));
		await chooseCloudModel("Endpoint B model");
		await act(async () => finishOldCatalog(releaseCatalog([{ id: "late-endpoint-a-model", displayName: "Late endpoint A model", default: true }])));
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Endpoint B model");
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.queryByRole("menuitem", { name: "Late endpoint A model" })).not.toBeInTheDocument();
	});

	it("asks again when the agent's Cloud connection changes", async () => {
		const queryClient = cloudFixture("codex");
		h.cloudListUserAgentModels
			.mockResolvedValueOnce(releaseCatalog([{ id: "account-a", displayName: "Account A", default: true }]))
			.mockResolvedValueOnce({ source: "worker", models: [{ id: "account-b", displayName: "Account B", default: true }] });
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Account A");
		act(() => queryClient.setQueryData(providerConnectionsQueryKey, [
			{ id: "codex-b", provider: "codex", label: "default", validationState: "valid", updatedAt: "2026-10-24T01:00:00Z", config: {} },
		]));
		await chooseCloudModel("Account B");
		expect(h.cloudListUserAgentModels).toHaveBeenCalledTimes(2);
	});

	it("drops a choice made for the previous credential after a resave", async () => {
		const connection = { id: "c", provider: "codex", label: "default", validationState: "valid", updatedAt: "2026-10-20T00:00:00Z", config: {} };
		const queryClient = cloudFixture("codex", {}, [connection]);
		h.cloudListUserAgentModels.mockResolvedValueOnce(releaseCatalog([{ id: "old-model", displayName: "Old model", default: true, efforts: ["high"] }]))
			.mockRejectedValue(new CloudCpError("No catalog", { status: 409, code: "MODEL_CATALOG_UNAVAILABLE" }));
		render(<Wrap queryClient={queryClient}><TaskComposer projectId="cloud-1" onCreated={vi.fn()} /></Wrap>);
		await chooseCloudModel("Old model");
		act(() => queryClient.setQueryData(providerConnectionsQueryKey, [{ ...connection, updatedAt: "2026-10-21T00:00:00Z" }]));
		await screen.findByText(/Cloud has no model list for Codex/);
		expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Project or agent default");
		fireEvent.change(task(), { target: { value: "Start after credential resave" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.cloudCreateSession).toHaveBeenCalled());
		expect(h.cloudCreateSession.mock.calls[0]?.[1]).not.toHaveProperty("model");
		expect(h.cloudCreateSession.mock.calls[0]?.[1]).not.toHaveProperty("reasoningEffort");
	});

	it("leaves a local project on this computer's catalog and its default", async () => {
		h.get.mockImplementation(async (path: string) => path.includes("/models")
			? { data: { agent: "codex", selectionMode: "catalog", models: [{ id: "gpt-local", label: "Local GPT", isDefault: true }, { id: "gpt-other", label: "Other GPT" }], allowCustom: true } }
			: { data: { status: "ok", project: { agent: "codex", config: {} } } });
		h.post.mockResolvedValue({ data: { workerId: "local-session" } });
		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		await waitFor(() => expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("Local GPT"));
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expect(screen.queryByRole("menuitem", { name: "Use project or agent default" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		fireEvent.change(task(), { target: { value: "Run locally" } });
		await waitForTaskReady();
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.objectContaining({ body: expect.objectContaining({ model: "gpt-local" }) })));
		expect(h.get).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", expect.anything());
		expect(h.cloudListUserAgentModels).not.toHaveBeenCalled();
	});

	it("keeps local Cursor effort and thinking variants", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) return { data: {
				agentId: "cursor", selectionMode: "catalog", customModelEntry: "none", allowCustom: false,
				models: [
					{ id: "claude-opus-4-8-low", label: "Claude Opus 4.8 Low", isDefault: true },
					{ id: "claude-opus-4-8-high", label: "Claude Opus 4.8 High" },
					{ id: "claude-opus-4-8-thinking-low", label: "Claude Opus 4.8 Low Thinking" },
					{ id: "claude-opus-4-8-thinking-high", label: "Claude Opus 4.8 High Thinking" },
				],
			} };
			if (path === "/api/v1/settings") return { data: { defaultSessionMode: "tui" } };
			return { data: { status: "ok", project: { agent: "cursor", config: {} } } };
		});
		h.post.mockResolvedValue({ data: { workerId: "cursor-session" } });
		render(<Wrap><TaskComposer projectId="proj-1" onCreated={vi.fn()} /></Wrap>);
		const effortPicker = await screen.findByRole("button", { name: "Effort" });
		await userEvent.click(effortPicker);
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		await userEvent.click(effortPicker);
		await userEvent.click(screen.getByText("Thinking"));
		expect(screen.getByRole("switch", { name: "Thinking" })).toBeChecked();
		await userEvent.keyboard("{Escape}");
		fireEvent.change(task(), { target: { value: "Run Cursor with high thinking" } });
		fireEvent.click(startTask());
		await waitFor(() => expect(h.post).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.objectContaining({ body: expect.objectContaining({ model: "claude-opus-4-8-thinking-high" }) })));
		expect(h.cloudListUserAgentModels).not.toHaveBeenCalled();
	});
});

describe("useTaskLaunch agent choices", () => {
	function Choices() {
		const task = useTaskLaunch({ projectId: "proj-1" });
		return <ul>{task.agentChoices.map((agent) => <li key={agent.id}>{agent.id}</li>)}</ul>;
	}

	it("offers locally only agents that can start, never an \"Auth unknown\" one", async () => {
		h.get.mockResolvedValue({ data: { status: "ok", project: { agent: "claude-code", config: {} } } });
		h.agentCatalog = { agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("amp", "Amp", { authentication: "unknown" }),
			agentReadiness("fx", "fx", { authentication: "configured" }),
			agentReadiness("goose", "Goose", { authentication: "unauthorized" }),
			agentReadiness("crush", "Crush", { installation: "unknown" }),
			agentReadiness("aider", "Aider", { installation: "not_installed" }),
		] };
		render(<Wrap><Choices /></Wrap>);
		await waitFor(() => expect(screen.getAllByRole("listitem").map((item) => item.textContent).sort()).toEqual(["claude-code", "fx"]));
	});
});

describe("useTaskLaunch on the new chat page", () => {
	function LoadError() {
		const task = useTaskLaunch({ projectId: "__standalone__" });
		return <p>{task.remoteLoadError ?? "ready"}</p>;
	}

	it("says at once that Scratchpad has no ready agent", async () => {
		h.agentCatalog = { agents: [agentReadiness("goose", "Goose", { authentication: "unauthorized" })] };
		render(<Wrap><LoadError /></Wrap>);
		await screen.findByText("No agent is ready. Set one up in Settings.");
	});

	it("reports no load error for Scratchpad once an agent is ready", async () => {
		h.agentCatalog = { agents: [agentReadiness("codex", "Codex")] };
		render(<Wrap><LoadError /></Wrap>);
		await screen.findByText("ready");
	});

	it("keeps the prepared worktree a send in flight will claim when the page goes away", async () => {
		h.agentCatalog = { agents: [agentReadiness("claude-code", "Claude Code")] };
		h.get.mockResolvedValue({ data: { status: "ok", project: { id: "proj-1", config: { worker: { agent: "claude-code" } } } } });
		h.delete.mockResolvedValue({});
		let created!: (value: unknown) => void;
		h.post.mockImplementation(async (path: string) => {
			if (path === "/api/v1/projects/{id}/tasks/prepare") return { data: { taskPreparation: "proj-1-7" } };
			if (path === "/api/v1/orchestrators/delegate") return new Promise((done) => { created = done; });
			return { data: {} };
		});
		// The readiness check before the create is the window in which the page can go away.
		let checked!: () => void;
		h.ensureTargetedReadiness.mockReturnValue(new Promise((done) => { checked = () => done({ agents: [] }); }));
		let task!: ReturnType<typeof useTaskLaunch>;
		function Launcher() {
			task = useTaskLaunch({ projectId: "proj-1" });
			return <p>{task.preparedSessionId || "unprepared"}{task.canSubmit ? " ready" : ""}</p>;
		}
		const { unmount } = render(<Wrap><Launcher /></Wrap>);
		await screen.findByText("proj-1-7 ready");
		let launched!: Promise<string>;
		act(() => { launched = task.launch("Fix it"); });
		unmount();
		expect(h.delete).not.toHaveBeenCalled();

		await act(async () => { checked(); });
		await waitFor(() => expect(h.post).toHaveBeenCalledWith("/api/v1/orchestrators/delegate", expect.objectContaining({
			body: expect.objectContaining({ taskPreparation: "proj-1-7" }),
		})));
		await act(async () => {
			created({ data: { workerId: "proj-1-7" } });
			await launched;
		});
		// Answered, the send releases it as any send does.
		expect(h.delete).toHaveBeenCalledWith("/api/v1/task-preparations/{token}", { params: { path: { token: "proj-1-7" } } });
	});
});
