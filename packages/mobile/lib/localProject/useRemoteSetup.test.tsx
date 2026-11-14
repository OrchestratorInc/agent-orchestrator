import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useRemoteSetup, type RemoteSetup } from "./useRemoteSetup";

const fixtures = vi.hoisted(() => ({
	owners: [] as { login: string; avatarUrl: string }[],
	taken: new Set<string>(),
	listGitHubOwners: vi.fn(),
	checkGitHubAvailability: vi.fn(),
}));
vi.mock("./api", () => ({ listGitHubOwners: fixtures.listGitHubOwners, checkGitHubAvailability: fixtures.checkGitHubAvailability }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn() } }));

const config = { hostId: "mac", host: "h", httpPort: "1", password: "x" } as never;
let renderer: ReactTestRenderer | undefined;
const current: { remote?: RemoteSetup } = {};

function Probe({ active = true, suggestedName = "notes app" }: { active?: boolean; suggestedName?: string }) {
	current.remote = useRemoteSetup({ config, hostId: "mac", active, suggestedName });
	return null;
}

const remote = () => current.remote!;

async function settle() {
	await act(async () => { await vi.advanceTimersByTimeAsync(400); });
	for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
}

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.owners = [{ login: "octo", avatarUrl: "a" }, { login: "acme", avatarUrl: "b" }];
	fixtures.taken = new Set(["octo/taken"]);
	fixtures.listGitHubOwners.mockReset().mockImplementation(async () => fixtures.owners);
	fixtures.checkGitHubAvailability.mockReset().mockImplementation(async (_cfg: unknown, owner: string, name: string) =>
		fixtures.taken.has(`${owner}/${name}`) ? { available: false, message: "Repository name is already in use for this owner." } : { available: true });
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("useRemoteSetup", () => {
	it("creates a private GitHub repository under the first owner, named after the folder, once the name is free", async () => {
		await act(async () => { renderer = create(<Probe />); });
		await settle();
		expect(remote().owner).toBe("octo");
		expect(remote().name).toBe("notes-app");
		expect(remote().availability).toEqual({ state: "available" });
		expect(remote().ready).toBe(true);
		expect(remote().request).toEqual({ remoteUrl: "https://github.com/octo/notes-app.git", githubRepository: { owner: "octo", name: "notes-app", private: true } });
	});

	it("isn't ready while the name is taken", async () => {
		await act(async () => { renderer = create(<Probe />); });
		await settle();
		await act(async () => remote().setName("taken"));
		await settle();
		expect(remote().availability).toEqual({ state: "unavailable", message: "Repository name is already in use for this owner." });
		expect(remote().ready).toBe(false);
	});

	it("falls back to a URL when gh on the machine is signed out", async () => {
		fixtures.owners = [];
		await act(async () => { renderer = create(<Probe />); });
		await settle();
		expect(remote().mode).toBe("url");
		expect(remote().ready).toBe(false);
		await act(async () => remote().setUrl("git@github.com:octo/notes.git"));
		expect(remote().ready).toBe(true);
		expect(remote().request).toEqual({ remoteUrl: "git@github.com:octo/notes.git" });
		await act(async () => remote().setUrl("not a url"));
		expect(remote().urlInvalid).toBe(true);
	});

	it("asks the machine nothing while the step isn't shown", async () => {
		await act(async () => { renderer = create(<Probe active={false} />); });
		await settle();
		expect(fixtures.listGitHubOwners).not.toHaveBeenCalled();
		expect(fixtures.checkGitHubAvailability).not.toHaveBeenCalled();
	});
});
