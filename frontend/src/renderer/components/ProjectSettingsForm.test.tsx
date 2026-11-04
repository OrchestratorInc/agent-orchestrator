import { useState, type ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const { getMock, putMock, postMock, navigateMock, closeSettingsMock, openGlobalSettingsMock, ensureAgentReadinessMock, trackerIntakeGate } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	postMock: vi.fn(),
	navigateMock: vi.fn(),
	closeSettingsMock: vi.fn(),
	openGlobalSettingsMock: vi.fn(),
	ensureAgentReadinessMock: vi.fn(),
	trackerIntakeGate: { enabled: true },
}));

vi.mock("../hooks/useAgentReadinessQuery", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useAgentReadinessQuery")>();
	return { ...actual, useEnsureAgentReadiness: ensureAgentReadinessMock };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
	};
});

vi.mock("../stores/ui-store", () => ({
	useUiStore: (selector: (state: Record<string, unknown>) => unknown) =>
		selector({
			closeSettings: closeSettingsMock,
			openGlobalSettings: openGlobalSettingsMock,
		}),
}));

vi.mock("../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { trackerIntakeEnabled: trackerIntakeGate.enabled }, isLoading: false, error: undefined }),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		PUT: putMock,
		POST: postMock,
	},
	apiErrorCode: (error: unknown) =>
		typeof error === "object" && error !== null && "code" in error
			? String((error as { code: unknown }).code)
			: undefined,
	apiErrorRequestId: (error: unknown) =>
		typeof error === "object" && error !== null && "requestId" in error
			? String((error as { requestId: unknown }).requestId)
			: undefined,
	apiErrorDetails: (error: unknown) =>
		typeof error === "object" && error !== null && "details" in error
			? (error as { details: Record<string, unknown> }).details
			: undefined,
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return "Request failed";
	},
}));

import { ProjectSettingsForm, type ProjectSettingsSaveState, type ProjectSettingsSection } from "./ProjectSettingsForm";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";

async function beginEdit(label: string) {
	await userEvent.click(await screen.findByRole("button", { name: `Edit ${label}` }));
	return screen.getByLabelText(label);
}

function TestProjectSettings({
	projectId,
	section,
}: {
	projectId: string;
	section?: ProjectSettingsSection;
}) {
	const [saveState, setSaveState] = useState<ProjectSettingsSaveState>({
		phase: "idle",
	});
	return (
		<>
			<ProjectSettingsForm projectId={projectId} section={section} onSaveState={setSaveState} />
			{saveState.error && <span>{saveState.error}</span>}
			{saveState.phase === "saved" && <span>{"Saved"}</span>}
		</>
	);
}

function renderSettings(projectId = "proj-1", workspaces?: WorkspaceSummary[], section?: ProjectSettingsSection) {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	if (workspaces) {
		queryClient.setQueryData(workspaceQueryKey, workspaces);
	}
	render(
		<QueryClientProvider client={queryClient}>
			<TestProjectSettings projectId={projectId} section={section} />
		</QueryClientProvider>,
	);
	return queryClient;
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	const escaped = optionName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	await userEvent.click(await screen.findByRole("menuitem", { name: new RegExp(`^${escaped}$`, "i") }));
}

function submitSettings() {
	fireEvent.submit(document.getElementById("project-settings-form")!);
}

const agentCatalogResponse = {
	data: {
		agents: [
			agentReadiness("claude-code", "Claude Code"),
			agentReadiness("codex", "Codex"),
			agentReadiness("copilot", "GitHub Copilot"),
			agentReadiness("cursor", "Cursor"),
			agentReadiness("goose", "Goose"),
			agentReadiness("kilocode", "Kilo Code"),
			agentReadiness("kiro", "Kiro", { authentication: "unknown" }),
			agentReadiness("opencode", "OpenCode"),
			agentReadiness("pi", "Pi"),
		],
	},
	error: undefined,
};

function mockProject(project: Record<string, unknown>, agentResponse = agentCatalogResponse) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents/readiness") return agentResponse;
		if (path === "/api/v1/agents/{agent}/models") {
			return {
				data: {
					agentId: "test-agent",
					selectionMode: "text",
					models: [],
					allowCustom: true,
					source: "manual",
					fetchedAt: "2026-07-31T00:00:00Z",
					stale: false,
				},
				error: undefined,
			};
		}
		return {
			data: {
				status: "ok",
				project,
			},
			error: undefined,
		};
	});
}

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset();
	postMock.mockReset();
	navigateMock.mockReset();
	closeSettingsMock.mockReset();
	openGlobalSettingsMock.mockReset();
	ensureAgentReadinessMock.mockReset();
	trackerIntakeGate.enabled = true;
	putMock.mockResolvedValue({ data: { project: {} }, error: undefined });
	postMock.mockResolvedValue({
		data: { orchestrator: { id: "proj-1-orch-2" } },
		error: undefined,
		response: { status: 200 },
	});
});

describe("ProjectSettingsForm", () => {
	it("saves a changed project setting without a submit action", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				autoReview: false,
			},
		});

		renderSettings();
		await userEvent.click(await screen.findByRole("switch", { name: "Auto review PRs" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", expect.objectContaining({
			body: expect.objectContaining({ config: expect.objectContaining({ autoReview: true }) }),
		})));
	});

	it("ensures agent readiness in the background without manual refresh buttons", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		await waitFor(() => expect(ensureAgentReadinessMock).toHaveBeenCalledWith({ hostId: undefined }));
		expect(screen.getByRole("button", { name: "Worker approval" })).toHaveTextContent("Auto");
		expect(screen.queryByRole("button", { name: "Refresh agents" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh worker model list" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh orchestrator model list" })).not.toBeInTheDocument();
	});

	it("does not have its own close button (dialog handles closing)", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByRole("button", { name: "Edit Project name" });

		// Close button is now in SettingsDialog, not in the form itself
		expect(screen.queryByRole("button", { name: "Close settings" })).not.toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("shows the project name as text with a pencil until edit is clicked", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		expect(await screen.findByRole("button", { name: "Edit Project name" })).toHaveTextContent("Project One");
		expect(screen.queryByRole("textbox", { name: "Project name" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Edit Project name" }));
		expect(screen.getByRole("textbox", { name: "Project name" })).toHaveValue("Project One");
		expect(screen.queryByRole("button", { name: "Edit Project name" })).not.toBeInTheDocument();

		await userEvent.keyboard("{Escape}");
		expect(await screen.findByRole("button", { name: "Edit Project name" })).toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Project name" })).not.toBeInTheDocument();
	});

	it("does not navigate on Escape (dialog handles closing)", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByRole("button", { name: "Edit Project name" });

		await userEvent.keyboard("{Escape}");

		// Escape is handled by the Radix Dialog in SettingsDialog, not the form
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("atomically saves the project display name and config without changing its stable ID", async () => {
		mockProject({
			id: "tg_content_factory_5863f66be3",
			name: "tg_content_factory_5863f66be3",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("tg_content_factory_5863f66be3");

		const projectName = await beginEdit("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "TG Content Factory");
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "tg_content_factory_5863f66be3" } },
			body: expect.objectContaining({ displayName: "TG Content Factory" }),
		});
		expect(screen.getByLabelText("Project name")).toHaveValue("TG Content Factory");
	});

	it("renders git scp-style remotes as clickable https links", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const repoLink = await screen.findByRole("link", { name: "git@github.com:acme/project-one.git" });
		expect(repoLink).toHaveAttribute("href", "https://github.com/acme/project-one");
	});

	it("renders self-managed GitLab nested-group remotes with host and full namespace", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@gitlab.company.com:eng/platform/agent-ops.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const repoLink = await screen.findByRole("link", {
			name: "git@gitlab.company.com:eng/platform/agent-ops.git",
		});
		expect(repoLink).toHaveAttribute("href", "https://gitlab.company.com/eng/platform/agent-ops");
	});

	it("renders ssh remotes as clickable https links", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "ssh://git@github.com/acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const repoLink = await screen.findByRole("link", { name: "ssh://git@github.com/acme/project-one.git" });
		expect(repoLink).toHaveAttribute("href", "https://github.com/acme/project-one");
	});

	it("shows the default permission setting and preserves saved permission modes", async () => {
		mockProject({
			id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one",
			repo: "", defaultBranch: "main", config: {
				worker: { agent: "codex", agentConfig: { permissions: "default" } },
				orchestrator: { agent: "claude-code", agentConfig: { permissions: "default" } },
			},
		});
		renderSettings("proj-1", undefined, "agents");
		const worker = await screen.findByRole("button", { name: "Worker approval" });
		const orchestrator = screen.getByRole("button", { name: "Orchestrator approval" });
		// A project role has no agent of its own, so "default" defers to whichever agent the session uses.
		expect(worker).toHaveTextContent("Use agent permissions");
		expect(orchestrator).toHaveTextContent("Use agent permissions");
		await userEvent.click(worker);
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
			"Use agent permissions", "Auto", "Accept edits", "Bypass permissions",
		]);
		await userEvent.keyboard("{Escape}");
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const config = putMock.mock.calls[0][1].body.config;
		expect(config.worker.agentConfig.permissions).toBe("default");
		expect(config.orchestrator.agentConfig.permissions).toBe("default");
	});

	it("can switch back to the session agent's own permission setting", async () => {
		mockProject({
			id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one",
			repo: "", defaultBranch: "main", config: {
				worker: { agent: "claude-code", agentConfig: { permissions: "accept-edits" } },
				orchestrator: { agent: "claude-code" },
			},
		});
		renderSettings("proj-1", undefined, "agents");
		const worker = await screen.findByRole("button", { name: "Worker approval" });
		expect(worker).toHaveTextContent("Accept edits");
		await userEvent.click(worker);
		await userEvent.click(screen.getByRole("menuitem", { name: "Use agent permissions" }));
		expect(worker).toHaveTextContent("Use agent permissions");
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0][1].body.config.worker.agentConfig.permissions).toBe("default");
	});

	it("shows Auto for unset permissions without saving an override", async () => {
		mockProject({
			id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one",
			repo: "", defaultBranch: "main", config: {
				worker: { agent: "claude-code" },
				orchestrator: { agent: "claude-code" },
			},
		});
		renderSettings("proj-1", undefined, "agents");
		expect(await screen.findByRole("button", { name: "Worker approval" })).toHaveTextContent("Auto");
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0][1].body.config.worker.agentConfig?.permissions).toBeUndefined();
	});

	it("saves role permissions without dropping hidden workflow config or legacy agent keys", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				canonicalRepoURL: "https://github.com/upstream/project-one",
				defaultBranch: "develop",
				sessionPrefix: "po",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				worker: {
					agent: "codex",
					agentConfig: { model: "worker-model" },
				},
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "claude-opus-4-5",
					permissions: "auto",
				},
				reviewers: [{ harness: "claude-code" }],
			},
		});

		renderSettings("proj-1", undefined, "agents");

		expect(screen.queryByLabelText("Default branch")).not.toBeInTheDocument();
		const permissionMode = await screen.findByRole("button", { name: "Worker approval" });
		expect(screen.queryByRole("button", { name: "Worker agent" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Orchestrator agent" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Worker model" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Orchestrator model" })).not.toBeInTheDocument();
		expect(permissionMode).toHaveTextContent("Auto");
		await chooseOption(permissionMode, "Accept edits");

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "proj-1" } },
			body: {
				displayName: "Project One",
				config: expect.objectContaining({
					// Hidden workflow config is preserved
					canonicalRepoURL: "https://github.com/upstream/project-one",
					defaultBranch: "develop",
					sessionPrefix: "po",
					env: { FOO: "bar" },
					reviewers: [{ harness: "claude-code", agentConfig: { model: "claude-opus-4-5", permissions: "auto" } }],
					// The legacy agent and model keys are stripped; permissions remain
					worker: { agentConfig: { permissions: "accept-edits" } },
					orchestrator: { agentConfig: { permissions: "auto" } },
					agentConfig: undefined,
				}),
			},
		});
		expect(putMock).toHaveBeenCalledTimes(1);
		expect(await screen.findByText("Saved")).toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalledWith("/api/v1/orchestrators", expect.anything());
	}, 20_000);

	it("loads workflow fields correctly", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				sessionPrefix: "po",
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				reviewers: [{ harness: "claude-code" }],
			},
		});

		renderSettings("proj-1", undefined, "general");

		expect(await beginEdit("Default branch")).toHaveValue("develop");
		await userEvent.keyboard("{Escape}");
		expect(await beginEdit("Session prefix")).toHaveValue("po");
	});

	it("loads and saves the project auto review setting", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				autoReview: true,
			},
		});

		renderSettings("proj-1", undefined, "general");

		const toggle = await screen.findByRole("switch", { name: "Auto review PRs" });
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		expect(toggle).not.toBeChecked();

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const request = putMock.mock.calls[0]?.[1];
		expect(request?.body.config.autoReview).toBe(false);
	});

	it("loads and saves whether workers request an AO review", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "general");

		const toggle = await screen.findByRole("switch", { name: "Workers request AO review" });
		expect(toggle).not.toBeChecked();

		await userEvent.click(toggle);
		expect(toggle).toBeChecked();

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const request = putMock.mock.calls[0]?.[1];
		expect(request?.body.config.workersRequestReview).toBe(true);
		expect(request?.body.config.reviewers).toBeUndefined();
	});

	// With no reviewer configured the daemon reviews with the default worker
	// agent and its model, so the reviewer row must show that model.
	it("keeps the automatic default branch unpinned when saving other settings", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "trunk",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "general");

		expect(await beginEdit("Default branch")).toHaveValue("auto");
		await userEvent.keyboard("{Escape}");
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const request = putMock.mock.calls[0]?.[1];
		expect(request?.body.config.defaultBranch).toBeUndefined();
	});


	it("preserves existing reviewer-only config fields when saving project settings", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") return agentCatalogResponse;
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
							reviewers: [
								{ harness: "codex", agentConfig: { model: "gpt-5", permissions: "bypass-permissions" } },
							],
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1");
		await screen.findByRole("button", { name: "Edit Project name" });
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "proj-1" } },
			body: expect.objectContaining({
				config: expect.objectContaining({
					reviewers: [
						expect.objectContaining({
							harness: "codex",
							agentConfig: expect.objectContaining({
								model: "gpt-5",
								permissions: "bypass-permissions",
							}),
						}),
					],
				}),
			}),
		});
	});

	it("clears the saved reviewer model and reviewer-only config when switching the project reviewer harness", async () => {
		getMock.mockImplementation(async (path: string, init?: { params?: { path?: { agent?: string } } }) => {
			if (path === "/api/v1/agents/readiness") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") {
				const agent = init?.params?.path?.agent;
				if (agent === "codex") {
					return {
						data: {
							agentId: "codex",
							selectionMode: "catalog",
							models: [
								{ id: "gpt-5", label: "GPT-5", isDefault: true },
								{ id: "gpt-5-mini", label: "GPT-5 Mini" },
							],
							allowCustom: true,
							source: "official-catalog",
							fetchedAt: "2026-08-30T00:00:00Z",
							stale: false,
						},
						error: undefined,
					};
				}
				return {
					data: {
						agentId: agent ?? "unknown",
						selectionMode: "text",
						models: [],
						allowCustom: true,
						source: "manual",
						fetchedAt: "2026-08-30T00:00:00Z",
						stale: false,
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
							reviewers: [
								{ harness: "codex", agentConfig: { permissions: "bypass-permissions" } },
							],
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);
		const codexOption = (await screen.findAllByRole("menuitem")).find((option) => option.textContent?.includes("Codex"));
		expect(codexOption).toBeTruthy();
		await userEvent.click(codexOption!);
		await userEvent.click(await screen.findByRole("button", { name: "Reviewer model" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /GPT-5 Mini/i }));
		expect(reviewer).toHaveTextContent("Codex");
		expect(screen.getByRole("button", { name: "Reviewer model" })).toHaveTextContent("GPT-5 Mini");

		await chooseOption(reviewer, "OpenCode");
		expect(reviewer).toHaveTextContent("OpenCode");

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "proj-1" } },
			body: expect.objectContaining({
				config: expect.objectContaining({
					reviewers: [
						expect.objectContaining({
							harness: "opencode",
							agentConfig: undefined,
						}),
					],
				}),
			}),
		});
	});

	it("shows the daemon validation message when the atomic settings save fails", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});
		putMock.mockResolvedValue({
			data: undefined,
			error: { message: "invalid permissions" },
		});

		renderSettings();

		const projectName = await beginEdit("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "Updated Project");
		submitSettings();

		expect(await screen.findByText("invalid permissions")).toBeInTheDocument();
		expect(screen.queryByText("Saved")).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("rejects a blank project name before sending the settings update", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const projectName = await beginEdit("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "   ");
		submitSettings();

		expect(await screen.findByText("Project name is required.")).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("saves a project with no pinned agents without sending empty agent values", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {},
		});

		renderSettings("proj-1", undefined, "agents");

		expect(await screen.findByRole("button", { name: "Worker approval" })).toBeInTheDocument();

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const { config } = putMock.mock.calls[0][1].body;
		expect(config.worker.agent).toBeUndefined();
		expect(config.orchestrator.agent).toBeUndefined();
		expect(screen.queryByText(/agents are required/)).not.toBeInTheDocument();
	});

	it("shows the resolved reviewer agent without an inherited placeholder", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewerAgent = await screen.findByRole("button", { name: "Reviewer agent" });
		expect(reviewerAgent).toHaveTextContent("Claude Code");
		expect(reviewerAgent).not.toHaveTextContent("default");

		await userEvent.click(reviewerAgent);
		expect(await screen.findByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
	});

	it("offers ready Pi reviewers and hides Kiro until authorized", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");
		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);
		const labels = (await screen.findAllByRole("menuitem")).map((option) => option.textContent);
		expect(labels).not.toContain("Kiro");
		expect(labels).toContain("Pi");
	});

	it("offers Muse Code as a reviewer", async () => {
		const muse = agentReadiness("muse", "Muse Code");
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "muse" },
				orchestrator: { agent: "claude-code" },
			},
		});
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [...agentCatalogResponse.data.agents, muse],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "muse" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);

		expect(await screen.findByRole("menuitem", { name: /Muse Code/ })).toBeInTheDocument();
	});

	it("orders reviewers using the default agent priority", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		await userEvent.click(await screen.findByRole("button", { name: "Reviewer agent" }));
		const reviewerLabels = (await screen.findAllByRole("menuitem"))
			.map((option) => option.textContent)
			.filter((label) => label !== "Enter model ID…");

		expect(reviewerLabels).toEqual([
			"Claude Code",
			"Codex",
			"Cursor",
			"OpenCode",
			"GitHub Copilot",
			"Kilo Code",
			"Pi",
			"Manage agents…",
		]);
	});

	it("offers the experimental host-trusted reviewer set", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" } },
		};
		const devin = agentReadiness("devin", "Devin");
		const droid = agentReadiness("droid", "Droid");
		const kimi = agentReadiness("kimi", "Kimi");
		const aider = agentReadiness("aider", "Aider");
		const amp = agentReadiness("amp", "Amp");
		const experimental = [
			agentReadiness("agy", "Agy"),
			agentReadiness("auggie", "Auggie"),
			agentReadiness("autohand", "Autohand"),
			agentReadiness("cline", "Cline"),
			agentReadiness("crush", "Crush"),
			agentReadiness("grok", "Grok"),
		];
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [...agentCatalogResponse.data.agents, devin, droid, kimi, aider, amp, ...experimental],
					},
					error: undefined,
				};
			}
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);
		const options = await screen.findAllByRole("menuitem");
		const labels = options.map((option) => option.textContent);
		expect(labels).toContain("Agy");
		expect(labels).toContain("Devin");
		expect(labels).toContain("Droid");
		expect(labels).toContain("Kimi");
		expect(labels).toContain("Aider");
		expect(labels).toContain("Amp");
		expect(labels).toContain("Auggie");
		expect(labels).toContain("Autohand");
		expect(labels).toContain("Cline");
		expect(labels).toContain("Crush");
		expect(labels).toContain("Grok");
	});

	it("warns when an experimental reviewer is selected", async () => {
		const kimchi = agentReadiness("kimchi", "Kimchi");
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [...agentCatalogResponse.data.agents, kimchi],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" } },
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");
		await chooseOption(await screen.findByRole("button", { name: "Reviewer agent" }), "Kimchi");
		expect(screen.getByRole("status")).toHaveTextContent("Experimental host-trusted reviewer");
	});

	it("shows Copilot as a reviewer option and saves it in the reviewers payload", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);
		const copilot = await screen.findByRole("menuitem", { name: "GitHub Copilot" });
		expect(copilot).not.toHaveAttribute("aria-disabled", "true");
		await userEvent.click(copilot);
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith(
			"/api/v1/projects/{id}",
			expect.objectContaining({
				body: expect.objectContaining({
					config: expect.objectContaining({ reviewers: [{ harness: "copilot" }] }),
				}),
			}),
		);
	});

	it("saves a concrete model selected from the inherited reviewer", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") {
				return {
					data: {
						agentId: "claude-code",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5", label: "GPT-5", isDefault: true },
							{ id: "gpt-5-mini", label: "GPT-5 Mini" },
						],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" } },
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");
		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		const reviewerModel = await screen.findByRole("button", { name: "Reviewer model" });
		await userEvent.click(reviewerModel);
		await userEvent.click(await screen.findByRole("menuitem", { name: "GPT-5 Mini" }));
		expect(reviewer).toHaveTextContent("Claude Code");
		expect(reviewerModel).toHaveTextContent("GPT-5 Mini");

		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith(
			"/api/v1/projects/{id}",
			expect.objectContaining({
				body: expect.objectContaining({
					config: expect.objectContaining({
						reviewers: [{ harness: "claude-code", agentConfig: { model: "gpt-5-mini" } }],
					}),
				}),
			}),
		);
	});

	it("persists reviewer approval selected from the inherited reviewer after reload", async () => {
		let savedConfig: Record<string, unknown> = {
			worker: { agent: "codex" },
			orchestrator: { agent: "claude-code" },
		};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") return { data: { agentId: "claude-code", selectionMode: "text", models: [] } };
			return { data: { status: "ok", project: {
				id: "proj-1", name: "Project One", kind: "single_repo", path: "/repo/project-one",
				repo: "", config: savedConfig,
			} } };
		});
		putMock.mockImplementation(async (_path: string, request: { body: { config: Record<string, unknown> } }) => {
			savedConfig = request.body.config;
			return { data: { project: {} }, error: undefined };
		});

		const first = render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TestProjectSettings projectId="proj-1" section="agents" />
			</QueryClientProvider>,
		);
		const approval = await screen.findByRole("button", { name: "Reviewer approval" });
		expect(approval).toHaveTextContent("Auto");
		await chooseOption(approval, "Accept edits");
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(savedConfig.reviewers).toEqual([{ harness: "claude-code", agentConfig: { permissions: "accept-edits" } }]);

		first.unmount();
		renderSettings("proj-1", undefined, "agents");
		expect(await screen.findByRole("button", { name: "Reviewer approval" })).toHaveTextContent("Accept edits");
	});

	it("hides the Copilot reviewer when its binary is missing", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [agentReadiness("copilot", "GitHub Copilot", { installation: "not_installed", authentication: "unknown" })],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		await userEvent.click(await screen.findByRole("button", { name: "Reviewer agent" }));
		const copilot = (await screen.findAllByRole("menuitem")).find((option) =>
			option.textContent?.includes("GitHub Copilot"),
		);
		expect(copilot).toBeUndefined();
		expect(screen.getByRole("menuitem", { name: "Manage agents…" })).toBeInTheDocument();
	});

	it("hides an installed Copilot reviewer until authorization is known", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [agentReadiness("copilot", "GitHub Copilot", { authentication: "unknown" })],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		await userEvent.click(await screen.findByRole("button", { name: "Reviewer agent" }));
		const copilot = (await screen.findAllByRole("menuitem")).find((option) =>
			option.textContent?.includes("GitHub Copilot"),
		);
		expect(copilot).toBeUndefined();
		expect(screen.getByRole("menuitem", { name: "Manage agents…" })).toBeInTheDocument();
	});

	it("offers Kilo Code as a configured reviewer", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewer = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewer);
		expect(await screen.findByRole("menuitem", { name: "Kilo Code" })).toBeEnabled();
	});

	it("offers the experimental Agy reviewer", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: { worker: { agent: "agy" }, orchestrator: { agent: "claude-code" } },
		};
		const agy = agentReadiness("agy", "Agy");
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents/readiness") {
				return {
					data: {
						agents: [...agentCatalogResponse.data.agents, agy],
					},
					error: undefined,
				};
			}
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "agents");

		const reviewerAgent = await screen.findByRole("button", { name: "Reviewer agent" });
		await userEvent.click(reviewerAgent);
		const options = await screen.findAllByRole("menuitem");
		expect(options.map((option) => option.textContent)).toContain("Agy");
	});

	it("shows scratch identity and saves only scratch-supported settings", async () => {
		mockProject({
			id: "scratch",
			name: "Scratch",
			kind: "scratch",
			path: "/home/me/.ao/scratch/default",
			repo: "",
			defaultBranch: "",
			config: {
				defaultBranch: "main",
				sessionPrefix: "ao",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				agentRules: "keep work small",
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "gpt-5-codex",
					permissions: "auto",
				},
				reviewers: [{ harness: "codex" }],
				autoReview: { enabled: true },
				trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
			},
		});

		renderSettings("scratch");

		expect(await screen.findByText("Project details")).toBeInTheDocument();
		expect(screen.queryByLabelText("Default branch")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Session prefix")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Auto-review pull requests")).not.toBeInTheDocument();
		expect(screen.queryByText("Reviewers")).not.toBeInTheDocument();
		expect(screen.queryByText("Tracker intake")).not.toBeInTheDocument();

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "scratch" } },
			body: {
				displayName: "Scratch",
				config: {
					env: { FOO: "bar" },
					sessionPrefix: "ao",
					symlinks: [".env"],
					postCreate: ["npm install"],
					agentRules: "keep work small",
					worker: { agentConfig: { permissions: "auto" } },
					orchestrator: { agentConfig: { permissions: "auto" } },
					agentConfig: undefined,
				},
			},
		});
		expect(postMock).not.toHaveBeenCalled();
	});

	it("saves GitHub tracker intake settings, deriving the repo from the project's git origin", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "general");

		await userEvent.click(await screen.findByLabelText("Enable issue intake"));

		// Repository is display-only, derived from the project's own git origin — no input to
		// fill. Assignee is the only eligibility rule in v1.
		expect(screen.getByRole("link", { name: "acme/project-one" })).toHaveAttribute(
			"href",
			"https://github.com/acme/project-one",
		);
		await userEvent.click(await screen.findByRole("button", { name: "Add assignee" }));
		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.trackerIntake).toEqual({
			enabled: true,
			assignee: "octocat",
		});
	});

	it("hides the intake section entirely when the daemon gate is off", async () => {
		trackerIntakeGate.enabled = false;
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" } },
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "general");

		expect(await screen.findByText("Pull requests")).toBeInTheDocument();
		expect(screen.queryByText("Issues")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Enable issue intake")).not.toBeInTheDocument();
	});

	it("still saves other settings when the gate is off and stored intake lacks an assignee", async () => {
		trackerIntakeGate.enabled = false;
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
						trackerIntake: { enabled: true },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "general");
		expect(await screen.findByText("Pull requests")).toBeInTheDocument();

		const prefix = await beginEdit("Session prefix");
		await userEvent.clear(prefix);
		await userEvent.type(prefix, "gated");
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0]?.[1]?.body.config.trackerIntake).toEqual({ enabled: true });
	});

	it("preserves explicit provider and unmodeled tracker intake fields on save", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@gitlab.example.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
						trackerIntake: {
							enabled: true,
							provider: "gitlab",
							repo: "acme/project-one",
							assignee: "octocat",
							labels: ["agent-ready"],
						},
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "general");
		await screen.findByRole("button", { name: "Edit Project name" });
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.trackerIntake).toEqual({
			enabled: true,
			provider: "gitlab",
			repo: "acme/project-one",
			assignee: "octocat",
			labels: ["agent-ready"],
		});
	});

	it("does not save or show a save error when intake is enabled with no assignee yet", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "general");

		await userEvent.click(await screen.findByLabelText("Enable issue intake"));
		submitSettings();

		expect(screen.queryByText("Enabling intake requires an assignee.")).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

});
