import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudError } from "./errors";
import type { CloudGitHubInstallation, CloudGitHubRepository, CloudProviderConnection } from "./types";
import { useNewCloudProject, type NewCloudProject } from "./useNewCloudProject";

const fixtures = vi.hoisted(() => ({
	cloud: {} as Record<string, unknown>,
	keys: 0,
	installGitHubApp: vi.fn(async (..._args: unknown[]) => undefined as unknown),
}));
vi.mock("./CloudProvider", () => ({ useCloud: () => fixtures.cloud }));
vi.mock("expo-crypto", () => ({ randomUUID: () => `key-${++fixtures.keys}` }));
vi.mock("./githubInstall", () => ({ installGitHubApp: fixtures.installGitHubApp }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), success: vi.fn(), error: vi.fn(), tap: vi.fn() } }));

function repo(fullName: string, extra: Partial<CloudGitHubRepository> = {}): CloudGitHubRepository {
	return {
		githubRepositoryId: `id-${fullName}`,
		name: fullName.split("/")[1] ?? fullName,
		fullName,
		htmlUrl: `https://github.com/${fullName}`,
		defaultBranch: "main",
		visibility: "private",
		isPrivate: true,
		isArchived: false,
		access: "admin",
		grantedAt: "2026-01-01T00:00:00Z",
		...extra,
	};
}

const activeInstallation: CloudGitHubInstallation = {
	id: "i1",
	githubInstallationId: "gh-1",
	accountLogin: "acme",
	accountType: "Organization",
	status: "active",
	repositorySelection: "selected",
	syncStatus: "synced",
	createdAt: "2026-01-01T00:00:00Z",
	updatedAt: "2026-01-01T00:00:00Z",
};

function connection(provider: string): CloudProviderConnection {
	return { id: provider, provider, label: "default", config: {}, validationState: "valid", createdAt: "", updatedAt: "" };
}

function cloudFixture() {
	const client = {
		listGitHubInstallations: vi.fn(async () => ({ installations: [activeInstallation] })),
		listGitHubRepositories: vi.fn(async () => ({ items: [repo("acme/api"), repo("acme/web"), repo("acme/old", { isArchived: true })], page: { hasMore: false } })),
		syncGitHubInstallation: vi.fn(async () => ({ installation: activeInstallation })),
		startGitHubInstallation: vi.fn(async () => ({ installationUrl: "https://github.com/apps/orchestrator/installations/new" })),
		createGitHubProject: vi.fn(async (..._args: unknown[]) => ({ project: { id: "p-new" } })),
		createStandaloneProject: vi.fn(async (..._args: unknown[]) => ({ project: { id: "p-solo" } })),
	};
	return {
		status: "ready",
		org: { id: "org-1", displayName: "Acme" },
		client,
		projects: [] as { id: string; githubRepositoryId?: string }[],
		connections: [connection("claude-code"), connection("codex")],
		refreshProjects: vi.fn(async () => {}),
	};
}

let renderer: ReactTestRenderer | undefined;
let cloud: ReturnType<typeof cloudFixture>;
const onCreated = vi.fn();
const current: { project?: NewCloudProject } = {};

function Probe() {
	current.project = useNewCloudProject({ onCreated });
	return null;
}

function project(): NewCloudProject {
	if (!current.project) throw new Error("not rendered");
	return current.project;
}

async function flush() {
	for (let step = 0; step < 6; step++) await act(async () => { await Promise.resolve(); });
}

async function mount() {
	await act(async () => { renderer = create(<Probe />); });
	await flush();
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.keys = 0;
	fixtures.installGitHubApp.mockReset();
	cloud = cloudFixture();
	fixtures.cloud = cloud;
	onCreated.mockReset();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	current.project = undefined;
});

describe("useNewCloudProject", () => {
	it("searches the reachable repositories only once something is typed, and creates the chosen one", async () => {
		await mount();
		expect(project().hasInstallation).toBe(true);
		expect(project().repos?.map((item) => item.fullName)).toEqual(["acme/api", "acme/web"]);
		expect(project().matches).toEqual([]);
		expect(project().canCreate).toBe(false);

		await act(async () => project().setQuery("WEB"));
		expect(project().searching).toBe(true);
		expect(project().matches.map((item) => item.fullName)).toEqual(["acme/web"]);

		await act(async () => project().chooseRepo("id-acme/web"));
		expect(project().repo?.fullName).toBe("acme/web");
		expect(project().query).toBe("");
		expect(project().canCreate).toBe(true);

		await act(async () => { await project().create(); });
		expect(cloud.client.createGitHubProject).toHaveBeenCalledWith(
			"org-1",
			{ githubRepositoryId: "id-acme/web", config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } } },
			"key-1",
		);
		expect(cloud.refreshProjects).toHaveBeenCalled();
		expect(onCreated).toHaveBeenCalledWith("p-new");
	});

	// githubInstall.test.ts covers the browser and the polling.
	it("connects GitHub through the signed-in browser flow, then lists what it can reach", async () => {
		await mount();
		const loadsBefore = cloud.client.listGitHubRepositories.mock.calls.length;
		await act(async () => { await project().connectGitHub(); });
		expect(fixtures.installGitHubApp).toHaveBeenCalledWith(cloud.client, "org-1", [activeInstallation], expect.any(Function));
		expect(cloud.client.listGitHubRepositories.mock.calls.length).toBeGreaterThan(loadsBefore);
		expect(project().connecting).toBe(false);
		expect(project().error).toBeNull();
	});

	it("shows why connecting GitHub failed", async () => {
		await mount();
		fixtures.installGitHubApp.mockRejectedValueOnce(new CloudError("GitHub is unreachable.", { status: 500 }));
		await act(async () => { await project().connectGitHub(); });
		expect(project().error).toBe("GitHub is unreachable.");
		expect(project().connecting).toBe(false);
	});

	it("goes back to searching on Change and focuses the search", async () => {
		await mount();
		await act(async () => project().chooseRepo("id-acme/api"));
		await act(async () => project().changeRepo());
		expect(project().repo).toBeUndefined();
		expect(project().reselecting).toBe(true);
		expect(project().canCreate).toBe(false);
	});

	it("retries a failed create with the same idempotency key", async () => {
		await mount();
		cloud.client.createGitHubProject.mockRejectedValueOnce(new CloudError("Boom", { status: 500 }));
		await act(async () => project().chooseRepo("id-acme/api"));
		await act(async () => { await project().create(); });
		expect(project().error).toBeTruthy();
		expect(onCreated).not.toHaveBeenCalled();

		await act(async () => { await project().create(); });
		const keys = cloud.client.createGitHubProject.mock.calls.map((call) => call[2]);
		expect(keys).toEqual(["key-1", "key-1"]);
		expect(onCreated).toHaveBeenCalledWith("p-new");
	});

	it("opens the project that already holds the repository", async () => {
		cloud.projects = [{ id: "p-existing", githubRepositoryId: "id-acme/api" }];
		cloud.client.createGitHubProject.mockRejectedValueOnce(new CloudError("Exists", { status: 409, code: "project_repository_exists" }));
		await mount();
		await act(async () => project().chooseRepo("id-acme/api"));
		await act(async () => { await project().create(); });
		expect(onCreated).toHaveBeenCalledWith("p-existing");
		expect(project().error).toBeNull();
	});

	it("falls back to a standalone project, which needs a name, where the deployment has no GitHub App", async () => {
		cloud.client.listGitHubInstallations.mockRejectedValueOnce(new CloudError("Not found", { status: 404 }));
		await mount();
		expect(project().githubUnavailable).toBe(true);
		expect(project().source).toBe("standalone");
		expect(project().canCreate).toBe(false);

		await act(async () => project().setName("  Notes  "));
		expect(project().canCreate).toBe(true);
		await act(async () => { await project().create(); });
		expect(cloud.client.createStandaloneProject).toHaveBeenCalledWith(
			"org-1",
			{ displayName: "Notes", config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } } },
			"key-1",
		);
		expect(onCreated).toHaveBeenCalledWith("p-solo");
	});

	it("offers to try again when the repositories fail to load", async () => {
		cloud.client.listGitHubInstallations.mockRejectedValueOnce(new CloudError("Down", { status: 503 }));
		await mount();
		expect(project().reposFailed).toBe(true);
		expect(project().error).toBeTruthy();
		expect(project().githubUnavailable).toBe(false);

		await act(async () => project().retryRepos());
		await flush();
		expect(project().reposFailed).toBe(false);
		expect(project().error).toBeNull();
		expect(project().repos).toHaveLength(2);
	});

	it("leaves the agents unset when none is connected", async () => {
		cloud.connections = [];
		await mount();
		expect(project().available).toEqual([]);
		expect(project().workerAgent).toBeUndefined();
		expect(project().orchestratorAgent).toBeUndefined();
	});
});
