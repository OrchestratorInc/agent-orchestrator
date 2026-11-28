import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiRequest } from "../api";
import { addFolderProject, checkGitHubAvailability, cleanupClone, initializeFolder, listDirectories, listGitHubOwners, prepareClone, prepareImportGit, validateImport } from "./api";
import {
	baseName,
	canCreateLocalProject,
	clonePathPreview,
	createUnsupported,
	DEFAULT_CLONE_PARENT,
	displayPath,
	folderNameFor,
	joinPath,
	newProjectPathPreview,
	placeTitle,
	filterDirectories,
	localProjectErrorMessage,
	localProjectRequest,
	repositoryNameFromUrl,
	UNSUPPORTED_MACHINE,
	unsupportedMachine,
	type LocalProjectDraft,
} from "./newLocalProject";

vi.mock("../api", () => ({
	ApiError: class extends Error {
		constructor(readonly status: number, message: string, readonly code?: string, readonly requestId?: string, readonly detail?: string) { super(message); }
	},
	apiRequest: vi.fn(),
}));

afterEach(() => { vi.mocked(apiRequest).mockReset(); });

const cfg = { host: "192.168.1.5", httpPort: "3011", password: "secret" } as never;

function draft(overrides: Partial<LocalProjectDraft>): LocalProjectDraft {
	return { source: "folder", folder: null, remoteUrl: "", destinationParent: DEFAULT_CLONE_PARENT, name: "", ...overrides };
}

function json(body: unknown) {
	return new Response(JSON.stringify(body), { status: 200 });
}

describe("canCreateLocalProject", () => {
	it("needs a chosen folder for an existing folder", () => {
		expect(canCreateLocalProject(draft({ source: "folder" }))).toBe(false);
		expect(canCreateLocalProject(draft({ source: "folder", folder: "/Users/me/app" }))).toBe(true);
	});

	it("needs a URL without spaces and a destination for a clone", () => {
		expect(canCreateLocalProject(draft({ source: "clone", remoteUrl: "  " }))).toBe(false);
		expect(canCreateLocalProject(draft({ source: "clone", remoteUrl: "https://github.com/a/b two" }))).toBe(false);
		expect(canCreateLocalProject(draft({ source: "clone", remoteUrl: "git@github.com:a/b.git", destinationParent: " " }))).toBe(false);
		expect(canCreateLocalProject(draft({ source: "clone", remoteUrl: "git@github.com:a/b.git" }))).toBe(true);
	});

	it("refuses a name past the daemon's limit", () => {
		expect(canCreateLocalProject(draft({ folder: "/x", name: "a".repeat(101) }))).toBe(false);
	});
});

describe("localProjectRequest", () => {
	it("sends a name only when one was typed", () => {
		expect(localProjectRequest(draft({ folder: "/Users/me/app", name: "  " }))).toEqual({ source: "folder", input: { path: "/Users/me/app" } });
		expect(localProjectRequest(draft({ folder: "/Users/me/app", name: " App " }))).toEqual({ source: "folder", input: { path: "/Users/me/app", name: "App" } });
	});

	it("trims a clone's URL and destination", () => {
		expect(localProjectRequest(draft({ source: "clone", remoteUrl: " https://github.com/a/b.git ", destinationParent: " ~/code " }))).toEqual({
			source: "clone",
			input: { remoteUrl: "https://github.com/a/b.git", destinationParent: "~/code" },
		});
	});
});

describe("names and paths", () => {
	it("takes a repository's folder name from HTTPS, SSH and trailing-slash URLs", () => {
		expect(repositoryNameFromUrl("https://github.com/acme/widgets.git")).toBe("widgets");
		expect(repositoryNameFromUrl("git@github.com:acme/widgets.git")).toBe("widgets");
		expect(repositoryNameFromUrl("https://github.com/acme/widgets/")).toBe("widgets");
		expect(repositoryNameFromUrl("")).toBe("");
	});

	it("previews where a clone lands", () => {
		expect(clonePathPreview("https://github.com/acme/widgets", "~/ao/projects/")).toBe("~/ao/projects/widgets");
		expect(clonePathPreview("", "~/ao/projects")).toBeUndefined();
	});

	it("names a folder by its last segment", () => {
		expect(baseName("/Users/me/app/")).toBe("app");
		expect(baseName("C:\\code\\app")).toBe("app");
		expect(baseName("/")).toBe("/");
	});

	it("shortens paths under home only", () => {
		expect(displayPath("/Users/me", "/Users/me")).toBe("~");
		expect(displayPath("/Users/me/code/app", "/Users/me")).toBe("~/code/app");
		expect(displayPath("/Users/meg/app", "/Users/me")).toBe("/Users/meg/app");
		expect(displayPath("/opt/app", undefined)).toBe("/opt/app");
	});

	it("filters folders by name and lists repositories first", () => {
		const entries = [
			{ name: "notes", path: "/h/notes", gitRepo: false },
			{ name: "Zeta", path: "/h/Zeta", gitRepo: true },
			{ name: "alpha", path: "/h/alpha", gitRepo: true },
		];
		expect(filterDirectories(entries, "").map((entry) => entry.name)).toEqual(["alpha", "Zeta", "notes"]);
		expect(filterDirectories(entries, "ET").map((entry) => entry.name)).toEqual(["Zeta"]);
	});
});

describe("errors", () => {
	it("tells a machine without the routes from a missing folder", () => {
		expect(unsupportedMachine(new ApiError(404, "404 Not Found"))).toBe(true);
		expect(unsupportedMachine(new ApiError(404, "", "FS_NOT_FOUND"))).toBe(false);
		expect(localProjectErrorMessage(new ApiError(404, "404 Not Found"))).toBe(UNSUPPORTED_MACHINE);
		expect(localProjectErrorMessage(new ApiError(409, "", "CLONE_DESTINATION_EXISTS"))).toMatch(/already exists/);
	});
});

describe("daemon calls", () => {
	it("lists a directory and drops malformed entries", async () => {
		vi.mocked(apiRequest).mockResolvedValue(json({ path: "/h/code", parent: "/h", entries: [{ name: "app", path: "/h/code/app", gitRepo: true }, { name: 3 }] }));
		await expect(listDirectories(cfg, "/h/code")).resolves.toEqual({ path: "/h/code", parent: "/h", entries: [{ name: "app", path: "/h/code/app", gitRepo: true }], truncated: false });
		expect(vi.mocked(apiRequest).mock.calls[0]?.[1]).toBe("/api/v1/fs/dirs?path=%2Fh%2Fcode");
	});

	it("asks for home when no path is given", async () => {
		vi.mocked(apiRequest).mockResolvedValue(json({ path: "/h", parent: "/", entries: [] }));
		await listDirectories(cfg);
		expect(vi.mocked(apiRequest).mock.calls[0]?.[1]).toBe("/api/v1/fs/dirs");
	});

	it("adds, initializes and clones through the desktop's routes", async () => {
		vi.mocked(apiRequest).mockImplementation(async () => json({ project: { id: "app" } }));
		await expect(addFolderProject(cfg, { path: "/h/app" })).resolves.toBe("app");
		await initializeFolder(cfg, "/h/app");
		const calls = vi.mocked(apiRequest).mock.calls.map(([, path, init]) => [path, init?.method, init?.body]);
		expect(calls).toEqual([
			["/api/v1/projects", "POST", JSON.stringify({ path: "/h/app" })],
			["/api/v1/projects/initialize", "POST", JSON.stringify({ path: "/h/app" })],
		]);
	});

	it("fails when the daemon returns no project", async () => {
		vi.mocked(apiRequest).mockResolvedValue(json({}));
		await expect(addFolderProject(cfg, { path: "/h/app" })).rejects.toThrow(/didn't return/);
	});
});

describe("new projects", () => {
	it("turns a name into a folder name", () => {
		expect(folderNameFor(" My  App ")).toBe("My-App");
		expect(folderNameFor("a/b\\c:d")).toBe("a-b-c-d");
		expect(folderNameFor(".hidden")).toBe("hidden");
		expect(folderNameFor("notes.")).toBe("notes");
		expect(folderNameFor("  ")).toBe("");
	});

	it("joins with the parent's own separator", () => {
		expect(joinPath("~/ao/projects/", "app")).toBe("~/ao/projects/app");
		expect(joinPath("C:\\code", "app")).toBe("C:\\code\\app");
	});

	it("needs a name that makes a folder, and sends the typed name", () => {
		expect(canCreateLocalProject(draft({ source: "new", name: "..." }))).toBe(false);
		expect(canCreateLocalProject(draft({ source: "new", name: "app" }))).toBe(true);
		expect(localProjectRequest(draft({ source: "new", name: "My App" }))).toEqual({ source: "new", input: { path: "~/ao/projects/My-App", name: "My App" } });
		expect(newProjectPathPreview("My App", "~/ao/projects")).toBe("~/ao/projects/My-App");
	});

	it("knows an old machine by its strict decoder or a missing route", () => {
		expect(createUnsupported(new ApiError(400, "", "INVALID_JSON"))).toBe(true);
		expect(createUnsupported(new ApiError(404, "404 Not Found"))).toBe(true);
		expect(createUnsupported(new ApiError(409, "", "PATH_EXISTS"))).toBe(false);
		expect(localProjectErrorMessage(new ApiError(409, "", "PATH_EXISTS"))).toMatch(/already exists/);
	});
});

describe("placeTitle", () => {
	it("calls the home folder Home", () => {
		expect(placeTitle("/Users/me", "/Users/me")).toBe("Home");
		expect(placeTitle("/Users/me/code", "/Users/me")).toBe("code");
	});
});

describe("the desktop's import routes", () => {
	it("validates a folder as a project and runs one Git step at a time", async () => {
		const validation = { isValid: true, blockingErrors: [], nextStep: "continue", root: { repoPath: "/h/app", isRepo: true, hasCommit: true, hasOrigin: true, requiredActions: [], blockingErrors: [] } };
		vi.mocked(apiRequest).mockImplementation(async (_cfg, path) => json(path === "/api/v1/imports/validate" ? validation : { events: [{ action: "git_init", repoPath: "/h/app", state: "success" }], validation }));
		await expect(validateImport(cfg, "/h/app")).resolves.toEqual(validation);
		await prepareImportGit(cfg, { path: "/h/app", approvedActions: ["git_init"], remoteUrl: "https://github.com/o/app.git", githubRepository: { owner: "o", name: "app", private: true } });
		await prepareImportGit(cfg, { path: "/h/app", approvedActions: ["git_commit"] });
		const bodies = vi.mocked(apiRequest).mock.calls.map(([, path, init]) => [path, JSON.parse(String(init?.body))]);
		expect(bodies).toEqual([
			["/api/v1/imports/validate", { importKind: "project", path: "/h/app" }],
			["/api/v1/imports/prepare-git", { importKind: "project", path: "/h/app", approvedActions: ["git_init"], remoteUrl: "https://github.com/o/app.git", githubRepository: { owner: "o", name: "app", private: true }, stepwise: true }],
			["/api/v1/imports/prepare-git", { importKind: "project", path: "/h/app", approvedActions: ["git_commit"], stepwise: true }],
		]);
	});

	it("prepares a clone, and removes one that is abandoned", async () => {
		vi.mocked(apiRequest).mockImplementation(async (_cfg, path) => json(path === "/api/v1/projects/clone/prepare" ? { path: "/h/x", remoteUrl: "u", preparationId: "p1" } : {}));
		await expect(prepareClone(cfg, { remoteUrl: "https://x/y.git", destinationParent: "~/ao/projects" })).resolves.toEqual({ path: "/h/x", preparationId: "p1" });
		await cleanupClone(cfg, { path: "/h/x", preparationId: "p1" });
		expect(vi.mocked(apiRequest).mock.calls.map(([, path]) => path)).toEqual(["/api/v1/projects/clone/prepare", "/api/v1/projects/clone/cleanup"]);
	});

	it("asks the machine's gh for owners and free names", async () => {
		vi.mocked(apiRequest).mockImplementation(async (_cfg, path) => json(String(path).startsWith("/api/v1/imports/github-owners")
			? { owners: [{ login: "octo", avatarUrl: "a" }, { avatarUrl: "b" }] }
			: { available: false, message: "taken" }));
		await expect(listGitHubOwners(cfg)).resolves.toEqual([{ login: "octo", avatarUrl: "a" }]);
		await expect(checkGitHubAvailability(cfg, "octo", "my app")).resolves.toEqual({ available: false, message: "taken" });
		expect(vi.mocked(apiRequest).mock.calls[1]?.[1]).toBe("/api/v1/imports/github-availability?owner=octo&name=my%20app");
	});
});
