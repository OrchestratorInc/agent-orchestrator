import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const hosts = vi.hoisted(() => ({
	list: [] as string[],
	listeners: new Set<() => void>(),
	probes: [] as { url: string; onEvent: () => void; onVerified: () => void; onFailure: () => void; closed: boolean }[],
}));

vi.mock("./host-clients", () => ({
	connectedHosts: () => hosts.list,
	subscribeConnectedHosts: (listener: () => void) => {
		hosts.listeners.add(listener);
		return () => hosts.listeners.delete(listener);
	},
	baseUrlForHost: (hostId: string) => `https://${hostId}.example/`,
	clientForHost: vi.fn(),
}));
vi.mock("./api-client", () => ({
	apiClient: {},
	eventStreamBaseUrl: (base: string) => base,
	getApiBaseUrl: () => "http://daemon.test",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => undefined,
}));
vi.mock("./remote-sse-probe", () => ({
	probeRemoteSse: (url: string, _names: string[], onEvent: () => void, onVerified: () => void, onFailure: () => void) => {
		const probe = { url, onEvent, onVerified, onFailure, closed: false };
		hosts.probes.push(probe);
		return () => {
			probe.closed = true;
		};
	},
}));

import { connectApprovalStream, connectRemoteApprovalStreams, isApprovalStreamLive, remotePendingApprovalsQueryKey } from "./approvals";

describe("connectRemoteApprovalStreams", () => {
	beforeEach(() => {
		hosts.list = [];
		hosts.listeners.clear();
		hosts.probes = [];
	});

	it("streams each connected host's approvals and refreshes only that host on an event", () => {
		hosts.list = ["box-a", "box-b"];
		const queryClient = new QueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
		const dispose = connectRemoteApprovalStreams(queryClient);
		expect(hosts.probes.map((probe) => probe.url)).toEqual([
			"https://box-a.example/api/v1/approvals/stream",
			"https://box-b.example/api/v1/approvals/stream",
		]);
		hosts.probes[1].onEvent();
		expect(invalidate).toHaveBeenCalledWith({ queryKey: remotePendingApprovalsQueryKey("box-b") });
		expect(invalidate).not.toHaveBeenCalledWith({ queryKey: remotePendingApprovalsQueryKey("box-a") });

		// A host that disconnects has its stream closed.
		hosts.list = ["box-b"];
		hosts.listeners.forEach((listener) => listener());
		expect(hosts.probes[0].closed).toBe(true);
		expect(hosts.probes[1].closed).toBe(false);
		dispose();
		expect(hosts.probes[1].closed).toBe(true);
	});
});

describe("connectRemoteApprovalStreams reconnects", () => {
	beforeEach(() => {
		hosts.list = [];
		hosts.listeners.clear();
		hosts.probes = [];
	});

	it("reopens a remote host's stream after it fails", async () => {
		vi.useFakeTimers();
		try {
			hosts.list = ["box-a"];
			const queryClient = new QueryClient();
			vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
			const dispose = connectRemoteApprovalStreams(queryClient);
			expect(hosts.probes).toHaveLength(1);
			hosts.probes[0]!.onFailure();
			await vi.advanceTimersByTimeAsync(60_000);
			expect(hosts.probes).toHaveLength(2);
			expect(hosts.probes[0]!.closed).toBe(true);
			expect(hosts.probes[1]!.url).toBe("https://box-a.example/api/v1/approvals/stream");
			dispose();
		} finally {
			vi.useRealTimers();
		}
	});
});

class FakeEventSource {
	static instances: FakeEventSource[] = [];
	readyState = 1;
	closed = false;
	onerror: (() => void) | null = null;
	private listeners = new Map<string, Array<() => void>>();
	constructor(readonly url: string) {
		FakeEventSource.instances.push(this);
	}
	addEventListener(name: string, listener: () => void) {
		this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener]);
	}
	emit(name: string) {
		for (const listener of this.listeners.get(name) ?? []) listener();
	}
	close() {
		this.closed = true;
		this.readyState = 2;
	}
}

describe("connectApprovalStream", () => {
	it("reconnects a stream that goes silent, and reports when it is live", async () => {
		vi.useFakeTimers();
		const original = globalThis.EventSource;
		FakeEventSource.instances = [];
		globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
		try {
			const queryClient = new QueryClient();
			vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
			const dispose = connectApprovalStream(queryClient);
			const first = FakeEventSource.instances[0]!;
			expect(first.url).toBe("http://daemon.test/api/v1/approvals/stream");
			expect(isApprovalStreamLive()).toBe(false);
			first.emit("ready");
			expect(isApprovalStreamLive()).toBe(true);
			// Heartbeats keep it alive.
			await vi.advanceTimersByTimeAsync(30_000);
			first.emit("heartbeat");
			await vi.advanceTimersByTimeAsync(30_000);
			expect(first.closed).toBe(false);
			// Then the server goes quiet without closing: the watchdog reconnects.
			await vi.advanceTimersByTimeAsync(60_000);
			expect(first.closed).toBe(true);
			expect(isApprovalStreamLive()).toBe(false);
			await vi.advanceTimersByTimeAsync(60_000);
			const second = FakeEventSource.instances[1]!;
			expect(second).toBeDefined();
			second.emit("ready");
			expect(isApprovalStreamLive()).toBe(true);
			dispose();
			expect(second.closed).toBe(true);
			expect(isApprovalStreamLive()).toBe(false);
		} finally {
			globalThis.EventSource = original;
			vi.useRealTimers();
		}
	});
});
