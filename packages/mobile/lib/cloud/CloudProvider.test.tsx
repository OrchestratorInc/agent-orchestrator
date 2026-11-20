import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { CloudMe, CloudOrganization, CloudProject } from "./types";

type Fn = Mock<(...args: any[]) => any>;
const fixtures = vi.hoisted(() => ({
	client: {} as Record<string, Fn>,
	options: undefined as { onForbidden?: () => void } | undefined,
	unregister: undefined as unknown as Fn,
	register: undefined as unknown as Fn,
	forgetDrafts: undefined as unknown as Fn,
	accountChanged: undefined as undefined | ((account: { id: string; email: string } | null) => void),
	signOutOfAccount: undefined as unknown as Fn,
}));

vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));
vi.mock("expo-crypto", () => ({ randomUUID: () => "uuid" }));
vi.mock("expo/fetch", () => ({ fetch: () => undefined }));
vi.mock("expo-router", () => ({ router: { push: () => undefined } }));
vi.mock("../account", () => ({
	getAccountAccessToken: async () => "token",
	loadAccount: async () => ({ id: "u1", email: "u@example.com" }),
	onAccountChange: (listener: (account: { id: string; email: string } | null) => void) => {
		fixtures.accountChanged = listener;
		return () => {};
	},
	signInToAccount: async () => null,
	signOutOfAccount: (...args: unknown[]) => fixtures.signOutOfAccount(...args),
}));
vi.mock("../accountHosts", () => ({ clearAccountHosts: async () => {}, syncAccountHosts: async () => {} }));
vi.mock("../appStatePoll", () => ({ shouldPoll: () => false }));
vi.mock("./drafts", () => ({ forgetCloudDrafts: (...args: unknown[]) => fixtures.forgetDrafts(...args) }));
vi.mock("./push", () => ({
	registerCloudPush: (...args: unknown[]) => fixtures.register(...args),
	unregisterCloudPush: (...args: unknown[]) => fixtures.unregister(...args),
}));
vi.mock("./client", async (actual) => ({
	...(await actual<typeof import("./client")>()),
	createCloudClient: (options: { onForbidden?: () => void }) => {
		fixtures.options = options;
		return fixtures.client;
	},
}));

const { CloudProvider, CloudPushRemovalError, useCloud } = await import("./CloudProvider");
type CloudState = ReturnType<typeof useCloud>;

const org = (id: string): CloudOrganization => ({ id, slug: id, displayName: id, role: "owner" });
const me = (orgId: string, extra: Partial<CloudMe> = {}): CloudMe => ({ user: { id: "u1", email: "u@example.com", displayName: "U" }, organizations: [org(orgId)], activeOrganizationId: orgId, chatHarnesses: ["claude-code"], ...extra });
const project = (id: string, orgId: string) => ({ id, orgId, displayName: id }) as unknown as CloudProject;
const page = <T,>(items: T[]) => ({ items, page: { hasMore: false } });
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); };

let renderer: ReactTestRenderer | undefined;
let latest: CloudState | undefined;
function Probe() {
	latest = useCloud();
	return null;
}
async function mount() {
	await act(async () => { renderer = create(createElement(CloudProvider, null, createElement(Probe))); await flush(); });
}
const reResolve = () => act(async () => { fixtures.options?.onForbidden?.(); await flush(); });

beforeEach(() => {
	fixtures.unregister = vi.fn(async () => true);
	fixtures.register = vi.fn(async () => ({ ok: true }));
	fixtures.forgetDrafts = vi.fn(async () => {});
	fixtures.signOutOfAccount = vi.fn(async () => {});
	fixtures.client = {
		getMe: vi.fn(async () => me("a")),
		listProjects: vi.fn(async (orgId: string) => page([project(`${orgId}-p`, orgId)])),
		listSessions: vi.fn(async () => page([])),
		listProviderConnections: vi.fn(async () => ({ providerConnections: [] })),
		listNotifications: vi.fn(async () => ({ items: [], unreadCount: 0 })),
		createOrganization: vi.fn(),
	};
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	latest = undefined;
});

describe("CloudProvider", () => {
	it("drops the old organization's projects, and its loads still out, when it resolves to another", async () => {
		await mount();
		expect(latest).toMatchObject({ status: "ready", org: { id: "a" }, projects: [{ id: "a-p" }] });

		// A refresh of "a"'s projects is still out when a 403 re-resolves to "b".
		let finishOld!: (value: unknown) => void;
		fixtures.client.listProjects.mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }));
		const stale = latest!.refreshProjects();
		fixtures.client.getMe.mockImplementation(async () => me("b"));
		await reResolve();
		expect(latest).toMatchObject({ org: { id: "b" }, projects: [{ id: "b-p" }] });

		await act(async () => { finishOld(page([project("a-p", "a")])); await stale; await flush(); });
		expect(latest?.projects).toEqual([project("b-p", "b")]);
	});

	it("reads the projects and the caller's standalone agents apart, and keeps each project once", async () => {
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		fixtures.client.listProjects.mockImplementation(async (orgId: string, list: { standaloneAgents?: string } = {}) =>
			list.standaloneAgents === "only" ? page([agent]) : page([project(`${orgId}-p`, orgId)]));
		await mount();
		expect(fixtures.client.listProjects.mock.calls.map((call) => call[1]?.standaloneAgents)).toEqual(["exclude", "only"]);
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);

		// A control plane without agents ignores the filter and lists every project in both reads.
		fixtures.client.listProjects.mockImplementation(async (orgId: string) => page([project(`${orgId}-p`, orgId)]));
		await act(async () => { await latest!.refreshProjects(); await flush(); });
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p"]);
	});

	it("lists a project the phone just created before the next read", async () => {
		await mount();
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		act(() => latest!.upsertProject(agent));
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);
		act(() => latest!.upsertProject({ ...agent, displayName: "renamed" }));
		expect(latest?.projects).toHaveLength(2);
		expect(latest?.projects[1].displayName).toBe("renamed");
	});

	it("keeps a project put in the list during a read that started before it, and lets a later read drop it", async () => {
		await mount();
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		// Both halves of a refresh are out when the agent's project goes in.
		const finish: Array<(value: unknown) => void> = [];
		fixtures.client.listProjects.mockImplementation(() => new Promise((resolve) => { finish.push(resolve); }));
		const stale = latest!.refreshProjects();
		act(() => latest!.upsertProject(agent));
		await act(async () => { finish.forEach((resolve) => resolve(page([project("a-p", "a")]))); await stale; await flush(); });
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);

		// A read that started after it answers for it: deleted meanwhile, it goes.
		fixtures.client.listProjects.mockImplementation(async (orgId: string) => page([project(`${orgId}-p`, orgId)]));
		await act(async () => { await latest!.refreshProjects(); await flush(); });
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p"]);
	});

	it("drops a project read that lands after a newer one, so it can't undo the newer read", async () => {
		await mount();
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		// Read A starts before the agent exists.
		const finishA: Array<(value: unknown) => void> = [];
		fixtures.client.listProjects.mockImplementation(() => new Promise((resolve) => { finishA.push(resolve); }));
		const readA = latest!.refreshProjects();
		act(() => latest!.upsertProject(agent));
		// Read B starts after it, lists it, and lands first.
		fixtures.client.listProjects.mockImplementation(async (orgId: string, list: { standaloneAgents?: string } = {}) =>
			list.standaloneAgents === "only" ? page([agent]) : page([project(`${orgId}-p`, orgId)]));
		await act(async () => { await latest!.refreshProjects(); await flush(); });
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);
		// Read A lands last, without the agent: it is out of date.
		await act(async () => { finishA.forEach((resolve) => resolve(page([project("a-p", "a")]))); await readA; await flush(); });
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);
	});

	it("reads the projects again when a polled session names one it hasn't read, once per project", async () => {
		await mount();
		const reads = () => fixtures.client.listProjects.mock.calls.length;
		const before = reads();
		// An agent started on another device: its session comes before its project.
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		fixtures.client.listProjects.mockImplementation(async (orgId: string, list: { standaloneAgents?: string } = {}) =>
			list.standaloneAgents === "only" ? page([agent]) : page([project(`${orgId}-p`, orgId)]));
		fixtures.client.listSessions.mockImplementation(async () => page([{ id: "s1", projectId: "a-agent" }]));
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		expect(reads()).toBe(before + 2);
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);

		// Known now: the next poll reads no projects.
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		expect(reads()).toBe(before + 2);

		// A project that never lists (a session shared from elsewhere) costs one read, not one per poll.
		fixtures.client.listSessions.mockImplementation(async () => page([{ id: "s1", projectId: "a-agent" }, { id: "s2", projectId: "elsewhere" }]));
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		expect(reads()).toBe(before + 4);
	});

	it("tries the project read again on a later poll when it failed", async () => {
		await mount();
		const reads = () => fixtures.client.listProjects.mock.calls.length;
		const before = reads();
		fixtures.client.listSessions.mockImplementation(async () => page([{ id: "s1", projectId: "a-agent" }]));
		fixtures.client.listProjects.mockImplementation(async () => { throw new Error("offline"); });
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		expect(reads()).toBe(before + 2);

		// Back online: the next poll reads the projects again, and finds the agent.
		const agent = { ...project("a-agent", "a"), standaloneAgent: true } as CloudProject;
		fixtures.client.listProjects.mockImplementation(async (orgId: string, list: { standaloneAgents?: string } = {}) =>
			list.standaloneAgents === "only" ? page([agent]) : page([project(`${orgId}-p`, orgId)]));
		await act(async () => { await latest!.refreshSessions(); await flush(); });
		expect(reads()).toBe(before + 4);
		expect(latest?.projects.map((item) => item.id)).toEqual(["a-p", "a-agent"]);
	});

	it("comes back to ready when retried from an error, though it kept its organization", async () => {
		await mount();
		fixtures.client.getMe.mockImplementation(async () => { throw new Error("offline"); });
		await reResolve();
		expect(latest).toMatchObject({ status: "error", org: { id: "a" } });

		fixtures.client.getMe.mockImplementation(async () => me("a"));
		await act(async () => { await latest!.refresh(); await flush(); });
		expect(latest?.status).toBe("ready");
	});

	it("stops Cloud pushing to this phone on sign-out even when no organization was resolved", async () => {
		fixtures.client.getMe.mockImplementation(async () => { throw new Error("offline"); });
		await mount();
		expect(latest).toMatchObject({ status: "error", org: null });
		await act(async () => { await latest!.signOut(); });
		expect(fixtures.unregister).toHaveBeenCalledOnce();
		expect(fixtures.signOutOfAccount).toHaveBeenCalledOnce();
	});

	it("stays signed in when the push registration couldn't be removed, unless told to sign out anyway", async () => {
		fixtures.unregister.mockImplementation(async () => false);
		await mount();
		await act(async () => { await expect(latest!.signOut()).rejects.toBeInstanceOf(CloudPushRemovalError); });
		expect(fixtures.signOutOfAccount).not.toHaveBeenCalled();
		await act(async () => { await latest!.signOut({ keepPush: true }); });
		expect(fixtures.signOutOfAccount).toHaveBeenCalledOnce();
	});

	// Connecting registers the phone without waiting for it. Were that request
	// to land after sign-out removed the registration, the phone would be
	// registered again and keep getting the old account's notifications.
	it("lets a registration still out land before sign-out removes it", async () => {
		const order: string[] = [];
		let land!: () => void;
		fixtures.register.mockImplementation(() => new Promise((resolve) => { land = () => { order.push("registered"); resolve({ ok: true }); }; }));
		fixtures.unregister.mockImplementation(async () => { order.push("removed"); return true; });
		await mount();
		const signingOut = latest!.signOut();
		await flush();
		expect(order).toEqual([]);
		land();
		await signingOut;
		expect(order).toEqual(["registered", "removed"]);
		expect(fixtures.signOutOfAccount).toHaveBeenCalledOnce();
	});

	// Unsent Cloud drafts outlive a restart, but not the account that wrote them.
	it("forgets Cloud drafts when the account signs out or changes, not when the app opens", async () => {
		await mount();
		expect(fixtures.forgetDrafts).not.toHaveBeenCalled();
		await act(async () => { fixtures.accountChanged?.({ id: "u1", email: "u@example.com" }); await flush(); });
		expect(fixtures.forgetDrafts).not.toHaveBeenCalled();
		await act(async () => { fixtures.accountChanged?.({ id: "u2", email: "v@example.com" }); await flush(); });
		expect(fixtures.forgetDrafts).toHaveBeenCalledTimes(1);
		await act(async () => { fixtures.accountChanged?.(null); await flush(); });
		expect(fixtures.forgetDrafts).toHaveBeenCalledTimes(2);
	});

	// The clearing runs in the background: the next account's Cloud screens,
	// which draw a composer only once an organization is resolved, must not
	// read a draft the last account left before it is gone.
	it("holds the next account's workspace until the last account's drafts are gone", async () => {
		await mount();
		let cleared!: () => void;
		fixtures.forgetDrafts.mockImplementation(() => new Promise<void>((resolve) => { cleared = resolve; }));
		await act(async () => { fixtures.accountChanged?.({ id: "u2", email: "v@example.com" }); await flush(); });
		// The switch clears the organization; Cloud's screens show nothing without one.
		expect(latest?.org).toBeNull();
		await act(async () => { cleared(); await flush(); });
		expect(latest).toMatchObject({ status: "ready", org: { id: "a" } });
	});

	it("offers no Chat agents when the control plane doesn't say which run in Chat", async () => {
		fixtures.client.getMe.mockImplementation(async () => me("a", { chatHarnesses: undefined }));
		await mount();
		expect(latest?.chatHarnesses).toEqual([]);
	});
});
