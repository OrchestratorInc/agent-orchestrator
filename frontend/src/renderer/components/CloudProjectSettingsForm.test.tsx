import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpProject, CloudCpProjectSettingsRequest } from "../lib/cloud-cp";
import { ProjectSettingsForm } from "./ProjectSettingsForm";
import { SettingsDialog } from "./SettingsDialog";
import { useUiStore } from "../stores/ui-store";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), localGet: vi.fn(), ready: true }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: { getProject: mocks.get, updateProjectSettings: mocks.patch }, ready: mocks.ready, baseUrl: "https://cloud.test" }) }));
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
			if (!update) return previous;
			const agent = update.agent ?? previous?.agent;
			if (!agent) throw new Error("Role agent is required");
			return { agent, agentConfig: { ...previous?.agentConfig, ...update.agentConfig } };
		};
		project = { ...project, ...(patch.displayName ? { displayName: patch.displayName } : {}), ...(patch.defaultBranch ? { defaultBranch: patch.defaultBranch } : {}), config: { ...project.config, ...patch.config, worker: mergeRole("worker"), orchestrator: mergeRole("orchestrator") } };
		return { project };
	});
});

describe("Cloud project settings", () => {
	it("roundtrips independent reviewer controls using canonical config", async () => {
		const view = mount();
		expect(await screen.findByRole("textbox", { name: "Reviewer model" })).toHaveValue("reviewer-model");
		expect(screen.getByRole("switch", { name: "Auto review PRs" })).toBeChecked();
		await choose("Reviewer agent", "Codex");
		expect(screen.getByRole("textbox", { name: "Reviewer model" })).toHaveValue("");
		await userEvent.type(screen.getByRole("textbox", { name: "Reviewer model" }), "review-codex");
		await choose("Reviewer effort", "max");
		await choose("Reviewer permissions", "Bypass permissions");
		await userEvent.click(screen.getByRole("switch", { name: "Auto review PRs" }));
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith("org", "project", {
			config: { autoReview: false, reviewers: [{ harness: "codex", agentConfig: { model: "review-codex", mode: "", effort: "max", permissions: "bypass-permissions" } }] },
		}));
		await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeDisabled());
		view.unmount();
		mount();
		expect(await screen.findByRole("textbox", { name: "Reviewer model" })).toHaveValue("review-codex");
		expect(screen.getByRole("button", { name: "Reviewer effort" })).toHaveTextContent("max");
		expect(screen.getByRole("switch", { name: "Auto review PRs" })).not.toBeChecked();
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("normalizes legacy roles and saves only changed identity fields", async () => {
		project.config = { workerAgent: "codex", orchestratorAgent: "claude-code", sessionPrefix: "legacy", trackerIntake: { enabled: true } };
		const view = mount();
		expect(await screen.findByRole("button", { name: "Worker agent" })).toHaveTextContent("Codex");
		expect(screen.getByRole("button", { name: "Orchestrator agent" })).toHaveTextContent("Claude Code");
		view.unmount();
		mount("general");
		const name = await screen.findByRole("textbox", { name: "Project name" });
		await userEvent.clear(name);
		await userEvent.type(name, "New name");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith("org", "project", { displayName: "New name" }));
		expect(screen.queryByText("Issue Intake")).not.toBeInTheDocument();
		expect(screen.queryByText("Session prefix")).not.toBeInTheDocument();
	});

	it("shows Cloud lookup errors without looking up a local project", async () => {
		mocks.get.mockRejectedValue(new Error("Cloud unavailable"));
		mount();
		expect(await screen.findByRole("alert")).toHaveTextContent("Cloud unavailable");
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
		const name = await screen.findByRole("textbox", { name: "Project name" });
		await userEvent.clear(name);
		await userEvent.type(name, "Pending name");
		await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
		await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(1));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "project", projectId: "project", cloudOrgId: "org" });
		expect(screen.getByRole("textbox", { name: "Project name" })).toBeDisabled();
		await act(async () => reject(new Error("Could not write Cloud settings")));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not write Cloud settings");
		expect(useUiStore.getState().settingsModal).not.toBeNull();
		expect(mocks.localGet).not.toHaveBeenCalled();
	});

	it("keeps local cue settings out of Cloud project settings, including deep links", async () => {
		useUiStore.getState().openProjectSettings("project", { cloudOrgId: "org", section: "cues" });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><TooltipProvider><SettingsDialog /></TooltipProvider></QueryClientProvider>);
		expect(await screen.findByRole("textbox", { name: "Project name" })).toHaveValue("Cloud project");
		expect(screen.queryByRole("button", { name: "Cues" })).not.toBeInTheDocument();
		expect(mocks.get).toHaveBeenCalledWith("org", "project", { signal: expect.any(AbortSignal) });
		expect(mocks.localGet).not.toHaveBeenCalled();
	});
});
