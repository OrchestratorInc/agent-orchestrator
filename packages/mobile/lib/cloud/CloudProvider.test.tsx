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
