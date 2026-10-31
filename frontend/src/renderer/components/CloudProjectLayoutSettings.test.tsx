import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { CloudProjectInstructionsSettings, CloudProjectRepositoriesSettings } from "./CloudProjectLayoutSettings";

const mocks = vi.hoisted(() => ({
	get: vi.fn(),
	patch: vi.fn(),
	listRepos: vi.fn(),
	orgCoderConfig: null as Record<string, unknown> | null,
	org: { id: "org-1", role: "admin" } as { id: string; role?: string },
	workerWorkingFolder: undefined as boolean | undefined,
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getProject: mocks.get, updateProjectSettings: mocks.patch, listGitHubRepositories: mocks.listRepos },
		ready: true,
		baseUrl: "https://cloud.test",
	}),
}));
vi.mock("../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ defaultProvider: "coder", hasCoder: true, coderWorkerWorkingFolder: mocks.workerWorkingFolder }),
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org }) }));
vi.mock("../hooks/useOrgCoderConfig", () => ({ useOrgCoderConfig: () => ({ data: mocks.orgCoderConfig, isLoading: false }) }));

const project = (config: Record<string, unknown>, repositoryUrl = "https://github.com/acme/alice") => ({
	id: "p", orgId: "org-1", displayName: "alice", repositoryUrl, defaultBranch: "main",
	config, createdAt: "2026-10-16T00:00:00Z", updatedAt: "2026-10-16T00:00:00Z",
});
const repository = (id: string, name: string) => ({
	githubRepositoryId: id, name, fullName: `acme/${name}`, htmlUrl: `https://github.com/acme/${name}`,
	defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "",
});

beforeEach(() => {
	mocks.orgCoderConfig = null;
	mocks.workerWorkingFolder = undefined;
	mocks.org = { id: "org-1", role: "admin" };
	mocks.get.mockReset().mockResolvedValue({ project: project({ repos: [{ url: "https://github.com/acme/11x", branch: "develop" }] }) });
	mocks.patch.mockReset().mockImplementation((_org: string, _id: string, body: { config: Record<string, unknown> }) =>
		Promise.resolve({ project: project({ ...body.config }) }));
	mocks.listRepos.mockReset().mockResolvedValue({
		items: [repository("1", "alice"), repository("2", "11x"), repository("3", "voice-calling-ai")],
		page: { hasMore: false },
	});
});

function renderWith(node: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

it("adds an additional repository and keeps a stored branch for one that stays", async () => {
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText("acme/alice")).toBeInTheDocument();
	expect(screen.getByText("../11x")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Add a repository" }));
	// Neither the main repository nor one already listed is offered.
	expect(screen.queryByRole("option", { name: /acme\/alice/ })).toBeNull();
	expect(screen.queryByRole("option", { name: /acme\/11x/ })).toBeNull();
	await userEvent.click(await screen.findByRole("option", { name: /acme\/voice-calling-ai/ }));
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({
		config: { repos: [{ url: "https://github.com/acme/11x", branch: "develop" }, { url: "https://github.com/acme/voice-calling-ai" }] },
	});
});

it("opens the picker for a newly added repository and drops the row when it is dismissed", async () => {
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText("acme/alice")).toBeInTheDocument();
	const add = screen.getByRole("button", { name: "Add a repository" });
	await userEvent.click(add);
	// One click is enough: the new row's list is already open.
	expect(await screen.findByRole("listbox", { name: "Additional repository 2" })).toBeInTheDocument();
	expect(screen.getByRole("combobox", { name: "Additional repository 2" })).toHaveAttribute("aria-expanded", "true");

	await userEvent.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("combobox", { name: "Additional repository 2" })).toBeNull());
	expect(screen.queryByRole("listbox")).toBeNull();
	await waitFor(() => expect(add).toHaveFocus());
	expect(add).toBeEnabled();
	// The stored repository is untouched and nothing is left to save.
	expect(screen.getByText("../11x")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
});

it("saves where the agent starts on its own", async () => {
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.click(await screen.findByRole("button", { name: "Agent starts in" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Folder with all repositories" }));
	expect(screen.getByText(/Falls back to the main repository/)).toBeInTheDocument();
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: { agentStartsIn: "parent" } });
});

it("offers the working folder only for a bring-your-own Coder org and validates it", async () => {
	const first = renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await screen.findByText("acme/alice");
	expect(screen.queryByLabelText("Working folder")).toBeNull();
	first.unmount();

	mocks.orgCoderConfig = { baseUrl: "https://coder.acme.test" };
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	const field = await screen.findByLabelText("Working folder");
	for (const rejected of ["work/{repo}", "~", "$HOME", "/"]) {
		fireEvent.change(field, { target: { value: rejected } });
		expect(screen.getByText(/must be an absolute path or start with/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
	}
	fireEvent.change(field, { target: { value: "~/work/{name}" } });
	expect(screen.getByText(/may only use \{repo\} as a placeholder/)).toBeInTheDocument();
	fireEvent.change(field, { target: { value: "~/work/{repo}" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: { workingDirectory: "~/work/{repo}" } });
});

it("will not remove a repository a Setup step still runs in", async () => {
	mocks.get.mockResolvedValue({ project: project({
		repos: [{ url: "https://github.com/acme/11x" }],
		postCreate: ["(\ncd -- '../11x' || exit 1\nmake dev-up\n)"],
	}) });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.click(await screen.findByRole("button", { name: "Remove acme/11x" }));
	expect(screen.getByText(/acme\/11x is still used by a Setup or Cleanup step/)).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
});

it("reports an invalid draft as unsaveable so the dialog can close", async () => {
	mocks.get.mockResolvedValue({ project: project({
		repos: [{ url: "https://github.com/acme/11x" }],
		postCreate: ["(\ncd -- '../11x' || exit 1\nmake dev-up\n)"],
	}) });
	const onSaveState = vi.fn();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" onSaveState={onSaveState} />);
	await userEvent.click(await screen.findByRole("button", { name: "Remove acme/11x" }));
	expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: true, unsaveable: true }));
});

it("refuses {repo} in a standalone project's working folder", async () => {
	mocks.orgCoderConfig = { connected: true };
	mocks.get.mockResolvedValue({ project: project({ source: "standalone", sandboxProvider: "coder" }, "https://scratch.ao.local/p") });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.type(await screen.findByLabelText("Working folder"), "~/work/{{repo}");
	expect(screen.getByText(/cannot use \{repo\}/)).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
});

it("surfaces the control plane's reason when a save is refused", async () => {
	mocks.patch.mockReset().mockRejectedValue(new Error("acme/voice-calling-ai is not connected to this organization's GitHub App"));
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.click(await screen.findByRole("button", { name: "Agent starts in" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Folder with all repositories" }));
	fireEvent.submit(document.getElementById("project-settings-form")!);
	expect(await screen.findByText(/is not connected to this organization's GitHub App/)).toBeInTheDocument();
});

it("shows a standalone project as having no repository", async () => {
	mocks.get.mockResolvedValue({ project: project({ source: "standalone" }, "https://scratch.ao.local/p") });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText(/No repository. Sessions start in an empty folder/)).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Add a repository" })).toBeNull();
	expect(screen.queryByRole("button", { name: "Agent starts in" })).toBeNull();
	expect(mocks.listRepos).not.toHaveBeenCalled();
});

it("is read-only for a member", async () => {
	mocks.org = { id: "org-1", role: "member" };
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText(/Only organization owners and admins/)).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Add a repository" })).toBeDisabled();
});

it("edits the worker and orchestrator instructions", async () => {
	mocks.get.mockResolvedValue({ project: project({ agentRules: "Start in alice." }) });
	renderWith(<CloudProjectInstructionsSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByLabelText("Worker instructions")).toHaveValue("Start in alice.");
	fireEvent.change(screen.getByLabelText("Orchestrator instructions"), { target: { value: "Split work by repository." } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: { orchestratorRules: "Split work by repository." } });
});

it("blocks instructions over 16 KB", async () => {
	mocks.get.mockResolvedValue({ project: project({}) });
	renderWith(<CloudProjectInstructionsSettings projectId="p" cloudOrgId="org-1" />);
	fireEvent.change(await screen.findByLabelText("Worker instructions"), { target: { value: "x".repeat(16 * 1024 + 1) } });
	expect(screen.getByText("Instructions must be at most 16 KB.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
});

it("describes the working folder as the workspace user's until the control plane checks ao-worker sessions out there", async () => {
	mocks.orgCoderConfig = { baseUrl: "https://coder.test", workerUser: "ao-worker" };
	mocks.get.mockResolvedValue({ project: project({ workingDirectory: "~/work/{repo}" }) });
	const view = renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText(/Leave empty to use your Coder connection's folder\./)).toBeInTheDocument();
	view.unmount();
	mocks.workerWorkingFolder = true;
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	expect(await screen.findByText(/Leave empty for the default location\./)).toBeInTheDocument();
	expect(screen.queryByText(/Leave empty to use your Coder connection's folder/)).not.toBeInTheDocument();
});

it("shows the working folder in the app's language", async () => {
	mocks.orgCoderConfig = { baseUrl: "https://coder.acme.test" };
	await appI18n.changeLanguage("de");
	try {
		renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
		const field = await screen.findByLabelText("Arbeitsordner");
		expect(screen.getByText(/Wo das Haupt-Repository in deinem Coder-Workspace ausgecheckt wird/)).toBeInTheDocument();
		fireEvent.change(field, { target: { value: "~/work/{name}" } });
		expect(screen.getByRole("alert")).toHaveTextContent("Der Arbeitsordner darf nur {repo} als Platzhalter verwenden.");
	} finally {
		await act(() => appI18n.changeLanguage("en"));
	}
});
