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
vi.mock("./remote-sse-probe", () => ({
	probeRemoteSse: (url: string, _names: string[], onEvent: () => void, onVerified: () => void, onFailure: () => void) => {
		const probe = { url, onEvent, onVerified, onFailure, closed: false };
		hosts.probes.push(probe);
		return () => {
			probe.closed = true;
		};
	},
}));

import { connectRemoteApprovalStreams, remotePendingApprovalsQueryKey } from "./approvals";

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
