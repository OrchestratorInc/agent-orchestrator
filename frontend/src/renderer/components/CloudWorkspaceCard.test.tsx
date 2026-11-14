import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import { useUiStore } from "../stores/ui-store";
import { CloudWorkspaceCard } from "./CloudWorkspaceCard";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	role: "admin" as string | undefined,
	chooseDirectory: vi.fn(),
	scanImportFolder: vi.fn(),
	getRepositoryBranch: vi.fn(),
	openExternal: vi.fn(),
	signIn: vi.fn(),
	pushFolderToRepository: vi.fn(),
	previewFolderUpload: vi.fn(),
	listUserProviderConnections: vi.fn(),
	listGitHubInstallations: vi.fn(),
	listGitHubRepositories: vi.fn(),
	syncGitHubInstallation: vi.fn(),
	startGitHubInstallation: vi.fn(),
	createGitHubRepository: vi.fn(),
	createGitHubProject: vi.fn(),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: { chooseDirectory: mocks.chooseDirectory, scanImportFolder: mocks.scanImportFolder, getRepositoryBranch: mocks.getRepositoryBranch, openExternal: mocks.openExternal },
		cloud: { pushFolderToRepository: mocks.pushFolderToRepository, previewFolderUpload: mocks.previewFolderUpload },
	},
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: {
			listUserProviderConnections: mocks.listUserProviderConnections,
			listGitHubInstallations: mocks.listGitHubInstallations,
			listGitHubRepositories: mocks.listGitHubRepositories,
			syncGitHubInstallation: mocks.syncGitHubInstallation,
			startGitHubInstallation: mocks.startGitHubInstallation,
			createGitHubRepository: mocks.createGitHubRepository,
			createGitHubProject: mocks.createGitHubProject,
		},
		ready: true,
		baseUrl: "https://cp.example.com",
	}),
}));

vi.mock("../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: { id: "org-1", slug: "acme", displayName: "Acme", role: mocks.role }, isLoading: false, error: undefined, ready: true }),
}));

vi.mock("../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ providers: ["freestyle"], defaultProvider: "freestyle", hasCoder: false, offersProviderChoice: false, ready: true, loaded: true, isLoading: false }),
}));

vi.mock("../hooks/useOrgCoderConfig", () => ({ useOrgCoderConfig: () => ({ data: null, isLoading: false }) }));
vi.mock("../hooks/useCoderTemplates", () => ({ useCoderTemplates: () => ({ templates: [], isLoading: false, isError: false }) }));

function Providers({ children }: { children: ReactNode }) {
	const [queryClient] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }));
	return (
		<TooltipProvider>
			<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
		</TooltipProvider>
	);
}

function installation(accountLogin: string, id = `inst-${accountLogin}`) {
	return { id, githubInstallationId: "100", accountLogin, accountType: "Organization", status: "active", repositorySelection: "all", syncStatus: "ready", createdAt: "", updatedAt: "" };
}

function repo(fullName: string, githubRepositoryId: string) {
	const name = fullName.split("/")[1] ?? fullName;
	return { githubRepositoryId, name, fullName, htmlUrl: `https://github.com/${fullName}`, defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "" };
}

function renderCard(onCreated = vi.fn(), onBusyChange?: (busy: boolean) => void) {
	const view = render(<CloudWorkspaceCard onAuthRequired={vi.fn()} onBack={vi.fn()} onCreated={onCreated} onBusyChange={onBusyChange} />, { wrapper: Providers });
	return Object.assign(onCreated, { unmount: view.unmount });
}

function scanned(path: string, overrides: Record<string, unknown>) {
	return { path, repos: [{ name: "notes", path, relativePath: ".", branch: "auto", remote: "", hasRemote: false, isRepo: true, hasCommit: true, status: "ok", ...overrides }] };
}

async function addRepository(user: ReturnType<typeof userEvent.setup>, fullName: string) {
	await user.click(await screen.findByRole("button", { name: "Add a repository" }));
	await user.click(await screen.findByRole("option", { name: new RegExp(fullName) }));
}

beforeEach(() => {
	mocks.role = "admin";
	mocks.chooseDirectory.mockReset();
	mocks.scanImportFolder.mockReset().mockImplementation(async ({ path }: { path: string }) => ({ path, repos: [] }));
	mocks.getRepositoryBranch.mockReset().mockResolvedValue(undefined);
	mocks.openExternal.mockReset();
	mocks.pushFolderToRepository.mockReset().mockResolvedValue({ status: "pushed", branch: "main", commit: "abc", source: "import", leftOut: [] });
	mocks.previewFolderUpload.mockReset().mockResolvedValue({ status: "ok", source: "import", leftOut: [] });
	mocks.listUserProviderConnections.mockReset().mockResolvedValue({
		providerConnections: [{ id: "conn-1", provider: "claude-code", label: "default", config: {}, validationState: "valid", createdAt: "", updatedAt: "" }],
	});
	mocks.listGitHubInstallations.mockReset().mockResolvedValue({ installations: [installation("acme")] });
	mocks.listGitHubRepositories.mockReset().mockResolvedValue({ items: [repo("acme/api", "11"), repo("acme/web", "12"), repo("other/lib", "13")], page: { hasMore: false } });
	mocks.syncGitHubInstallation.mockReset();
	mocks.startGitHubInstallation.mockReset();
	mocks.createGitHubRepository.mockReset().mockImplementation(async (_org: string, body: { name: string; owner?: string }) => ({
		repository: repo(`${body.owner ?? "acme"}/${body.name}`, "77"),
		created: true,
		pushPath: `/api/cloud/v1/orgs/org-1/github/git/${body.owner ?? "acme"}/${body.name}.git`,
	}));
	mocks.createGitHubProject.mockReset().mockResolvedValue({ project: { id: "ws-1", config: { layout: "workspace" } } });
	useUiStore.setState({ autoStartOrchestratorProjectIds: new Set() });
});

describe("CloudWorkspaceCard", () => {
	it("says which secret-looking files a folder's upload leaves out, before it runs", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.previewFolderUpload.mockResolvedValue({ status: "ok", source: "import", leftOut: [".env", "keys/id_rsa"] });

		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));

		expect(await screen.findByText("Leaves out files that look like secrets: .env, keys/id_rsa")).toBeInTheDocument();
		expect(mocks.previewFolderUpload).toHaveBeenCalledWith({ folder: "/Users/me/notes" });
		expect(mocks.pushFolderToRepository).not.toHaveBeenCalled();
	});

	it("refuses a repository folder whose history holds secret-looking files when it is picked", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.previewFolderUpload.mockResolvedValue({ status: "error", code: "secret_files", message: "notes's history holds files that look like secrets (.env)", files: [".env"] });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));

		// Said once, naming the files.
		expect(await screen.findByText("The history of notes holds files that look like secrets (.env), so Orchestrator does not upload it. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub.")).toBeInTheDocument();
		expect(screen.queryByText(/notes's history holds/)).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Repository name for notes")).not.toBeInTheDocument();
		expect(mocks.createGitHubRepository).not.toHaveBeenCalled();
	});

	it("shows a folder's upload refusal once, under that folder", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.pushFolderToRepository.mockResolvedValueOnce({ status: "error", code: "branch_exists", message: "already has a main branch" });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		const text = "The repository for notes already has commits, and Orchestrator only uploads a folder into an empty repository. If an earlier upload finished, remove notes and add its repository from GitHub instead.";
		await waitFor(() => expect(screen.getAllByText(text)).toHaveLength(1));
	});

	it("offers to connect GitHub when the new repository's account has no usable installation", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.createGitHubRepository.mockRejectedValueOnce(new CloudCpError("The AO GitHub App is not connected to this organization for acme.", { status: 422, code: "github_installation_required" }));

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		expect((await screen.findAllByText("The AO GitHub App is not connected to this organization for acme.")).length).toBeGreaterThan(0);
		expect(screen.getByRole("button", { name: /Connect repository/ })).toBeInTheDocument();
		expect(mocks.pushFolderToRepository).not.toHaveBeenCalled();
	});

	it("names the checked-out branch a git repository folder uploads, not its origin's default", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		// The scan reports origin's default branch ("auto" without one); the
		// main process pushes, and names, what is checked out.
		mocks.scanImportFolder.mockResolvedValue(scanned("/Users/me/notes", { branch: "main" }));
		mocks.previewFolderUpload.mockResolvedValue({ status: "ok", source: "head", branch: "draft", leftOut: [] });

		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));

		expect(await screen.findByText("Uploads the commits of draft, not uncommitted changes")).toBeInTheDocument();
		expect(mocks.previewFolderUpload).toHaveBeenCalledWith({ folder: "/Users/me/notes" });
	});

	it("says a detached repository folder uploads its checked-out commit", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.scanImportFolder.mockResolvedValue(scanned("/Users/me/notes", {}));
		mocks.previewFolderUpload.mockResolvedValue({ status: "ok", source: "head", leftOut: [] });

		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));

		expect(await screen.findByText("Uploads the checked-out commit and its history, not uncommitted changes")).toBeInTheDocument();
		expect(screen.queryByText(/commits of auto/)).not.toBeInTheDocument();
	});

	it("says what the main process uploads, whatever the scan made of the folder", async () => {
		const user = userEvent.setup();
		renderCard();
		// A linked worktree, whose .git file the scan reads as a plain folder.
		mocks.chooseDirectory.mockResolvedValueOnce("/Users/me/notes-wt");
		mocks.scanImportFolder.mockResolvedValueOnce(scanned("/Users/me/notes-wt", { isRepo: false, hasCommit: false, needsGitInit: true, branch: "" }));
		mocks.previewFolderUpload.mockResolvedValueOnce({ status: "ok", source: "head", branch: "wt-branch", leftOut: [] });
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByText("Uploads the commits of wt-branch, not uncommitted changes")).toBeInTheDocument();

		// A repository with a commit the main process imports as files.
		mocks.chooseDirectory.mockResolvedValueOnce("/Users/me/draft");
		mocks.scanImportFolder.mockResolvedValueOnce(scanned("/Users/me/draft", {}));
		mocks.previewFolderUpload.mockResolvedValueOnce({ status: "ok", source: "import", leftOut: [] });
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByText("Uploaded to a new private repository")).toBeInTheDocument();
		expect(mocks.getRepositoryBranch).not.toHaveBeenCalled();
	});

	it("keeps Create off while the folder dialog is open, and uploads the folder picked in it", async () => {
		const user = userEvent.setup();
		const onCreated = renderCard();
		let pick: (folder: string | null) => void = () => undefined;
		mocks.chooseDirectory.mockImplementationOnce(() => new Promise((resolve) => { pick = resolve; }));

		await user.type(screen.getByLabelText("Workspace name"), "Platform");
		await addRepository(user, "acme/api");
		expect(await screen.findByRole("button", { name: "Create" })).toBeEnabled();
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		await waitFor(() => expect(mocks.chooseDirectory).toHaveBeenCalledOnce());
		// The dialog does not block the window, so Create is still on the screen.
		expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();

		pick("/Users/me/notes");
		expect(await screen.findByLabelText("Repository name for notes")).toBeInTheDocument();
		await waitFor(() => expect(screen.getByRole("button", { name: "Create" })).toBeEnabled());
		await user.click(screen.getByRole("button", { name: "Create" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("ws-1"));
		expect(mocks.pushFolderToRepository).toHaveBeenCalledWith(expect.objectContaining({ folder: "/Users/me/notes" }));
		expect(mocks.createGitHubProject.mock.calls[0]?.[1].config.repos).toEqual([{ url: "https://github.com/acme/notes" }]);
	});

	it("turns Create back on when the folder dialog is cancelled", async () => {
		const user = userEvent.setup();
		renderCard();
		let pick: (folder: string | null) => void = () => undefined;
		mocks.chooseDirectory.mockImplementationOnce(() => new Promise((resolve) => { pick = resolve; }));

		await user.type(screen.getByLabelText("Workspace name"), "Platform");
		await addRepository(user, "acme/api");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Create" })).toBeDisabled());

		pick(null);
		await waitFor(() => expect(screen.getByRole("button", { name: "Create" })).toBeEnabled());
		expect(mocks.previewFolderUpload).not.toHaveBeenCalled();
	});

	it("creates a workspace from repositories and a local folder", async () => {
		const user = userEvent.setup();
		const onCreated = renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/My Notes");

		await user.type(screen.getByLabelText("Workspace name"), "Platform");
		await addRepository(user, "acme/api");
		await addRepository(user, "acme/web");
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		// The folder's repository is named after it, editable before it exists.
		expect(await screen.findByLabelText("Repository name for My Notes")).toHaveValue("My-Notes");
		await user.click(await screen.findByRole("button", { name: "Create" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("ws-1"));
		expect(mocks.createGitHubRepository).toHaveBeenCalledWith("org-1", { name: "My-Notes", owner: "acme", description: "Uploaded from Orchestrator" });
		expect(mocks.pushFolderToRepository).toHaveBeenCalledWith({
			folder: "/Users/me/My Notes",
			pushPath: "/api/cloud/v1/orgs/org-1/github/git/acme/My-Notes.git",
			branch: "main",
		});
		expect(mocks.createGitHubProject).toHaveBeenCalledWith("org-1", {
			githubRepositoryId: "11",
			displayName: "Platform",
			config: {
				worker: { agent: "claude-code" },
				orchestrator: { agent: "claude-code" },
				layout: "workspace",
				repos: [{ url: "https://github.com/acme/web" }, { url: "https://github.com/acme/My-Notes" }],
			},
		}, { idempotencyKey: expect.any(String) });
		// A Freestyle workspace opens on its orchestrator, already starting.
		expect(useUiStore.getState().autoStartOrchestratorProjectIds.has("ws-1")).toBe(true);
	});

	it("adds a folder whose origin is a granted repository as that repository", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/src/web");
		mocks.scanImportFolder.mockResolvedValue({ path: "/Users/me/src/web", repos: [{ remote: "git@github.com:acme/web.git", isRepo: true, hasCommit: true }] });

		await user.type(screen.getByLabelText("Workspace name"), "Solo");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByText("acme/web")).toBeInTheDocument();
		expect(screen.getByText("The origin of web")).toBeInTheDocument();
		await user.click(await screen.findByRole("button", { name: "Create" }));

		await waitFor(() => expect(mocks.createGitHubProject).toHaveBeenCalled());
		expect(mocks.createGitHubRepository).not.toHaveBeenCalled();
		expect(mocks.pushFolderToRepository).not.toHaveBeenCalled();
		expect(mocks.createGitHubProject.mock.calls[0]?.[1]).toMatchObject({ githubRepositoryId: "12", config: { layout: "workspace" } });
		expect(mocks.createGitHubProject.mock.calls[0]?.[1].config.repos).toBeUndefined();
	});

	it("keeps a created repository when its upload fails and only retries the upload", async () => {
		const user = userEvent.setup();
		const onCreated = renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.pushFolderToRepository.mockResolvedValueOnce({ status: "error", code: "push_failed", message: "Could not upload notes: remote hung up" });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		// The reason in the app's language, with git's own words beside it.
		expect((await screen.findAllByText("Could not upload notes.")).length).toBeGreaterThan(0);
		expect(screen.getByText("Could not upload notes: remote hung up")).toBeInTheDocument();
		expect(mocks.createGitHubProject).not.toHaveBeenCalled();
		// Once made, the repository's name is fixed.
		expect(screen.queryByLabelText("Repository name for notes")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Create" }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("ws-1"));
		expect(mocks.createGitHubRepository).toHaveBeenCalledTimes(1);
		expect(mocks.pushFolderToRepository).toHaveBeenCalledTimes(2);
	});

	it.each([
		["shallow_repository", "notes is a shallow clone, which GitHub does not accept as a new repository. Fetch its full history (git fetch --unshallow), then try again.", false],
		["lfs_unsupported", "notes uses Git LFS, which folder uploads do not support. Push it to GitHub yourself, then add it as a repository.", false],
		["branch_exists", "The repository for notes already has commits, and Orchestrator only uploads a folder into an empty repository. If an earlier upload finished, remove notes and add its repository from GitHub instead.", false],
		// GitHub's own refusal may name the files, so it stays as detail.
		["workflows_refused", "GitHub refused notes because it contains .github/workflows files and the Orchestrator GitHub App lacks the workflows permission. Remove them or grant the permission, then try again.", true],
		["file_too_large", "notes has a file larger than GitHub's 100 MB limit. Add it to its .gitignore, then try again.", true],
	])("explains a %s upload refusal in the app's language", async (code, text, keepsDetail) => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.pushFolderToRepository.mockResolvedValue({ status: "error", code, message: "English text from the main process" });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		expect((await screen.findAllByText(text)).length).toBeGreaterThan(0);
		// A message that says nothing more is not repeated.
		expect(screen.queryByText("English text from the main process") !== null).toBe(keepsDetail);
	});

	it("keeps the main process's detail for a refusal it explains further", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.pushFolderToRepository.mockResolvedValue({ status: "error", code: "folder_has_repositories", message: "notes holds git repositories of its own (lib, vendor/x)." });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		expect((await screen.findAllByText(/notes contains other git repositories or submodules/)).length).toBeGreaterThan(0);
		expect(screen.getByText("notes holds git repositories of its own (lib, vendor/x).")).toBeInTheDocument();
	});

	it("says an owner or admin is needed only when the project create is refused", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.createGitHubRepository.mockRejectedValueOnce(new CloudCpError("You cannot create repositories here.", { status: 403, code: "forbidden" }));

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));
		expect((await screen.findAllByText("You cannot create repositories here.")).length).toBeGreaterThan(0);
		expect(screen.queryByText(/needs an organization owner or admin/)).not.toBeInTheDocument();

		mocks.createGitHubProject.mockRejectedValueOnce(new CloudCpError("Forbidden.", { status: 403, code: "forbidden" }));
		await user.click(screen.getByRole("button", { name: "Create" }));
		expect(await screen.findByText("Forbidden. A workspace with more than one repository needs an organization owner or admin.")).toBeInTheDocument();
	});

	it("reports itself busy while creating, and stops when closed mid-upload", async () => {
		const user = userEvent.setup();
		const onBusyChange = vi.fn();
		const card = renderCard(vi.fn(), onBusyChange);
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		let finishPush: (value: unknown) => void = () => undefined;
		mocks.pushFolderToRepository.mockImplementation(() => new Promise((resolve) => { finishPush = resolve; }));

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));
		await waitFor(() => expect(mocks.pushFolderToRepository).toHaveBeenCalledOnce());
		expect(onBusyChange).toHaveBeenLastCalledWith(true);

		card.unmount();
		expect(onBusyChange).toHaveBeenLastCalledWith(false);
		finishPush({ status: "pushed", branch: "main", commit: "abc", source: "import" });
		await new Promise((resolve) => setTimeout(resolve, 20));
		// Nothing more runs for a card that is gone: no project, no navigation.
		expect(mocks.createGitHubProject).not.toHaveBeenCalled();
		expect(card).not.toHaveBeenCalled();
	});

	it("asks which account gets new repositories when several are connected", async () => {
		const user = userEvent.setup();
		mocks.listGitHubInstallations.mockResolvedValue({ installations: [installation("acme"), installation("me")] });
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByRole("button", { name: "Create" })).toBeDisabled();

		await user.click(screen.getByLabelText("Create new repositories in"));
		await user.click(await screen.findByRole("option", { name: "me" }));
		await user.click(screen.getByRole("button", { name: "Create" }));

		await waitFor(() => expect(mocks.createGitHubRepository).toHaveBeenCalledWith("org-1", expect.objectContaining({ owner: "me" })));
	});

	it("shows the control plane's reason when a repository cannot be made", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.createGitHubRepository.mockRejectedValue(new CloudCpError("acme/notes already exists and has commits.", { status: 422, code: "repository_create_refused" }));

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		expect((await screen.findAllByText("acme/notes already exists and has commits.")).length).toBeGreaterThan(0);
		expect(mocks.pushFolderToRepository).not.toHaveBeenCalled();
	});

	it("flags repositories from different accounts and folders that clash", async () => {
		const user = userEvent.setup();
		renderCard();
		await user.type(screen.getByLabelText("Workspace name"), "Mixed");
		await addRepository(user, "acme/api");
		await addRepository(user, "other/lib");

		expect(await screen.findByText(/must all belong to one GitHub account/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();

		await user.click(screen.getByRole("button", { name: "Remove other/lib" }));
		mocks.chooseDirectory.mockResolvedValue("/Users/me/api");
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		// A new acme/api would be the repository already listed.
		expect(await screen.findByText("acme/api is listed twice.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
		await user.clear(screen.getByLabelText("Repository name for api"));
		await user.type(screen.getByLabelText("Repository name for api"), "api-notes");
		expect(screen.getByRole("button", { name: "Create" })).toBeEnabled();
	});

	it("tells a plain member up front that more than one repository needs an admin", async () => {
		mocks.role = "member";
		const user = userEvent.setup();
		renderCard();
		await user.type(screen.getByLabelText("Workspace name"), "Pair");
		await addRepository(user, "acme/api");
		expect(screen.queryByText(/Only organization owners and admins/)).not.toBeInTheDocument();
		await addRepository(user, "acme/web");

		expect(screen.getByText(/Only organization owners and admins/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
		const list = screen.getByRole("list");
		expect(within(list).getAllByRole("listitem")).toHaveLength(2);
	});

	it("keeps what joined while a folder was being checked", async () => {
		const user = userEvent.setup();
		renderCard();
		let finishPreview: (value: unknown) => void = () => undefined;
		mocks.chooseDirectory.mockResolvedValueOnce("/Users/me/slow").mockResolvedValueOnce("/Users/me/fast");
		mocks.previewFolderUpload.mockImplementationOnce(() => new Promise((resolve) => { finishPreview = resolve; }));

		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await waitFor(() => expect(mocks.previewFolderUpload).toHaveBeenCalledWith({ folder: "/Users/me/slow" }));
		// While slow is checked, a repository and another folder join.
		await addRepository(user, "acme/api");
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByLabelText("Repository name for fast")).toBeInTheDocument();

		finishPreview({ status: "ok", source: "import", leftOut: [] });

		expect(await screen.findByLabelText("Repository name for slow")).toBeInTheDocument();
		expect(screen.getByLabelText("Repository name for fast")).toBeInTheDocument();
		expect(screen.getByText("acme/api")).toBeInTheDocument();
		expect(within(screen.getByRole("list")).getAllByRole("listitem")).toHaveLength(3);
	});

	it("waits for a folder being checked before it creates, so the folder is not left out", async () => {
		const user = userEvent.setup();
		const onCreated = renderCard();
		let finishPreview: (value: unknown) => void = () => undefined;
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.previewFolderUpload.mockImplementationOnce(() => new Promise((resolve) => { finishPreview = resolve; }));

		await user.type(screen.getByLabelText("Workspace name"), "Platform");
		await addRepository(user, "acme/api");
		await user.click(screen.getByRole("button", { name: "Add a local folder" }));
		expect(await screen.findByText("Checking notes")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();

		finishPreview({ status: "ok", source: "import", leftOut: [] });
		expect(await screen.findByLabelText("Repository name for notes")).toBeInTheDocument();
		expect(screen.queryByText("Checking notes")).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Create" }));

		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("ws-1"));
		expect(mocks.createGitHubProject.mock.calls[0]?.[1].config.repos).toEqual([{ url: "https://github.com/acme/notes" }]);
	});

	it("says a folder's repository is on GitHub and still empty when its upload fails", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.pushFolderToRepository.mockResolvedValueOnce({ status: "error", code: "push_failed", message: "Could not upload notes: remote hung up" });

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		const text = "acme/notes is on GitHub but still empty. Try again to upload into it. If you don't, it stays on GitHub empty: delete it there, or add notes again under the same name to upload into it later.";
		expect(await screen.findByText(text)).toBeInTheDocument();
	});

	it("does not call a repository empty when making it failed", async () => {
		const user = userEvent.setup();
		renderCard();
		mocks.chooseDirectory.mockResolvedValue("/Users/me/notes");
		mocks.createGitHubRepository.mockRejectedValueOnce(new CloudCpError("GitHub could not be reached.", { status: 502, code: "SCM_BROKER_FAILED" }));

		await user.type(screen.getByLabelText("Workspace name"), "Notes");
		await user.click(await screen.findByRole("button", { name: "Add a local folder" }));
		await user.click(await screen.findByRole("button", { name: "Create" }));

		expect((await screen.findAllByText("GitHub could not be reached.")).length).toBeGreaterThan(0);
		expect(screen.queryByText(/still empty/)).not.toBeInTheDocument();
	});
});
