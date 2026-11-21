import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({ get: vi.fn() }));
const bridgeMocks = vi.hoisted(() => ({ show: vi.fn(), onClick: vi.fn() }));
const uiMocks = vi.hoisted(() => ({ openGlobalSettings: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: apiMocks.get },
	apiErrorMessage: () => "Request failed",
}));
vi.mock("../lib/bridge", () => ({ aoBridge: { notifications: bridgeMocks } }));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: typeof uiMocks) => unknown) => select(uiMocks),
}));

import { useRemoteComputerUseAlerts } from "./useRemoteComputerUseAlerts";

let remote: Record<string, unknown>;

function renderAlerts() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, ...renderHook(() => useRemoteComputerUseAlerts(), { wrapper }) };
}

beforeEach(() => {
	apiMocks.get.mockReset().mockImplementation(async () => ({ data: remote, error: undefined }));
	bridgeMocks.show.mockReset().mockResolvedValue(undefined);
	bridgeMocks.onClick.mockReset().mockReturnValue(() => undefined);
	uiMocks.openGlobalSettings.mockReset();
});

describe("useRemoteComputerUseAlerts", () => {
	it("raises one OS notification per run of another computer's calls", async () => {
		remote = { allowRemote: true, activity: [{ caller: "abc", name: "Build box", started: "2026-11-21T09:00:00Z", lastAt: "2026-11-21T09:00:30Z" }] };
		const { client } = renderAlerts();
		await waitFor(() => expect(bridgeMocks.show).toHaveBeenCalledTimes(1));
		expect(bridgeMocks.show).toHaveBeenCalledWith(expect.objectContaining({
			id: "computer-use-remote:abc:2026-11-21T09:00:00Z",
			title: "Build box's agent is using this computer",
			body: "Orchestrator on Build box is operating apps here. Open Settings to stop it.",
			type: "computer_use_remote",
		}));

		// Later calls in the same run raise nothing new.
		remote = { allowRemote: true, activity: [{ caller: "abc", name: "Build box", started: "2026-11-21T09:00:00Z", lastAt: "2026-11-21T09:01:30Z" }] };
		await client.refetchQueries();
		expect(bridgeMocks.show).toHaveBeenCalledTimes(1);

		// A new run after a pause does.
		remote = { allowRemote: true, activity: [{ caller: "abc", started: "2026-11-21T09:10:00Z", lastAt: "2026-11-21T09:10:00Z" }] };
		await client.refetchQueries();
		await waitFor(() => expect(bridgeMocks.show).toHaveBeenCalledTimes(2));
		expect(bridgeMocks.show).toHaveBeenLastCalledWith(expect.objectContaining({ title: "Another computer's agent is using this computer" }));
	});

	it("stays quiet while other computers may not use this one", async () => {
		remote = { allowRemote: false, activity: [{ caller: "abc", name: "Build box", started: "2026-11-21T09:00:00Z", lastAt: "2026-11-21T09:00:30Z" }] };
		renderAlerts();
		await waitFor(() => expect(apiMocks.get).toHaveBeenCalled());
		expect(bridgeMocks.show).not.toHaveBeenCalled();
	});

	it("opens Computer use settings from its notification and ignores others", () => {
		remote = { allowRemote: false, activity: [] };
		renderAlerts();
		const onClick = bridgeMocks.onClick.mock.calls[0][0] as (id: string) => void;
		onClick("ntf_123");
		expect(uiMocks.openGlobalSettings).not.toHaveBeenCalled();
		onClick("computer-use-remote:abc:2026-11-21T09:00:00Z");
		expect(uiMocks.openGlobalSettings).toHaveBeenCalledWith("computerUse");
	});
});
