import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DeviceRevealWatcher } from "./DeviceRevealWatcher";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

const lease = {
	sessionId: "ao-1",
	provider: "local",
	reason: "Runs on this machine",
	device: { id: "UDID-1", name: "iPhone 17 Pro", platform: "ios", booted: true },
	agentDevice: { command: "/ao/agent-device", args: [] },
	openedAt: "2026-10-26T00:00:00Z",
};

function mockApi(state: { supportOn: boolean; lease: unknown }) {
	getMock.mockImplementation(async (path: string) =>
		path === "/api/v1/devices"
			? { data: { enabled: state.supportOn, state: state.supportOn ? "ready" : "off", platforms: [], devices: [], leases: [] } }
			: { data: { lease: state.lease } },
	);
}

function renderWatcher(onReveal: () => void, { terminated = false } = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<DeviceRevealWatcher enabled onReveal={onReveal} sessionId="ao-1" terminated={terminated} uiSessionId="ao-1" />
		</QueryClientProvider>,
	);
	return client;
}

const leaseCalls = () => getMock.mock.calls.filter(([path]) => path === "/api/v1/sessions/{sessionId}/device").length;

describe("DeviceRevealWatcher", () => {
	afterEach(() => getMock.mockReset());

	it("reveals a device an agent opens while the session is on screen", async () => {
		const state = { supportOn: true, lease: null as unknown };
		mockApi(state);
		const onReveal = vi.fn();
		const client = renderWatcher(onReveal);
		await waitFor(() => expect(leaseCalls()).toBe(1));

		state.lease = lease;
		await act(() => client.invalidateQueries({ queryKey: ["session-device", "ao-1"] }));

		await waitFor(() => expect(onReveal).toHaveBeenCalledTimes(1));
	});

	it("does not reveal a device the session already held when it was entered", async () => {
		mockApi({ supportOn: true, lease });
		const onReveal = vi.fn();
		renderWatcher(onReveal);
		await waitFor(() => expect(leaseCalls()).toBe(1));
		await act(async () => {});
		expect(onReveal).not.toHaveBeenCalled();
	});

	it("asks for no lease while device support is off", async () => {
		mockApi({ supportOn: false, lease });
		renderWatcher(vi.fn());
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/devices"));
		await act(async () => {});
		expect(leaseCalls()).toBe(0);
	});

	it("does not watch a terminated session", async () => {
		mockApi({ supportOn: true, lease });
		renderWatcher(vi.fn(), { terminated: true });
		await act(async () => {});
		expect(getMock).not.toHaveBeenCalled();
	});
});
