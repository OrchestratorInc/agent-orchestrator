import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpProject, CloudCpProjectSettingsRequest } from "../lib/cloud-cp";
import { ProjectSettingsForm } from "./ProjectSettingsForm";
import { SettingsDialog } from "./SettingsDialog";
import { useUiStore } from "../stores/ui-store";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), localGet: vi.fn(), connections: vi.fn(), ready: true }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: { getProject: mocks.get, updateProjectSettings: mocks.patch, listUserProviderConnections: mocks.connections }, ready: mocks.ready, baseUrl: "https://cloud.test" }) }));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"], cloudProjectsQueryKey: ["cloud-projects"], useWorkspaceQuery: () => ({ data: [] }) }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: mocks.localGet }, apiErrorMessage: (error: { message: string }) => error.message }));

let project: CloudCpProject;

function mount(section: "general" | "agents" = "agents", onSaveState = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><TooltipProvider><ProjectSettingsForm projectId="project" cloudOrgId="org" section={section} onSaveState={onSaveState} /></TooltipProvider></QueryClientProvider>);
}

async function choose(label: string, choice: string) {
	await userEvent.click(screen.getByRole("button", { name: label }));
	await userEvent.click(await screen.findByRole("menuitem", { name: choice }));
}

beforeEach(() => {
	mocks.get.mockReset();
	mocks.patch.mockReset();
	mocks.localGet.mockReset();
	mocks.connections.mockReset();
	mocks.connections.mockResolvedValue({ providerConnections: [] });
	mocks.localGet.mockImplementation(async (_path: string, options: { params: { path: { agent: string } } }) => ({ data: {
		agent: options.params.path.agent, selectionMode: "catalog", allowCustom: true,
		models: ["worker-model", "orchestrator-model", "reviewer-model", "review-codex"].map((id) => ({ id, label: id, efforts: ["low", "high", "max"] })),
	} }));
	mocks.ready = true;
	useUiStore.setState({ settingsModal: null });
	project = {
		id: "project", orgId: "org", displayName: "Cloud project", repositoryUrl: "https://github.com/owner/repo", defaultBranch: "main", createdAt: "now", updatedAt: "now",
		config: {
			worker: { agent: "codex", agentConfig: { model: "worker-model", effort: "max" } },
			orchestrator: { agent: "claude-code", agentConfig: { model: "orchestrator-model" } },
			reviewers: [{ harness: "claude-code", agentConfig: { model: "reviewer-model", effort: "high", permissions: "auto" } }],
			coder: { templateId: "preserve" },
		},
	};
	mocks.get.mockImplementation(async () => ({ project }));
	mocks.patch.mockImplementation(async (_org: string, _id: string, patch: CloudCpProjectSettingsRequest) => {
		const mergeRole = (role: "worker" | "orchestrator") => {
			const previous = project.config[role];
			const update = patch.config?.[role];
			if (update === undefined) return previous;
			if (update === null) return undefined;
			const agent = update.agent ?? previous?.agent;
			if (!agent) throw new Error("Role agent is required");
			return { agent, agentConfig: { ...previous?.agentConfig, ...update.agentConfig } };
		};
		project = { ...project, ...(patch.displayName ? { displayName: patch.displayName } : {}), ...(patch.defaultBranch ? { defaultBranch: patch.defaultBranch } : {}), config: { ...project.config, ...patch.config, worker: mergeRole("worker"), orchestrator: mergeRole("orchestrator") } };
		for (const role of ["worker", "orchestrator"] as const) if (project.config[role] === undefined) delete project.config[role];
		return { project };
	});
});

describe("Cloud project settings", () => {
	it.each([
		["worker", "Worker", "codex"], ["worker", "Worker", "claude-code"],
		["orchestrator", "Orchestrator", "codex"], ["orchestrator", "Orchestrator", "claude-code"],
		["reviewer", "Reviewer", "codex"], ["reviewer", "Reviewer", "claude-code"],
	] as const)("locks agent/model but persists %s effort for %s with %s", async (role, label, agent) => {
		const agentConfig = { model: "private-model", effort: "high" as const, permissions: "auto" as const };
		if (role === "reviewer") project.config.reviewers = [{ harness: agent, agentConfig }];
		else project.config[role] = { agent, agentConfig };
		const view = mount();
		expect(await screen.findByRole("button", { name: `${label} agent` })).toBeDisabled();
		expect(screen.getByRole("button", { name: `${label} model` })).toBeDisabled();
		expect(screen.getByRole("button", { name: `${label} model` })).toHaveTextContent("private-model");
		expect(screen.getByRole("button", { name: `${label} approval` })).toBeDisabled();
		await choose(`${label} effort`, "Low");
		const expected = { model: "private-model", mode: "", effort: "low", permissions: "auto" };
		await waitFor(() => expect(mocks.patch).toHaveBeenLastCalledWith("org", "project", {
			config: role === "reviewer" ? { reviewers: [{ harness: agent, agentConfig: expected }] } : { [role]: { agent, agentConfig: expected } },
		}));
		view.unmount();
		mount();
		expect(await screen.findByRole("button", { name: `${label} effort` })).toHaveTextContent("Low");
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("keeps unavailable effort disabled without changing the session's agent", async () => {
		project.config = { worker: { agent: "opencode" }, orchestrator: { agent: "cursor" } };
		mount();
		for (const role of ["Worker", "Orchestrator", "Reviewer"]) {
			expect(await screen.findByRole("button", { name: `${role} agent` })).toBeDisabled();
			expect(screen.getByRole("button", { name: `${role} effort` })).toBeDisabled();
		}
		expect(mocks.patch).not.toHaveBeenCalled();
	});

	it("shows repository and edits session prefix without replacing other settings", async () => {
		project.config.sessionPrefix = "team";
		const view = mount("general");
		expect(await screen.findByRole("link", { name: "https://github.com/owner/repo" })).toHaveAttribute("href", project.repositoryUrl);
		await userEvent.click(screen.getByRole("button", { name: "Edit Session prefix" }));
		const prefix = screen.getByRole("textbox", { name: "Session prefix" });
		await userEvent.clear(prefix);
		await userEvent.type(prefix, "review");
		await waitFor(() => expect(mocks.patch).toHaveBeenLastCalledWith("org", "project", { config: { sessionPrefix: "review" } }));
		expect(project.config.coder).toEqual({ templateId: "preserve" });
		view.unmount();
		mount("general");
		expect(await screen.findByText("review")).toBeInTheDocument();
	});

	it("normalizes legacy roles and saves only changed identity fields", async () => {
		project.config = { workerAgent: "codex", orchestratorAgent: "claude-code", sessionPrefix: "legacy", trackerIntake: { enabled: true } };
		const view = mount();
		expect(await screen.findByRole("button", { name: "Worker agent" })).toHaveTextContent("Codex");
		expect(screen.getByRole("button", { name: "Orchestrator agent" })).toHaveTextContent("Claude Code");
		view.unmount();
		mount("general");
		await userEvent.click(await screen.findByRole("button", { name: "Edit Project name" }));
		const name = screen.getByRole("textbox", { name: "Project name" });
		await userEvent.clear(name);
		await userEvent.type(name, "New name");
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith("org", "project", { displayName: "New name" }));
		expect(screen.queryByText("Issue Intake")).not.toBeInTheDocument();
		expect(screen.getByText("legacy")).toBeInTheDocument();
	});

	it("keeps auto review independently editable", async () => {
		mount("general");
		const autoReview = await screen.findByRole("switch", { name: "Auto review PRs" });
		expect(autoReview).toBeChecked();
		await userEvent.click(autoReview);
		await waitFor(() => expect(mocks.patch).toHaveBeenLastCalledWith("org", "project", { config: { autoReview: false } }));
	});

	it("shows Cloud lookup errors without looking up a local project", async () => {
		mocks.get.mockRejectedValue(new Error("Cloud unavailable"));
		mount();
		expect(await screen.findByRole("alert")).toHaveTextContent("Cloud unavailable");
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("retries a failed Cloud load from the dialog without submitting settings or using the daemon", async () => {
		mocks.get.mockRejectedValueOnce(new Error("Cloud unavailable")).mockRejectedValueOnce(new Error("Still unavailable"));
		useUiStore.getState().openProjectSettings("project", { cloudOrgId: "org" });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><SettingsDialog /></TooltipProvider></QueryClientProvider>);
		await screen.findByRole("button", { name: "Retry" });
		expect(screen.queryByRole("button", { name: "Edit Project name" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(screen.getAllByRole("alert")[0]).toHaveTextContent("Still unavailable"));
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("button", { name: "Edit Project name" })).toBeInTheDocument();
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
		expect(mocks.get).toHaveBeenCalledTimes(3);
		expect(mocks.patch).not.toHaveBeenCalled();
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("keeps local project lookup on the local daemon", async () => {
		mocks.localGet.mockResolvedValue({ error: { message: "Local lookup" } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><ProjectSettingsForm projectId="local-project" /></QueryClientProvider>);
		await screen.findByText("Local lookup");
		expect(mocks.localGet).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "local-project" } } });
		expect(mocks.get).not.toHaveBeenCalled();
	});

	it("waits for a pending Cloud save before closing the dialog and keeps save errors visible", async () => {
		let reject!: (error: Error) => void;
		mocks.patch.mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
		useUiStore.getState().openProjectSettings("project", { cloudOrgId: "org" });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><SettingsDialog /></TooltipProvider></QueryClientProvider>);
		await userEvent.click(await screen.findByRole("button", { name: "Edit Project name" }));
		const name = screen.getByRole("textbox", { name: "Project name" });
		await userEvent.clear(name);
		await userEvent.type(name, "Pending name");
		await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "project", cloudOrgId: "org" });
		expect(screen.getByRole("button", { name: "Edit Project name" })).toBeDisabled();
		await act(async () => reject(new Error("Could not write Cloud settings")));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not write Cloud settings");
		expect(useUiStore.getState().settingsModal).not.toBeNull();
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 750)); });
		expect(mocks.patch).toHaveBeenCalledTimes(1);
		mocks.patch.mockResolvedValue({ project: { ...project, displayName: "Pending name" } });
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
		await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("keeps local cue settings out of Cloud project settings, including deep links", async () => {
		useUiStore.getState().openProjectSettings("project", { cloudOrgId: "org", section: "cues" });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><SettingsDialog /></TooltipProvider></QueryClientProvider>);
		expect(await screen.findByRole("button", { name: "Edit Project name" })).toBeInTheDocument();
		expect(screen.getByText("Cloud project")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cues" })).not.toBeInTheDocument();
		expect(mocks.get).toHaveBeenCalledWith("org", "project", { signal: expect.any(AbortSignal) });
		expect(mocks.localGet).not.toHaveBeenCalled();
	});
});
