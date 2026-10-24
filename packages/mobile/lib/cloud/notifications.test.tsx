import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	listeners: new Set<(state: string) => void>(),
	appState: "active",
	auth: {} as Record<string, unknown>,
	storage: new Map<string, string>(),
}));
vi.mock("react-native", () => ({ AppState: {
	get currentState() { return fixtures.appState; },
	addEventListener: (_name: string, listener: (state: string) => void) => {
		fixtures.listeners.add(listener);
		return { remove: () => { fixtures.listeners.delete(listener); } };
	},
} }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => fixtures.storage.get(key) ?? null,
	setItem: async (key: string, value: string) => { fixtures.storage.set(key, value); },
} }));
vi.mock("./authStore", () => ({ useCloudAuth: () => fixtures.auth }));

import { CloudNotificationsProvider, useCloudNotifications } from "./notifications";
import type { CloudNotification } from "@aoagents/cloud-client";

const notification: CloudNotification = { id: "notice", source: "cloud", orgId: "org", recipientUserId: "alice", type: "needs_input", title: "Waiting", body: "", status: "unread", metadata: {}, createdAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z" };
let result: ReturnType<typeof useCloudNotifications>;
let second: ReturnType<typeof useCloudNotifications>;
let renderer: ReactTestRenderer | undefined;
function Consumer({ another = false }: { another?: boolean }) {
	const value = useCloudNotifications();
	if (another) second = value; else result = value;
	return null;
}
const tree = () => <CloudNotificationsProvider><Consumer /><Consumer another /></CloudNotificationsProvider>;
async function mount() { await act(async () => { renderer = create(tree()); await vi.advanceTimersByTimeAsync(0); }); }
async function state(next: string) {
	fixtures.appState = next;
	await act(async () => { for (const listener of fixtures.listeners) listener(next); await vi.advanceTimersByTimeAsync(0); });
}
beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => { if (!String(args[0]).startsWith("react-test-renderer is deprecated")) error(...args); });
	fixtures.storage.clear(); fixtures.appState = "active";
	fixtures.auth = { signedIn: true, accountId: "alice", orgId: "org", baseUrl: "https://cloud.test/", sessionEpoch: 1, orgLoading: false, orgError: null, busy: false, isCurrentSession: () => true,
		client: {
			listNotifications: vi.fn(async () => ({ items: [notification], unreadCount: 4, page: { hasMore: false }, latestSequence: 500 })),
			listNotificationEvents: vi.fn(async () => ({ items: [], hasMore: false })),
			markNotificationRead: vi.fn(async () => ({ updated: 1 })), markAllNotificationsRead: vi.fn(async () => ({ updated: 1 })),
		},
	};
});
afterEach(async () => { if (renderer) await act(async () => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("Cloud notification provider", () => {
	it("shares one foreground runtime across inbox and badge consumers", async () => {
		const client = fixtures.auth.client as { listNotifications: ReturnType<typeof vi.fn>; listNotificationEvents: ReturnType<typeof vi.fn> };
		await mount();
		expect(result).toMatchObject({ available: true, source: { kind: "cloud", id: "org" }, scopeKey: '["https://cloud.test","alice","org"]', ownerKey: '["https://cloud.test","alice","org",1]', unreadCount: 4 });
		expect(second).toBe(result);
		expect(client.listNotifications).toHaveBeenCalledTimes(1);
		expect(client.listNotificationEvents).toHaveBeenCalledTimes(1);
		await state("background");
		await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
		expect(client.listNotificationEvents).toHaveBeenCalledTimes(1);
		await state("active");
		expect(client.listNotifications).toHaveBeenCalledTimes(2);
		expect(client.listNotificationEvents).toHaveBeenCalledTimes(2);
	});
	it("keeps the inbox available during organization resolution without starting requests", async () => {
		fixtures.auth.orgId = null; fixtures.auth.orgLoading = true;
		await mount();
		expect(result).toMatchObject({ available: true, source: null, scopeKey: null, items: [], unreadCount: 0, loading: true });
		expect((fixtures.auth.client as { listNotifications: ReturnType<typeof vi.fn> }).listNotifications).not.toHaveBeenCalled();
	});
	it("drops old rows and counts synchronously when the signed-in owner changes", async () => {
		await mount();
		expect(result.items).toHaveLength(1);
		fixtures.auth = { ...fixtures.auth, accountId: "bob", sessionEpoch: 2, orgId: null, orgLoading: true };
		await act(async () => renderer?.update(tree()));
		expect(result).toMatchObject({ items: [], unreadCount: 0, source: null, scopeKey: null });
		fixtures.auth = { ...fixtures.auth, signedIn: false, orgLoading: false };
		await act(async () => renderer?.update(tree()));
		expect(result).toMatchObject({ available: false, items: [], unreadCount: 0, loading: false });
		expect(fixtures.listeners.size).toBe(0);
	});
	it("fences callbacks from a previous login while keeping persisted scope stable", async () => {
		await mount();
		const previous = result;
		fixtures.auth = { ...fixtures.auth, sessionEpoch: 2 };
		await act(async () => { renderer?.update(tree()); await vi.advanceTimersByTimeAsync(0); });
		expect(result.scopeKey).toBe(previous.scopeKey);
		expect(result.ownerKey).not.toBe(previous.ownerKey);
		await expect(previous.clearOne(notification)).rejects.toThrow();
	});
});
