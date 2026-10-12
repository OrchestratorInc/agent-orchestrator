import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import type { Host } from "./hosts";
import { connectedSpawnHosts } from "./hostRoute";
import { emptyHostSnapshot, useOtherHosts, type HostSnapshot, type VerifiedHostSnapshot } from "./otherHosts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("./api", () => ({
	ApiError: class ApiError extends Error { constructor(public status: number) { super(String(status)); } },
	getSessions: vi.fn(),
	getNotifications: vi.fn(),
}));
vi.mock("./connectRuntime", () => ({ connectToHost: vi.fn() }));
vi.mock("./hosts", () => ({ findHost: vi.fn() }));
vi.mock("./config", () => ({ isConfigured: (config: ServerConfig) => Boolean(config.host) }));
vi.mock("./pollInterval", () => ({ pollIntervalFor: () => 60_000 }));

type HandoffResult = ReturnType<typeof useOtherHosts> & {
	claimPrimary?: (hostId: string) => VerifiedHostSnapshot | null;
	adoptPrimary?: (snapshot: HostSnapshot) => boolean;
};

const hostA = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "a", lastConnected: 0 } satisfies Host;
const hostB = { id: "b", name: "B", platform: "linux", endpoints: [], token: "b", lastConnected: 0 } satisfies Host;
const hostC = { id: "c", name: "C", platform: "linux", endpoints: [], token: "c", lastConnected: 0 } satisfies Host;
const hosts = [hostA, hostB, hostC];
const configA = { hostId: "a", host: "a.test", httpPort: "3011", muxPort: "14801", password: "a" } satisfies ServerConfig;
const configB = { hostId: "b", host: "b.test", httpPort: "3011", muxPort: "14801", password: "b" } satisfies ServerConfig;
const configC = { hostId: "c", host: "c.test", httpPort: "3011", muxPort: "14801", password: "c" } satisfies ServerConfig;
const configs = { a: configA, b: configB, c: configC };

let current!: HandoffResult;

function Harness(props: { selectedHostId: string; appActive: boolean; hosts?: Host[] }) {
	current = useOtherHosts(props.hosts ?? hosts, props.selectedHostId, props.appActive);
	return null;
}

function openSnapshot(host: Host, config: ServerConfig): VerifiedHostSnapshot {
	return { ...emptyHostSnapshot(host), config, connection: "open", loading: false };
}

function callsFor(mock: ReturnType<typeof vi.fn>, hostId: string): number {
	return mock.mock.calls.filter(([requested]) => requested === hostId).length;
}

async function renderHarness(selectedHostId = "a") {
	let renderer!: ReactTestRenderer;
	await act(async () => {
		renderer = create(<Harness selectedHostId={selectedHostId} appActive />);
		await Promise.resolve();
		await Promise.resolve();
	});
	return renderer;
}

describe("other host ownership handoff", () => {
	beforeEach(async () => {
		vi.clearAllMocks();
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions, getNotifications } = await import("./api");
		vi.mocked(connectToHost).mockImplementation(async (hostId) => {
			const config = configs[hostId as keyof typeof configs];
			return { ok: true, hostId, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config };
		});
		vi.mocked(getSessions).mockResolvedValue({ projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} });
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
	});

	afterEach(() => vi.useRealTimers());

	it.each(["endpoint", "token"] as const)("re-verifies only the secondary host whose %s changes", async (field) => {
		const { connectToHost } = await import("./connectRuntime");
		const renderer = await renderHarness();
		try {
			const updatedHost = field === "token" ? { ...hostB, token: "rotated" } : {
				...hostB, endpoints: [{ kind: "lan" as const, host: "new-b.test", port: 3011, secure: false }],
			};
			const updatedConfig = { ...configB, host: field === "endpoint" ? "new-b.test" : configB.host, password: updatedHost.token };
			vi.mocked(connectToHost).mockImplementation(async (hostId) => ({
				ok: true, hostId, endpoint: { kind: "lan", host: updatedConfig.host, port: 3011, secure: false },
				config: hostId === "b" ? updatedConfig : configC,
			}));
			await act(async () => { renderer.update(<Harness selectedHostId="a" appActive hosts={[hostA, updatedHost, hostC]} />); });
			expect(current.configForHost("b")).toEqual(updatedConfig);
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.configForHost("c")).toBe(configC);
			expect(callsFor(vi.mocked(connectToHost), "b")).toBe(2);
			expect(callsFor(vi.mocked(connectToHost), "c")).toBe(1);
		} finally { act(() => renderer.unmount()); }
	});

	it("keeps verified runners when only a name, appearance, or recency changes", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const renderer = await renderHarness();
		try {
			const renamed = { ...hostB, name: "Studio", appearance: { symbol: "laptop" as const }, lastConnected: 100 };
			await act(async () => { renderer.update(<Harness selectedHostId="a" appActive hosts={[hostA, renamed, hostC]} />); });
			expect(current.snapshots.b.name).toBe("Studio");
			expect(current.configForHost("b")).toBe(configB);
			expect(callsFor(vi.mocked(connectToHost), "b")).toBe(1);
		} finally { act(() => renderer.unmount()); }
	});

	it("recovers an auth-stopped secondary runner when its saved token is corrected", async () => {
		const { getSessions, ApiError } = await import("./api");
		const { connectToHost } = await import("./connectRuntime");
		const renderer = await renderHarness();
		try {
			vi.mocked(getSessions).mockRejectedValueOnce(new ApiError(401, "Unauthorized"));
			await act(async () => { await current.refreshHost("b"); });
			expect(current.configForHost("b")).toBeNull();
			const corrected = { ...configB, password: "corrected" };
			vi.mocked(connectToHost).mockResolvedValueOnce({ ok: true, hostId: "b", endpoint: { kind: "lan", host: configB.host, port: 3011, secure: false }, config: corrected });
			await act(async () => { renderer.update(<Harness selectedHostId="a" appActive hosts={[hostA, { ...hostB, token: "corrected" }, hostC]} />); });
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.configForHost("b")).toBe(corrected);
			expect(current.configForHost("c")).toBe(configC);
		} finally { act(() => renderer.unmount()); }
	});

	it("shares untouched worker rows across repeated polls and notification-only updates", async () => {
		const { getSessions, getNotifications } = await import("./api");
		const rows = ["First", "Second"].map((displayName, index) => ({
			id: String(index), displayName, projectId: "project", status: "idle", mode: "chat" as const,
			branch: null, issueId: null, issueTitle: null, userPrompt: null, summary: null,
			createdAt: "2026-10-09T00:00:00Z", lastActivityAt: "2026-10-09T00:00:00Z",
		}));
		vi.mocked(getSessions).mockImplementation(async () => ({ projects: [], sessions: structuredClone(rows), orchestrators: [], orchestratorId: null, stats: {} }));
		const renderer = await renderHarness();
		try {
			const first = current.snapshots.b.sessions;
			const otherHost = current.snapshots.c.sessions;
			vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 3, nextCursor: undefined });
			await act(async () => { await current.refreshHost("b"); });
			expect(current.snapshots.b.notificationsUnread).toBe(3);
			expect(current.snapshots.b.sessions).toBe(first);
			rows[1].displayName = "Changed";
			await act(async () => { await current.refreshHost("b"); });
			expect(current.snapshots.b.sessions[0]).toBe(first[0]);
			expect(current.snapshots.b.sessions[1].displayName).toBe("Changed");
			expect(current.snapshots.c.sessions).toBe(otherHost);
		} finally { act(() => renderer.unmount()); }
	});

	it("keeps A, B, and C open through an immediate A to B to A switch", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const renderer = await renderHarness();
		try {
			expect(current.claimPrimary).toBeTypeOf("function");
			expect(current.adoptPrimary).toBeTypeOf("function");
			if (!current.claimPrimary || !current.adoptPrimary) return;

			const claimed: { b: VerifiedHostSnapshot | null } = { b: null };
			await act(async () => {
				claimed.b = current.claimPrimary!("b");
				expect(current.adoptPrimary!(openSnapshot(hostA, configA))).toBe(true);
				renderer.update(<Harness selectedHostId="b" appActive />);
				await Promise.resolve();
			});
			expect(claimed.b?.config).toBe(configB);
			expect(current.snapshots.a.connection).toBe("open");
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.snapshots.c.connection).toBe("open");

			await act(async () => {
				const incomingA = current.claimPrimary!("a");
				expect(incomingA?.config).toBe(configA);
				expect(current.adoptPrimary!(claimed.b!)).toBe(true);
				renderer.update(<Harness selectedHostId="a" appActive />);
				await Promise.resolve();
			});

			expect(current.snapshots.a.connection).toBe("open");
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.snapshots.c.connection).toBe("open");
			expect(callsFor(vi.mocked(connectToHost), "a")).toBe(0);
			expect(callsFor(vi.mocked(connectToHost), "b")).toBe(1);
			expect(callsFor(vi.mocked(connectToHost), "c")).toBe(1);
		} finally {
			act(() => renderer.unmount());
		}
	});

	it("drops verified background credentials while inactive and re-verifies on resume", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const renderer = await renderHarness();
		try {
			expect(current.snapshots.b.config).toBe(configB);
			expect(current.snapshots.c.config).toBe(configC);

			await act(async () => {
				renderer.update(<Harness selectedHostId="a" appActive={false} />);
				await Promise.resolve();
			});
			expect(current.snapshots.b).toMatchObject({ config: null, connection: "closed", loading: false });
			expect(current.snapshots.c).toMatchObject({ config: null, connection: "closed", loading: false });

			await act(async () => {
				renderer.update(<Harness selectedHostId="a" appActive />);
				await Promise.resolve();
				await Promise.resolve();
			});
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.snapshots.c.connection).toBe("open");
			expect(callsFor(vi.mocked(connectToHost), "b")).toBe(2);
			expect(callsFor(vi.mocked(connectToHost), "c")).toBe(2);
		} finally {
			act(() => renderer.unmount());
		}
	});

	it("removes only an adopted host that genuinely fails its poll", async () => {
		const { getSessions } = await import("./api");
		const renderer = await renderHarness();
		try {
			expect(current.claimPrimary).toBeTypeOf("function");
			expect(current.adoptPrimary).toBeTypeOf("function");
			if (!current.claimPrimary || !current.adoptPrimary) return;

			vi.mocked(getSessions).mockImplementation(async (candidate) => {
				if (candidate.hostId === "a") throw new Error("network lost");
				return { projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} };
			});
			await act(async () => {
				const incomingB = current.claimPrimary!("b");
				expect(incomingB?.config).toBe(configB);
				expect(current.adoptPrimary!(openSnapshot(hostA, configA))).toBe(true);
				renderer.update(<Harness selectedHostId="b" appActive />);
				await Promise.resolve();
				await Promise.resolve();
			});

			expect(current.snapshots.a).toMatchObject({ connection: "closed", config: null });
			expect(current.snapshots.b.connection).toBe("open");
			expect(current.snapshots.c.connection).toBe("open");
			expect(connectedSpawnHosts(Object.values(current.snapshots)).map((item) => item.id)).toEqual(["b", "c"]);
		} finally {
			act(() => renderer.unmount());
		}
	});
});
