import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";
import type { ServerConfig } from "./config";
import type { DashboardSession } from "./api";
import type { HostSnapshot } from "./otherHosts";

vi.mock("./api", () => ({
	ApiError: class ApiError extends Error { constructor(public status: number) { super(String(status)); } },
	getSessions: vi.fn(),
	getNotifications: vi.fn(),
}));
vi.mock("./connectRuntime", () => ({ connectToHost: vi.fn() }));
vi.mock("./hosts", () => ({ findHost: vi.fn() }));
vi.mock("./config", () => ({ isConfigured: (config: ServerConfig) => Boolean(config.host) }));
vi.mock("./pollInterval", () => ({ pollIntervalFor: () => 8_000 }));

const host: Host = {
	id: "host-b", name: "Host B", platform: "linux", endpoints: [], token: "secret", lastConnected: 0,
};
const config: ServerConfig = {
	host: "host-b.test", hostId: host.id, httpPort: "3011", muxPort: "14801", password: "secret",
};

describe("another paired host's live runner", () => {
	beforeEach(() => vi.clearAllMocks());
	afterEach(() => vi.useRealTimers());

	it("polls and publishes its own verified sessions", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions, getNotifications } = await import("./api");
		vi.mocked(connectToHost).mockResolvedValue({ ok: true, hostId: host.id, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config });
		vi.mocked(getSessions).mockResolvedValue({
			projects: [{ id: "same-project", name: "Todo" }],
			sessions: [{ id: "same-session", projectId: "same-project", mode: "chat" } as DashboardSession],
			orchestrators: [], orchestratorId: null, stats: {},
		});
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 2, nextCursor: undefined });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(host);
		const runner = startOtherHost(host, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			expect(getSessions).toHaveBeenCalledWith(config, "all");
			expect(snapshot.connection).toBe("open");
			expect(snapshot.sessions[0].id).toBe("same-session");
			expect(snapshot.notificationsUnread).toBe(2);
		} finally { runner.stop(); }
	});

	// One unanswered poll on another machine used to read as "This machine is
	// offline" in its chat and project views. Its outcomes are tracked like the
	// selected machine's, so a blip reads as reconnecting and only a sustained
	// run of failed polls and races reads as unreachable.
	it("tracks poll and race outcomes so a blip is not an offline machine", async () => {
		vi.useFakeTimers();
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions, getNotifications } = await import("./api");
		const { classifyLink, UNKNOWN_PHONE_NETWORK } = await import("./linkHealth");
		vi.mocked(connectToHost).mockResolvedValueOnce({ ok: true, hostId: host.id, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config })
			.mockResolvedValue({ ok: false, reason: "unreachable" } as never);
		vi.mocked(getSessions).mockRejectedValue(new Error("network lost"));
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(host);
		const runner = startOtherHost(host, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		const health = () => classifyLink(snapshot.linkTracker!, UNKNOWN_PHONE_NETWORK, Date.now());
		try {
			await runner.refresh();
			expect(snapshot.connection).toBe("closed");
			expect(health()).toBe("reconnecting");
			await vi.advanceTimersByTimeAsync(2_000 + 15_000 + 15_000);
			expect(health()).toBe("unreachable");
		} finally { runner.stop(); }
	});

	it("never sends a bearer to a host with the wrong identity", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions } = await import("./api");
		vi.mocked(connectToHost).mockResolvedValue({ ok: true, hostId: "different-host", endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(host);
		const runner = startOtherHost(host, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			expect(runner.config).toBeNull();
			expect(snapshot.connection).toBe("closed");
			expect(getSessions).not.toHaveBeenCalled();
		} finally { runner.stop(); }
	});

	it.each([
		["network path fails", undefined],
		["saved address reports another host", 421],
	])("re-races and reconnects after a host's %s", async (_reason, status) => {
		vi.useFakeTimers();
		const { connectToHost } = await import("./connectRuntime");
		const { ApiError, getSessions, getNotifications } = await import("./api");
		vi.mocked(connectToHost).mockResolvedValue({ ok: true, hostId: host.id, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config });
		vi.mocked(getSessions)
			.mockRejectedValueOnce(status === undefined ? new Error("network lost") : new ApiError(status, "host mismatch"))
			.mockResolvedValue({ projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} });
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(host);
		const runner = startOtherHost(host, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			expect(snapshot.connection).toBe("closed");
			expect(snapshot.config).toBeNull();
			await vi.advanceTimersByTimeAsync(2_000);
			expect(connectToHost).toHaveBeenCalledTimes(2);
			expect(snapshot.connection).toBe("open");
		} finally { runner.stop(); }
	});

	it("keeps a reassigned address offline when no endpoint still belongs to the paired host", async () => {
		vi.useFakeTimers();
		const { connectToHost } = await import("./connectRuntime");
		const { ApiError, getSessions } = await import("./api");
		vi.mocked(connectToHost)
			.mockResolvedValueOnce({ ok: true, hostId: host.id, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config })
			.mockResolvedValueOnce({ ok: false, reason: "none-reachable" });
		vi.mocked(getSessions).mockRejectedValue(new ApiError(421, "host mismatch"));
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(host);
		const runner = startOtherHost(host, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			await vi.advanceTimersByTimeAsync(2_000);
			expect(connectToHost).toHaveBeenCalledTimes(2);
			expect(snapshot.hostId).toBe(host.id);
			expect(snapshot.connection).toBe("closed");
			expect(snapshot.config).toBeNull();
			expect(getSessions).toHaveBeenCalledTimes(1);
		} finally { runner.stop(); }
	});

	it("stops polling a host after a rejected credential", async () => {
		vi.useFakeTimers();
		const { connectToHost } = await import("./connectRuntime");
		const { findHost } = await import("./hosts");
		const { ApiError, getSessions } = await import("./api");
		const tunnel = { kind: "tunnel" as const, host: config.host, port: 3011, secure: false };
		const paired = { ...host, endpoints: [{ kind: "lan" as const, host: "lan.test", port: 3011, secure: false }, tunnel] };
		vi.mocked(findHost).mockResolvedValue(paired);
		vi.mocked(connectToHost).mockResolvedValue({ ok: true, hostId: host.id, endpoint: tunnel, config: { ...config, endpointKind: "tunnel" } });
		vi.mocked(getSessions).mockRejectedValue(new ApiError(401, "unauthorized"));
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(paired);
		const runner = startOtherHost(paired, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			await vi.advanceTimersByTimeAsync(60_000);
			expect(snapshot.errorStatus).toBe(401);
			expect(snapshot.config).toBeNull();
			expect(runner.config).toBeNull();
			expect(getSessions).toHaveBeenCalledTimes(1);
			expect(connectToHost).toHaveBeenCalledTimes(1);
		} finally { runner.stop(); }
	});

	it("hides a cached bearer until a returning host has passed identity verification", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions, getNotifications } = await import("./api");
		let finishConnect!: (result: Awaited<ReturnType<typeof connectToHost>>) => void;
		vi.mocked(connectToHost).mockImplementation(() => new Promise((resolve) => { finishConnect = resolve; }));
		vi.mocked(getSessions).mockResolvedValue({ projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} });
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot: HostSnapshot = { ...emptyHostSnapshot(host), config, connection: "open" };
		const runner = startOtherHost(host, snapshot, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			const pending = runner.refresh();
			expect(snapshot.config).toBeNull();
			finishConnect({ ok: true, hostId: host.id, endpoint: { kind: "lan", host: config.host, port: 3011, secure: false }, config });
			await pending;
			expect(snapshot.connection).toBe("open");
		} finally { runner.stop(); }
	});

	it("moves a nonselected host from tunnel back to LAN without interrupting a working poll", async () => {
		vi.useFakeTimers();
		vi.setSystemTime(1_000_000);
		const { connectToHost } = await import("./connectRuntime");
		const { findHost } = await import("./hosts");
		const { getSessions, getNotifications } = await import("./api");
		const tunnel = { kind: "tunnel" as const, host: "host-b.tunnel.test", port: 443, secure: true };
		const lan = { kind: "lan" as const, host: "host-b.lan.test", port: 3011, secure: false };
		const paired = { ...host, endpoints: [lan, tunnel] };
		const tunnelConfig = { ...config, host: tunnel.host, httpPort: "443", secure: true, endpointKind: "tunnel" as const };
		const lanConfig = { ...config, host: lan.host, endpointKind: "lan" as const };
		vi.mocked(findHost).mockResolvedValue(paired);
		vi.mocked(connectToHost)
			.mockResolvedValueOnce({ ok: true, hostId: host.id, endpoint: tunnel, config: tunnelConfig })
			.mockResolvedValueOnce({ ok: true, hostId: host.id, endpoint: lan, config: lanConfig });
		const answer = { projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} };
		let finishOldPoll!: (value: typeof answer) => void;
		vi.mocked(getSessions)
			.mockResolvedValueOnce(answer)
			.mockImplementationOnce(() => new Promise((resolve) => { finishOldPoll = resolve; }))
			.mockResolvedValue(answer);
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
		const { emptyHostSnapshot, startOtherHost } = await import("./otherHosts");
		let snapshot = emptyHostSnapshot(paired);
		const runner = startOtherHost(paired, undefined, (patch) => { snapshot = { ...snapshot, ...patch }; });
		try {
			await runner.refresh();
			expect(snapshot.config).toBe(tunnelConfig);
			await vi.advanceTimersByTimeAsync(60_001);
			expect(connectToHost).toHaveBeenCalledTimes(2);
			expect(snapshot.connection).toBe("connecting");
			finishOldPoll(answer);
			await vi.advanceTimersByTimeAsync(1);
			expect(snapshot.config).toBe(lanConfig);
			expect(snapshot.connection).toBe("open");
			expect(getSessions).toHaveBeenCalledWith(lanConfig, "all");
		} finally { runner.stop(); }
	});

	it("adopts a verified open primary without racing its identity again", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const { getSessions, getNotifications } = await import("./api");
		const otherHosts = await import("./otherHosts") as typeof import("./otherHosts") & {
			startVerifiedOtherHost?: typeof import("./otherHosts")["startOtherHost"];
		};
		expect(otherHosts.startVerifiedOtherHost).toBeTypeOf("function");
		if (!otherHosts.startVerifiedOtherHost) return;

		vi.mocked(getSessions).mockResolvedValue({ projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} });
		vi.mocked(getNotifications).mockResolvedValue({ notifications: [], unreadCount: 0, nextCursor: undefined });
		let snapshot: HostSnapshot = { ...otherHosts.emptyHostSnapshot(host), config, connection: "open", loading: false };
		const runner = otherHosts.startVerifiedOtherHost(host, snapshot, (patch) => { snapshot = { ...snapshot, ...patch }; });
		expect(runner).not.toBeNull();
		try {
			await runner!.refresh();
			expect(connectToHost).not.toHaveBeenCalled();
			expect(getSessions).toHaveBeenCalledWith(config, "all");
			expect(snapshot.connection).toBe("open");
		} finally { runner?.stop(); }
	});

	it("refuses to adopt a verified snapshot whose config belongs to another host", async () => {
		const { connectToHost } = await import("./connectRuntime");
		const otherHosts = await import("./otherHosts") as typeof import("./otherHosts") & {
			startVerifiedOtherHost?: typeof import("./otherHosts")["startOtherHost"];
		};
		expect(otherHosts.startVerifiedOtherHost).toBeTypeOf("function");
		if (!otherHosts.startVerifiedOtherHost) return;

		const wrong: HostSnapshot = {
			...otherHosts.emptyHostSnapshot(host),
			config: { ...config, hostId: "different-host" },
			connection: "open",
			loading: false,
		};
		expect(otherHosts.startVerifiedOtherHost(host, wrong, vi.fn())).toBeNull();
		expect(connectToHost).not.toHaveBeenCalled();
	});

	it("ignores a verified runner's in-flight result after ownership stops", async () => {
		const { getSessions } = await import("./api");
		const otherHosts = await import("./otherHosts") as typeof import("./otherHosts") & {
			startVerifiedOtherHost?: typeof import("./otherHosts")["startOtherHost"];
		};
		expect(otherHosts.startVerifiedOtherHost).toBeTypeOf("function");
		if (!otherHosts.startVerifiedOtherHost) return;

		let finish!: (value: Awaited<ReturnType<typeof getSessions>>) => void;
		vi.mocked(getSessions).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		const publish = vi.fn();
		const runner = otherHosts.startVerifiedOtherHost(
			host,
			{ ...otherHosts.emptyHostSnapshot(host), config, connection: "open", loading: false },
			publish,
		)!;
		const pending = runner.refresh();
		runner.stop();
		finish({ projects: [], sessions: [], orchestrators: [], orchestratorId: null, stats: {} });
		await pending;
		expect(publish).not.toHaveBeenCalled();
	});
});
