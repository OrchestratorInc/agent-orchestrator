import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import type { ImportAction, ImportValidation } from "./api";
import { useNewLocalProject, type NewLocalProject } from "./useNewLocalProject";

// A fake machine: folders with their Git state, validated and prepared the
// way the daemon's importer does it, so the hook's loop runs as it would.
// pushPending: the GitHub repository was made and origin set, but the push failed (ao.import.githubRepository recorded).
type Repo = { isRepo: boolean; hasCommit: boolean; hasOrigin: boolean; pushPending?: boolean; blocking?: string[]; children?: boolean };

const fixtures = vi.hoisted(() => ({
	app: {} as Record<string, unknown>,
	repos: new Map<string, Repo>(),
	failAction: null as ImportAction | null,
	// The next GitHub creation makes the repository but its push fails, as the daemon's partial success.
	failPushOnce: false,
	// Report repoPath as the daemon does: `~` expanded to the machine user's home.
	expandHome: false,
	githubSent: [] as unknown[],
	remote: { ready: true, request: { remoteUrl: "https://github.com/octo/app.git", githubRepository: { owner: "octo", name: "app", private: true } } },
	remoteActive: [] as boolean[],
	listDirectories: vi.fn(),
	addFolderProject: vi.fn(),
	initializeFolder: vi.fn(),
	validateImport: vi.fn(),
	prepareImportGit: vi.fn(),
	prepareClone: vi.fn(),
	cleanupClone: vi.fn(),
	places: new Map<string, { recent: string[]; destination?: string }>(),
	keys: 0,
	createIds: new Map<string, string>(),
	remembered: [] as { hostId: string; place: { folder?: string; destination?: string } }[],
}));

vi.mock("../api", () => ({
	ApiError: class extends Error {
		constructor(readonly status: number, message: string, readonly code?: string, readonly requestId?: string, readonly detail?: string) { super(message); }
	},
}));
vi.mock("../store", () => ({ useApp: () => fixtures.app }));
vi.mock("expo-crypto", () => ({ randomUUID: () => `create-${++fixtures.keys}` }));
vi.mock("./api", () => ({
	listDirectories: fixtures.listDirectories,
	addFolderProject: fixtures.addFolderProject,
	initializeFolder: fixtures.initializeFolder,
	validateImport: fixtures.validateImport,
	prepareImportGit: fixtures.prepareImportGit,
	prepareClone: fixtures.prepareClone,
	cleanupClone: fixtures.cleanupClone,
}));
vi.mock("./recentFolders", () => ({
	loadProjectPlaces: async (hostId: string) => fixtures.places.get(hostId) ?? { recent: [] },
	rememberProjectPlace: async (hostId: string, place: { folder?: string; destination?: string }) => { fixtures.remembered.push({ hostId, place }); },
}));
vi.mock("./useGitHubRepositories", () => ({
	useGitHubRepositories: () => ({ available: false, loading: false, count: 0, query: "", setQuery: () => {}, searching: false, matches: [], take: () => "" }),
}));
vi.mock("./useRemoteSetup", () => ({
	useRemoteSetup: ({ active }: { active: boolean }) => {
		fixtures.remoteActive.push(active);
		return { ready: fixtures.remote.ready, request: fixtures.remote.request };
	},
}));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), success: vi.fn(), error: vi.fn(), warning: vi.fn(), tap: vi.fn() } }));

function validationFor(path: string): ImportValidation {
	const repo = fixtures.repos.get(path) ?? { isRepo: false, hasCommit: false, hasOrigin: false };
	const required: ImportAction[] = [];
	if (!repo.isRepo) required.push("git_init");
	if (!repo.hasCommit) required.push("git_commit");
	if (!repo.hasOrigin || repo.pushPending) required.push("create_remote_repository");
	const blocking = repo.blocking ?? [];
	return {
		isValid: blocking.length === 0,
		blockingErrors: blocking,
		nextStep: blocking.length ? "error" : !repo.isRepo && repo.children ? "choose_import_kind" : required.length ? "prepare_git" : "continue",
		root: { repoPath: fixtures.expandHome ? path.replace(/^~(?=\/|$)/, HOME) : path, isRepo: repo.isRepo, hasCommit: repo.hasCommit, hasOrigin: repo.hasOrigin, requiredActions: required, blockingErrors: blocking },
	};
}

const HOME = "/Users/me";
const listings: Record<string, { path: string; parent: string; entries: { name: string; path: string; gitRepo: boolean }[]; truncated: boolean }> = {
	[HOME]: {
		path: HOME,
		parent: "/Users",
		entries: [
			{ name: "notes", path: `${HOME}/notes`, gitRepo: false },
			{ name: "code", path: `${HOME}/code`, gitRepo: false },
			{ name: "app", path: `${HOME}/app`, gitRepo: true },
		],
		truncated: false,
	},
	[`${HOME}/code`]: { path: `${HOME}/code`, parent: HOME, entries: [], truncated: false },
	[`${HOME}/work`]: { path: `${HOME}/work`, parent: HOME, entries: [], truncated: false },
};

function host(hostId: string, name: string, connection: "open" | "closed" = "open") {
	return { hostId, name, connection, config: { hostId, host: hostId, httpPort: "3011", password: "x" } };
}

function appFixture() {
	const hostStates = [host("mac", "MacBook"), host("studio", "Studio"), host("gone", "Gone", "closed")];
	return {
		hostStates,
		currentHostId: "studio",
		configForHost: (id: string) => hostStates.find((item) => item.hostId === id)?.config ?? null,
		refreshHost: vi.fn(async () => {}),
	};
}

let renderer: ReactTestRenderer | undefined;
let app: ReturnType<typeof appFixture>;
const onCreated = vi.fn();
const current: { project?: NewLocalProject } = {};

function Probe() {
	current.project = useNewLocalProject({ onCreated });
	return null;
}

function project(): NewLocalProject {
	if (!current.project) throw new Error("not rendered");
	return current.project;
}

async function flush() {
	for (let step = 0; step < 10; step++) await act(async () => { await Promise.resolve(); });
}

async function mount() {
	await act(async () => { renderer = create(<Probe />); });
	await flush();
}

async function act_(fn: () => unknown) {
	await act(async () => { await fn(); });
	await flush();
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	app = appFixture();
	fixtures.app = app;
	fixtures.repos = new Map([
		[`${HOME}/app`, { isRepo: true, hasCommit: true, hasOrigin: true }],
		[`${HOME}/notes`, { isRepo: false, hasCommit: false, hasOrigin: false }],
	]);
	fixtures.failAction = null;
	fixtures.failPushOnce = false;
	fixtures.expandHome = false;
	fixtures.githubSent = [];
	fixtures.remote = { ready: true, request: { remoteUrl: "https://github.com/octo/app.git", githubRepository: { owner: "octo", name: "app", private: true } } };
	fixtures.remoteActive = [];
	fixtures.listDirectories.mockReset().mockImplementation(async (_cfg: unknown, path?: string) => {
		const listing = listings[path ?? HOME];
		if (!listing) throw new ApiError(404, "", "FS_NOT_FOUND");
		return listing;
	});
	fixtures.addFolderProject.mockReset().mockImplementation(async (_cfg: unknown, input: { path: string }) => input.path.split("/").pop());
	// As the daemon does: a folder is made once, and a retry with the same id resumes it.
	fixtures.keys = 0;
	fixtures.createIds = new Map();
	fixtures.initializeFolder.mockReset().mockImplementation(async (_cfg: unknown, path: string, options: { createId?: string } = {}) => {
		const madeBy = fixtures.createIds.get(path);
		if (madeBy !== undefined) {
			if (madeBy === options.createId) return;
			throw new ApiError(409, "", "PATH_EXISTS");
		}
		fixtures.createIds.set(path, options.createId ?? "");
		fixtures.repos.set(path, { isRepo: true, hasCommit: true, hasOrigin: false });
	});
	fixtures.validateImport.mockReset().mockImplementation(async (_cfg: unknown, path: string) => validationFor(path));
	fixtures.prepareImportGit.mockReset().mockImplementation(async (_cfg: unknown, input: { path: string; approvedActions: ImportAction[] }) => {
		// The daemon's absolute path back to the name the fake machine keeps.
		if (fixtures.expandHome && input.path.startsWith(`${HOME}/`)) input = { ...input, path: `~${input.path.slice(HOME.length)}` };
		const before = validationFor(input.path);
		const action = before.root.requiredActions[0]!;
		if (fixtures.failAction === action) return { events: [{ action, repoPath: input.path, state: "error", error: "gh: not logged in" }], validation: before };
		if (action === "create_remote_repository") fixtures.githubSent.push((input as { githubRepository?: unknown }).githubRepository);
		if (action === "create_remote_repository" && fixtures.failPushOnce) {
			fixtures.failPushOnce = false;
			fixtures.repos.set(input.path, { ...fixtures.repos.get(input.path)!, hasOrigin: true, pushPending: true });
			return { events: [{ action, repoPath: input.path, state: "error", error: "push initial commit: network failed" }], validation: validationFor(input.path) };
		}
		const repo = { ...fixtures.repos.get(input.path)! } as Repo;
		if (action === "git_init") repo.isRepo = true;
		if (action === "git_commit") repo.hasCommit = true;
		if (action === "create_remote_repository") { repo.hasOrigin = true; repo.pushPending = false; }
		fixtures.repos.set(input.path, repo);
		return { events: [{ action, repoPath: input.path, state: "success" }], validation: validationFor(input.path) };
	});
	fixtures.prepareClone.mockReset().mockImplementation(async (_cfg: unknown, input: { destinationParent: string }) => {
		const path = `${input.destinationParent}/sample`;
		fixtures.repos.set(path, { isRepo: true, hasCommit: true, hasOrigin: true });
		return { path, preparationId: "prep-1" };
	});
	fixtures.cleanupClone.mockReset().mockResolvedValue(undefined);
	fixtures.places.clear();
	fixtures.remembered = [];
	onCreated.mockReset();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	current.project = undefined;
});

describe("useNewLocalProject", () => {
	it("starts on the default machine among the connected ones, browsing its home", async () => {
		await mount();
		expect(project().machines.map((machine) => machine.id)).toEqual(["mac", "studio"]);
		expect(project().hostId).toBe("studio");
		expect(project().home).toBe(HOME);
		expect(project().browseTarget).toBe("folder");
		expect(project().entries.map((entry) => entry.name)).toEqual(["app", "code", "notes"]);
		expect(project().canCreate).toBe(false);
	});

	it("validates a chosen repository as the desktop does, then adds it with no Git setup", async () => {
		await mount();
		await act_(() => project().pressEntry(project().entries.find((entry) => entry.name === "app")!));
		expect(fixtures.validateImport).toHaveBeenCalledWith(expect.objectContaining({ hostId: "studio" }), `${HOME}/app`);
		expect(project().setupActions).toEqual([]);
		expect(project().remoteNeeded).toBe(false);
		expect(project().canCreate).toBe(true);
		await act_(() => project().create());
		expect(fixtures.prepareImportGit).not.toHaveBeenCalled();
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: `${HOME}/app` });
		expect(app.refreshHost).toHaveBeenCalledWith("studio");
		expect(onCreated).toHaveBeenCalledWith("studio", "app");
		expect(fixtures.remembered).toEqual([{ hostId: "studio", place: { folder: `${HOME}/app` } }]);
	});

	it("runs a plain folder's Git setup one step at a time, remote included, before adding it", async () => {
		fixtures.remote.ready = false;
		await mount();
		await act_(() => project().choose(`${HOME}/notes`));
		expect(project().setupActions).toEqual(["git_init", "git_commit", "create_remote_repository"]);
		expect(project().remoteNeeded).toBe(true);
		expect(fixtures.remoteActive.at(-1)).toBe(true);
		// The desktop won't go on without a remote.
		expect(project().canCreate).toBe(false);
		fixtures.remote.ready = true;
		await act_(() => project().setName("Notes"));
		expect(project().canCreate).toBe(true);
		await act_(() => project().create());
		const steps = fixtures.prepareImportGit.mock.calls.map(([, input]) => input as { approvedActions: ImportAction[]; remoteUrl?: string; githubRepository?: unknown });
		expect(steps.map((step) => step.approvedActions[0])).toEqual(["git_init", "git_commit", "create_remote_repository"]);
		// Every step carries the remote while one is still required, as the desktop sends it.
		expect(steps.every((step) => step.remoteUrl === "https://github.com/octo/app.git")).toBe(true);
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: `${HOME}/notes`, name: "Notes" });
		expect(onCreated).toHaveBeenCalledWith("studio", "notes");
	});

	it("stops on a failed step, says which, and shows what is left", async () => {
		fixtures.failAction = "create_remote_repository";
		await mount();
		await act_(() => project().choose(`${HOME}/notes`));
		await act_(() => project().create());
		expect(project().error).toBe("Create remote repository failed: gh: not logged in");
		expect(fixtures.addFolderProject).not.toHaveBeenCalled();
		expect(project().setupActions).toEqual(["create_remote_repository"]);
		expect(project().busy).toBe(false);
	});

	it("refuses folders the desktop refuses, in its words", async () => {
		fixtures.repos.set(`${HOME}/code`, { isRepo: true, hasCommit: true, hasOrigin: true, blocking: ["DETACHED_HEAD"] });
		await mount();
		await act_(() => project().choose(`${HOME}/code`));
		expect(project().blocked).toMatch(/detached HEAD/);
		expect(project().canCreate).toBe(false);
	});

	it("sends folders of repositories to the desktop as workspaces", async () => {
		fixtures.repos.set(`${HOME}/code`, { isRepo: false, hasCommit: false, hasOrigin: false, children: true });
		await mount();
		await act_(() => project().choose(`${HOME}/code`));
		expect(project().blocked).toMatch(/workspace/);
		expect(project().canCreate).toBe(false);
	});

	it("clones without registering, validates the checkout, then adds it with its preparation", async () => {
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://github.com/acme/sample.git"));
		await act_(() => project().create());
		expect(fixtures.prepareClone).toHaveBeenCalledWith(expect.anything(), { remoteUrl: "https://github.com/acme/sample.git", destinationParent: "~/ao/projects" });
		expect(fixtures.validateImport).toHaveBeenCalledWith(expect.anything(), "~/ao/projects/sample");
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/sample", clonePreparationId: "prep-1" });
		expect(fixtures.cleanupClone).not.toHaveBeenCalled();
		expect(fixtures.remembered).toEqual([{ hostId: "studio", place: { destination: "~/ao/projects" } }]);
	});

	it("removes a clone the desktop wouldn't accept", async () => {
		fixtures.prepareClone.mockImplementation(async () => {
			fixtures.repos.set("~/ao/projects/sample", { isRepo: true, hasCommit: true, hasOrigin: true, blocking: ["DETACHED_HEAD"] });
			return { path: "~/ao/projects/sample", preparationId: "prep-1" };
		});
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://github.com/acme/sample.git"));
		await act_(() => project().create());
		expect(project().error).toMatch(/detached HEAD/);
		expect(fixtures.cleanupClone).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/sample", preparationId: "prep-1" });
		expect(fixtures.addFolderProject).not.toHaveBeenCalled();
	});

	it("makes a new project's folder, gives it a remote, then adds it", async () => {
		await mount();
		await act_(() => project().chooseSource("new"));
		expect(project().setupActions).toEqual(["git_init", "git_commit", "create_remote_repository"]);
		await act_(() => project().setName(" My App "));
		await act_(() => project().create());
		expect(fixtures.initializeFolder).toHaveBeenCalledWith(expect.anything(), "~/ao/projects/My-App", { create: true, createId: "create-1" });
		expect(fixtures.prepareImportGit.mock.calls.map(([, input]) => (input as { approvedActions: ImportAction[] }).approvedActions[0])).toEqual(["create_remote_repository"]);
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/My-App", name: "My App" });
		expect(onCreated).toHaveBeenCalledWith("studio", "My-App");
	});

	it("carries on in the same new folder after a failed step", async () => {
		fixtures.failAction = "create_remote_repository";
		await mount();
		await act_(() => project().chooseSource("new"));
		await act_(() => project().setName("app2"));
		await act_(() => project().create());
		expect(project().error).toMatch(/Create remote repository failed/);
		fixtures.failAction = null;
		await act_(() => project().create());
		// The retry names the same create, so the daemon resumes the folder it made.
		const ids = fixtures.initializeFolder.mock.calls.map(([, , options]) => (options as { createId: string }).createId);
		expect(ids).toEqual(["create-1", "create-1"]);
		expect(onCreated).toHaveBeenCalledWith("studio", "app2");
	});

	it("says to update a machine that can't create projects yet", async () => {
		fixtures.initializeFolder.mockRejectedValue(new ApiError(400, "", "INVALID_JSON"));
		await mount();
		await act_(() => project().chooseSource("new"));
		await act_(() => project().setName("app"));
		await act_(() => project().create());
		expect(project().error).toMatch(/Update Orchestrator on this machine to create new projects/);
		expect(fixtures.addFolderProject).not.toHaveBeenCalled();
	});

	it("starts where this machine's last projects went, and offers them as shortcuts", async () => {
		fixtures.places.set("studio", { recent: [`${HOME}/work`, `${HOME}/code`], destination: `${HOME}/work` });
		await mount();
		expect(project().listing?.path).toBe(`${HOME}/work`);
		expect(project().home).toBe(HOME);
		expect(project().destinationParent).toBe(`${HOME}/work`);
		expect(project().recentPlaces).toEqual([`${HOME}/code`]);
		await act_(() => project().setFilter("x"));
		expect(project().recentPlaces).toEqual([]);
	});

	it("stays home when the remembered folder is gone", async () => {
		fixtures.places.set("studio", { recent: [`${HOME}/moved`] });
		await mount();
		expect(project().listing?.path).toBe(HOME);
		expect(project().listingError).toBeNull();
	});

	it("says when the machine predates the routes", async () => {
		fixtures.listDirectories.mockRejectedValue(new ApiError(404, "404 Not Found"));
		await mount();
		expect(project().listingError).toMatch(/Update Orchestrator/);
	});

	it("starts over on another machine", async () => {
		await mount();
		await act_(() => project().choose(`${HOME}/app`));
		await act_(() => project().chooseHost("mac"));
		expect(project().hostId).toBe("mac");
		expect(project().folder).toBeNull();
		expect(project().browseTarget).toBe("folder");
		expect(fixtures.listDirectories).toHaveBeenLastCalledWith(expect.objectContaining({ hostId: "mac" }), undefined);
	});

	it("resumes a new folder whose first create lost its response", async () => {
		let lost = true;
		const made = fixtures.initializeFolder.getMockImplementation()!;
		fixtures.initializeFolder.mockImplementation(async (...args: unknown[]) => {
			await (made as (...a: unknown[]) => Promise<void>)(...args);
			if (lost) { lost = false; throw new Error("Network request failed"); }
		});
		await mount();
		await act_(() => project().chooseSource("new"));
		await act_(() => project().setName("app3"));
		await act_(() => project().create());
		expect(project().error).toBeTruthy();
		await act_(() => project().create());
		expect(onCreated).toHaveBeenCalledWith("studio", "app3");
	});

	it("keeps an unused clone until removing it works, and tries again before cloning anew", async () => {
		fixtures.cleanupClone.mockRejectedValueOnce(new Error("offline"));
		fixtures.prepareClone.mockImplementation(async (_cfg: unknown, input: { remoteUrl: string; destinationParent: string }) => {
			const path = `${input.destinationParent}/${input.remoteUrl.split("/").pop()}`;
			fixtures.repos.set(path, { isRepo: true, hasCommit: true, hasOrigin: false });
			return { path, preparationId: `prep-${path}` };
		});
		fixtures.remote.ready = false;
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://x/one"));
		// The checkout needs a remote: it stays prepared while the form asks for one.
		await act_(() => project().create());
		expect(fixtures.prepareClone).toHaveBeenCalledTimes(1);
		await act_(() => project().setRemoteUrl("https://x/two"));
		expect(fixtures.cleanupClone).toHaveBeenCalledTimes(1);
		expect(project().error).toMatch(/couldn't remove the unused clone/);
		// The stale clone's steps are not this URL's.
		expect(project().setupActions).toEqual([]);
		fixtures.remote.ready = true;
		await act_(() => renderer?.update(<Probe />));
		await act_(() => project().create());
		expect(fixtures.cleanupClone).toHaveBeenCalledTimes(2);
		expect(fixtures.cleanupClone).toHaveBeenLastCalledWith(expect.objectContaining({ hostId: "studio" }), { path: "~/ao/projects/one", preparationId: "prep-~/ao/projects/one" });
		expect(fixtures.prepareClone).toHaveBeenLastCalledWith(expect.anything(), { remoteUrl: "https://x/two", destinationParent: "~/ao/projects" });
	});

	it("removes a clone that finishes after the machine changed, on the machine that made it", async () => {
		let finish!: () => void;
		fixtures.prepareClone.mockImplementation(() => new Promise((resolve) => { finish = () => resolve({ path: "~/ao/projects/sample", preparationId: "prep-1" }); }));
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://x/sample"));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		// The studio goes offline while it clones.
		app.hostStates = app.hostStates.filter((item) => item.hostId !== "studio");
		fixtures.app = { ...app, hostStates: app.hostStates };
		await act_(() => renderer?.update(<Probe />));
		await act_(async () => { finish(); await pending; });
		expect(project().hostId).toBe("mac");
		expect(fixtures.cleanupClone).toHaveBeenCalledWith(expect.objectContaining({ hostId: "studio" }), { path: "~/ao/projects/sample", preparationId: "prep-1" });
		expect(fixtures.addFolderProject).not.toHaveBeenCalled();
	});

	it("can't create while a folder is being browsed, and a new name clears the last error", async () => {
		await mount();
		await act_(() => project().chooseSource("new"));
		await act_(() => project().setName("app"));
		expect(project().canCreate).toBe(true);
		await act_(() => project().changeFolder("destination"));
		expect(project().canCreate).toBe(false);
		await act_(() => project().cancelBrowse());
		fixtures.initializeFolder.mockRejectedValueOnce(new ApiError(409, "", "PATH_EXISTS"));
		await act_(() => project().create());
		expect(project().error).toMatch(/already exists/);
		await act_(() => project().setName("app-2"));
		expect(project().error).toBeNull();
	});

	it("holds the machine while a create runs", async () => {
		let finish!: () => void;
		fixtures.addFolderProject.mockImplementation(() => new Promise((resolve) => { finish = () => resolve("app"); }));
		await mount();
		await act_(() => project().choose(`${HOME}/app`));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		expect(project().busy).toBe(true);
		await act_(() => project().chooseHost("mac"));
		expect(project().hostId).toBe("studio");
		await act_(async () => { finish(); await pending; });
	});

	it("never registers a clone whose removal started, even back on its URL", async () => {
		let removed!: () => void;
		fixtures.cleanupClone.mockImplementationOnce(() => new Promise<void>((resolve) => { removed = resolve; }));
		let n = 0;
		fixtures.prepareClone.mockImplementation(async (_cfg: unknown, input: { destinationParent: string }) => {
			const path = `${input.destinationParent}/sample-${++n}`;
			fixtures.repos.set(path, { isRepo: true, hasCommit: true, hasOrigin: false });
			return { path, preparationId: `prep-${n}` };
		});
		fixtures.remote.ready = false;
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://x/sample"));
		await act_(() => project().create());
		// Away and back while the first clone is still being removed.
		await act_(() => project().setRemoteUrl("https://x/other"));
		await act_(() => project().setRemoteUrl("https://x/sample"));
		expect(project().setupActions).toEqual([]);
		fixtures.remote.ready = true;
		await act_(() => renderer?.update(<Probe />));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		// Create waits for the removal; it doesn't reuse the clone being removed.
		expect(fixtures.prepareClone).toHaveBeenCalledTimes(1);
		await act_(async () => { removed(); await pending; });
		expect(fixtures.cleanupClone).toHaveBeenCalledTimes(1);
		expect(fixtures.prepareClone).toHaveBeenCalledTimes(2);
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/sample-2", clonePreparationId: "prep-2" });
	});

	it("keeps the source and the URL a create was asked for while it runs", async () => {
		let finish!: () => void;
		fixtures.addFolderProject.mockImplementation(() => new Promise((resolve) => { finish = () => resolve("app"); }));
		await mount();
		await act_(() => project().choose(`${HOME}/app`));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		await act_(() => project().chooseSource("clone"));
		expect(project().source).toBe("folder");
		await act_(async () => { finish(); await pending; });
	});

	it("holds the destination while a clone is being created", async () => {
		let finish!: () => void;
		fixtures.addFolderProject.mockImplementation(() => new Promise((resolve) => { finish = () => resolve("sample"); }));
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://x/sample"));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		await act_(() => project().changeFolder("destination"));
		expect(project().browseTarget).toBeNull();
		await act_(async () => { finish(); await pending; });
		expect(fixtures.cleanupClone).not.toHaveBeenCalled();
		expect(onCreated).toHaveBeenCalledWith("studio", "sample");
	});

	it("holds the destination while the clone is still being made, and removes nothing before the add", async () => {
		let cloned!: () => void;
		fixtures.prepareClone.mockImplementation((_cfg: unknown, input: { destinationParent: string }) => new Promise((resolve) => {
			cloned = () => {
				const path = `${input.destinationParent}/sample`;
				fixtures.repos.set(path, { isRepo: true, hasCommit: true, hasOrigin: true });
				resolve({ path, preparationId: "prep-1" });
			};
		}));
		await mount();
		await act_(() => project().chooseSource("clone"));
		await act_(() => project().setRemoteUrl("https://x/sample"));
		let pending!: Promise<void>;
		await act(async () => { pending = project().create(); });
		// prepareClone is still running.
		await act_(() => project().changeFolder("destination"));
		await act_(() => project().choose(`${HOME}/code`));
		expect(project().browseTarget).toBeNull();
		expect(project().destinationParent).toBe("~/ao/projects");
		await act_(async () => { cloned(); await pending; });
		expect(fixtures.cleanupClone).not.toHaveBeenCalled();
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/sample", clonePreparationId: "prep-1" });
		expect(onCreated).toHaveBeenCalledWith("studio", "sample");
	});

	it("retries only the push when GitHub made the repository but the push failed", async () => {
		fixtures.failPushOnce = true;
		await mount();
		await act_(() => project().choose(`${HOME}/notes`));
		await act_(() => project().create());
		expect(project().error).toMatch(/push initial commit: network failed/);
		expect(project().setupActions).toEqual(["create_remote_repository"]);
		// The name is taken now, by the repository the first try made.
		fixtures.remote.ready = false;
		await act_(() => renderer?.update(<Probe />));
		expect(project().remoteResuming).toBe(true);
		expect(project().canCreate).toBe(true);
		await act_(() => project().create());
		expect(fixtures.githubSent).toEqual([fixtures.remote.request.githubRepository, fixtures.remote.request.githubRepository]);
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: `${HOME}/notes` });
		expect(onCreated).toHaveBeenCalledWith("studio", "notes");
	});

	it("checks another repository name as any new one, even after a failed push", async () => {
		fixtures.failPushOnce = true;
		await mount();
		await act_(() => project().choose(`${HOME}/notes`));
		await act_(() => project().create());
		fixtures.remote = { ready: false, request: { remoteUrl: "https://github.com/octo/other.git", githubRepository: { owner: "octo", name: "other", private: true } } };
		await act_(() => renderer?.update(<Probe />));
		expect(project().remoteResuming).toBe(false);
		expect(project().canCreate).toBe(false);
	});

	it("retries the push for a new project under ~/ao/projects, whose path the daemon reports expanded", async () => {
		fixtures.expandHome = true;
		fixtures.failPushOnce = true;
		await mount();
		await act_(() => project().chooseSource("new"));
		await act_(() => project().setName("My App"));
		await act_(() => project().create());
		expect(project().error).toMatch(/push initial commit: network failed/);
		fixtures.remote.ready = false;
		await act_(() => renderer?.update(<Probe />));
		expect(project().remoteResuming).toBe(true);
		expect(project().canCreate).toBe(true);
		await act_(() => project().create());
		expect(fixtures.githubSent).toHaveLength(2);
		expect(fixtures.addFolderProject).toHaveBeenCalledWith(expect.anything(), { path: "~/ao/projects/My-App", name: "My App" });
		expect(onCreated).toHaveBeenCalledWith("studio", "My-App");
	});
});
