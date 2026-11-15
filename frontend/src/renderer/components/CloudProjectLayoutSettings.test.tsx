import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, onTestFinished, vi } from "vitest";
import { appI18n } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { CloudProjectInstructionsSettings, CloudProjectRepositoriesSettings } from "./CloudProjectLayoutSettings";

const mocks = vi.hoisted(() => ({
	get: vi.fn(),
	patch: vi.fn(),
	listRepos: vi.fn(),
	createRepository: vi.fn(),
	orgCoderConfig: null as Record<string, unknown> | null,
	org: { id: "org-1", role: "admin" } as { id: string; role?: string },
	workerWorkingFolder: undefined as boolean | undefined,
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getProject: mocks.get, updateProjectSettings: mocks.patch, listGitHubRepositories: mocks.listRepos, createGitHubRepository: mocks.createRepository },
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

// The control plane answers a settings write with the whole project, layout included.
function workspacePatches() {
	mocks.patch.mockImplementation((_org: string, _id: string, body: { config: Record<string, unknown> }) =>
		Promise.resolve({ project: project({ layout: "workspace", ...body.config }) }));
}

it("lists a workspace's repositories as its own, without an agent start choice, and saves an uploaded folder at once", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace", repos: [{ url: "https://github.com/acme/11x", branch: "develop" }] }) });
	mocks.createRepository.mockResolvedValue({
		repository: repository("9", "notes"),
		created: true,
		pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git",
	});
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] });
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockResolvedValue({ status: "pushed", branch: "main", commit: "abc", source: "import", leftOut: [] });
	const onSaveState = vi.fn();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" onSaveState={onSaveState} />);

	// The pane's title already says it, so the field label is for screen readers only.
	expect(await screen.findByText("Workspace repositories")).toHaveClass("sr-only");
	expect(screen.queryByText("Agent starts in")).toBeNull();
	await userEvent.click(screen.getByRole("button", { name: "Add a local folder" }));
	await userEvent.click(await screen.findByRole("button", { name: "Upload" }));

	await waitFor(() => expect(push).toHaveBeenCalledWith({ folder: "/Users/me/notes", pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git", branch: "main" }));
	// The new repository goes in the workspace's own GitHub account.
	expect(mocks.createRepository).toHaveBeenCalledWith("org-1", { name: "notes", owner: "acme", description: "Uploaded from Orchestrator" });
	// Saved without Save, so leaving Settings never strands the new repository;
	// a stored branch is kept.
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({
		config: { repos: [{ url: "https://github.com/acme/11x", branch: "develop" }, { url: "https://github.com/acme/notes" }] },
	});
	expect(await screen.findByText("../notes")).toBeInTheDocument();
	// Nothing is left to save.
	await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: false, phase: "saved" })));
	choose.mockRestore();
});

it("says which secret-looking files a picked folder's upload leaves out, and refuses a folder it cannot upload", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] });
	const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload")
		.mockResolvedValueOnce({ status: "ok", source: "import", leftOut: [".env", "config/.npmrc"] })
		.mockResolvedValueOnce({ status: "error", code: "secret_files", message: "history holds .env", files: [".env"] });
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockClear();
	mocks.createRepository.mockClear();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	expect(await screen.findByText("Leaves out files that look like secrets: .env, config/.npmrc")).toBeInTheDocument();
	expect(preview).toHaveBeenCalledWith({ folder: "/Users/me/notes" });
	await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	expect(await screen.findByText(/The history of notes holds files that look like secrets \(\.env\)/)).toBeInTheDocument();
	expect(screen.queryByText("history holds .env")).toBeNull();
	expect(screen.queryByLabelText("Repository name for /Users/me/notes")).toBeNull();
	expect(push).not.toHaveBeenCalled();
	expect(mocks.createRepository).not.toHaveBeenCalled();
	choose.mockRestore();
	preview.mockRestore();
});

it("names a folder's repository before uploading it, and retries without making it again", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	mocks.createRepository.mockReset().mockResolvedValue({
		repository: repository("9", "notes-app"),
		created: true,
		pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes-app.git",
	});
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] });
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockClear()
		.mockResolvedValueOnce({ status: "error", code: "push_failed", message: "Could not upload notes: remote hung up" })
		.mockResolvedValueOnce({ status: "pushed", branch: "main", commit: "abc", source: "import", leftOut: [] });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	const name = await screen.findByLabelText("Repository name for /Users/me/notes");
	await userEvent.clear(name);
	await userEvent.type(name, "bad name");
	expect(screen.getByRole("button", { name: "Upload" })).toBeDisabled();
	await userEvent.clear(name);
	await userEvent.type(name, "notes-app");
	await userEvent.click(screen.getByRole("button", { name: "Upload" }));

	expect(await screen.findByText("Could not upload notes.")).toBeInTheDocument();
	expect(screen.getByText("Could not upload notes: remote hung up")).toBeInTheDocument();
	expect(mocks.createRepository).toHaveBeenCalledWith("org-1", { name: "notes-app", owner: "acme", description: "Uploaded from Orchestrator" });
	await userEvent.click(screen.getByRole("button", { name: "Upload" }));

	expect(await screen.findByText("../notes-app")).toBeInTheDocument();
	expect(mocks.createRepository).toHaveBeenCalledOnce();
	expect(push).toHaveBeenCalledTimes(2);
	choose.mockRestore();
	push.mockRestore();
});

it("checks a folder's repository against the workspace before making it", async () => {
	mocks.createRepository.mockReset();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace", repos: [{ url: "https://github.com/acme/11x" }] }) });
	vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/11x");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/11x", repos: [] });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	// A new acme/11x would be the repository already in the workspace.
	expect(await screen.findByText("acme/11x is listed twice.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Upload" })).toBeDisabled();
	const name = screen.getByLabelText("Repository name for /Users/me/11x");
	await userEvent.clear(name);
	await userEvent.type(name, "alice");
	// The main repository's folder is taken too.
	expect(await screen.findByText("acme/alice is already the main repository.")).toBeInTheDocument();
	await userEvent.clear(name);
	await userEvent.type(name, "repository");
	expect(await screen.findByText(/which Orchestrator uses itself/)).toBeInTheDocument();
	await userEvent.clear(name);
	await userEvent.type(name, "site.git");
	expect(await screen.findByText(/is not a valid repository name/)).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Upload" })).toBeDisabled();
	expect(mocks.createRepository).not.toHaveBeenCalled();
});

it("offers no folder upload once the workspace is full", async () => {
	const repos = Array.from({ length: 10 }, (_, i) => ({ url: `https://github.com/acme/r${i}` }));
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace", repos }) });
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	expect(await screen.findByRole("button", { name: "Add a local folder" })).toBeDisabled();
	expect(screen.getAllByText("A project can have at most 10 additional repositories.").length).toBeGreaterThan(0);
});

it("says a repository folder uploads its commits and holds the dialog open while it uploads", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	mocks.createRepository.mockReset().mockResolvedValue({ repository: repository("9", "notes"), created: true, pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git" });
	vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [{ name: "notes", path: "/Users/me/notes", relativePath: ".", branch: "auto", remote: "", hasRemote: false, isRepo: true, hasCommit: true }] });
	const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload").mockResolvedValue({ status: "ok", source: "head", branch: "draft", leftOut: [] });
	onTestFinished(() => preview.mockRestore());
	let finishPush: (value: Awaited<ReturnType<typeof aoBridge.cloud.pushFolderToRepository>>) => void = () => undefined;
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockImplementation(() => new Promise((resolve) => { finishPush = resolve; }));
	const onSaveState = vi.fn();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" onSaveState={onSaveState} />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	expect(await screen.findByText("Uploads the commits of draft, not uncommitted changes")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Upload" }));
	await waitFor(() => expect(push).toHaveBeenCalledOnce());
	// Settings waits for an upload as for a save in flight.
	await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ requestPending: true, phase: "saving" })));

	finishPush({ status: "pushed", branch: "main", commit: "abc", source: "head", leftOut: [] });
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ requestPending: false, phase: "saved", dirty: false })));
	push.mockRestore();
});

it("asks to sign in again when an upload finds the session expired", async () => {
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	mocks.createRepository.mockReset().mockResolvedValue({ repository: repository("9", "notes"), created: true, pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git" });
	vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] });
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockResolvedValue({ status: "error", code: "signed_out", message: "Your AO Cloud session expired." });
	const signIn = vi.spyOn(aoBridge.cloud, "signIn").mockResolvedValue(undefined as never);
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	await userEvent.click(await screen.findByRole("button", { name: "Upload" }));

	expect(await screen.findByText("Your Orchestrator Cloud session expired. Sign in again, then try again.")).toBeInTheDocument();
	expect(signIn).toHaveBeenCalledOnce();
	expect(mocks.patch).not.toHaveBeenCalled();
	push.mockRestore();
	signIn.mockRestore();
});

/** A workspace holding 11x and voice-calling-ai, whose next folder upload waits for finish. */
function slowUploadSetup() {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace", repos: [{ url: "https://github.com/acme/11x" }, { url: "https://github.com/acme/voice-calling-ai" }] }) });
	mocks.createRepository.mockReset().mockResolvedValue({ repository: repository("9", "notes"), created: true, pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git" });
	const spies = [
		vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes"),
		vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] }),
	];
	const upload = { finish: (): void => undefined };
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockImplementation(() => new Promise((resolve) => {
		upload.finish = () => resolve({ status: "pushed", branch: "main", commit: "abc", source: "import", leftOut: [] });
	}));
	onTestFinished(() => [...spies, push].forEach((spy) => spy.mockRestore()));
	return { upload, push };
}

it("keeps a repository removed and saved during an upload out of the workspace when the upload saves", async () => {
	const { upload, push } = slowUploadSetup();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	await userEvent.click(await screen.findByRole("button", { name: "Upload" }));
	await waitFor(() => expect(push).toHaveBeenCalledOnce());

	// While it uploads, 11x is removed and saved.
	await userEvent.click(screen.getByRole("button", { name: "Remove acme/11x" }));
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: { repos: [{ url: "https://github.com/acme/voice-calling-ai" }] } });
	await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled());

	upload.finish();

	await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(2));
	expect(mocks.patch.mock.calls[1][2]).toEqual({
		config: { repos: [{ url: "https://github.com/acme/voice-calling-ai" }, { url: "https://github.com/acme/notes" }] },
	});
	expect(await screen.findByText("../notes")).toBeInTheDocument();
	expect(screen.queryByText("../11x")).not.toBeInTheDocument();
});

it("saves an uploaded folder after a save in flight, on top of what it saved", async () => {
	const { upload, push } = slowUploadSetup();
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);
	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	await userEvent.click(await screen.findByRole("button", { name: "Upload" }));
	await waitFor(() => expect(push).toHaveBeenCalledOnce());

	let finishSave: () => void = () => undefined;
	mocks.patch.mockImplementationOnce((_org: string, _id: string, body: { config: Record<string, unknown> }) => new Promise((resolve) => {
		finishSave = () => resolve({ project: project({ layout: "workspace", ...body.config }) });
	}));
	await userEvent.click(screen.getByRole("button", { name: "Remove acme/11x" }));
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());

	// The upload ends while that save is still in flight.
	upload.finish();
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(mocks.patch).toHaveBeenCalledOnce();
	finishSave();

	await waitFor(() => expect(mocks.patch).toHaveBeenCalledTimes(2));
	expect(mocks.patch.mock.calls[1][2]).toEqual({
		config: { repos: [{ url: "https://github.com/acme/voice-calling-ai" }, { url: "https://github.com/acme/notes" }] },
	});
	expect(await screen.findByText("../notes")).toBeInTheDocument();
	expect(screen.queryByText("../11x")).not.toBeInTheDocument();
	await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled());
});

it("says a picked folder is being checked, and takes no other folder until it is", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/slow");
	const scan = vi.spyOn(aoBridge.app, "scanImportFolder").mockImplementation(async ({ path }) => ({ path, repos: [] }));
	let finishSlow: () => void = () => undefined;
	const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload")
		.mockImplementationOnce(() => new Promise((resolve) => { finishSlow = () => resolve({ status: "ok", source: "import", leftOut: [] }); }));
	onTestFinished(() => [choose, scan, preview].forEach((spy) => spy.mockRestore()));
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	expect(await screen.findByRole("status")).toHaveTextContent("Checking slow");
	// A second click would start a second full check of a folder.
	expect(screen.getByRole("button", { name: "Add a local folder" })).toBeDisabled();
	expect(choose).toHaveBeenCalledOnce();

	finishSlow();
	expect(await screen.findByLabelText("Repository name for /Users/me/slow")).toBeInTheDocument();
	expect(screen.queryByText("Checking slow")).not.toBeInTheDocument();
});

it("takes no other folder while the folder dialog is open, and none after it is cancelled", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	let pick: (folder: string | null) => void = () => undefined;
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockImplementationOnce(() => new Promise((resolve) => { pick = resolve; }));
	const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload").mockClear();
	onTestFinished(() => [choose, preview].forEach((spy) => spy.mockRestore()));
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	await waitFor(() => expect(screen.getByRole("button", { name: "Add a local folder" })).toBeDisabled());
	expect(screen.queryByRole("status")).toBeNull();

	act(() => pick(null));
	await waitFor(() => expect(screen.getByRole("button", { name: "Add a local folder" })).toBeEnabled());
	expect(preview).not.toHaveBeenCalled();
});

it("says a folder's repository is on GitHub and still empty when its upload fails", async () => {
	workspacePatches();
	mocks.get.mockResolvedValue({ project: project({ layout: "workspace" }) });
	mocks.createRepository.mockReset().mockResolvedValue({ repository: repository("9", "notes"), created: true, pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git" });
	const choose = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue("/Users/me/notes");
	const scan = vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/Users/me/notes", repos: [] });
	const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockResolvedValue({ status: "error", code: "push_failed", message: "Could not upload notes: remote hung up" });
	onTestFinished(() => [choose, scan, push].forEach((spy) => spy.mockRestore()));
	renderWith(<CloudProjectRepositoriesSettings projectId="p" cloudOrgId="org-1" />);

	await userEvent.click(await screen.findByRole("button", { name: "Add a local folder" }));
	await userEvent.click(await screen.findByRole("button", { name: "Upload" }));

	expect(await screen.findByText("Could not upload notes.")).toBeInTheDocument();
	expect(screen.getByText("acme/notes is on GitHub but still empty. Try again to upload into it. If you don't, it stays on GitHub empty: delete it there, or add notes again under the same name to upload into it later.")).toBeInTheDocument();
});
