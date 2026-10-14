import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

const { cloudGateState, getSessionMock, settingsState } = vi.hoisted(() => ({
	cloudGateState: { cloudEnabled: false },
	getSessionMock: vi.fn(),
	settingsState: {
		isLoading: true,
		settings: undefined as undefined | { cloudControlPlaneUrl: string },
	},
}));

vi.mock("./useSettings", () => ({ useSettings: () => settingsState }));
vi.mock("./useCloudGate", () => ({ useCloudGate: () => cloudGateState }));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		cloud: {
			getSession: getSessionMock,
			onSessionChanged: () => () => undefined,
			signIn: vi.fn(),
			signOut: vi.fn(),
		},
	},
}));
vi.mock("../lib/cloud-telemetry", () => ({ noteCloudSession: vi.fn() }));
vi.mock("../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({}),
}));

import { useCloudCp } from "./useCloudCp";

beforeEach(() => {
	cloudGateState.cloudEnabled = false;
	getSessionMock.mockReset().mockReturnValue(new Promise(() => undefined));
	settingsState.isLoading = true;
	settingsState.settings = undefined;
});

it("reports Cloud readiness as loading while settings are unresolved", () => {
	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: true, ready: false });
});

it("keeps loading while an enabled Cloud account is unresolved", () => {
	settingsState.isLoading = false;
	settingsState.settings = { cloudControlPlaneUrl: "https://cloud.example" };
	cloudGateState.cloudEnabled = true;

	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: true, ready: false });
});

it("does not leave disabled Cloud in a loading state", () => {
	settingsState.isLoading = false;
	settingsState.settings = { cloudControlPlaneUrl: "https://cloud.example" };

	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: false, ready: false });
});

it("finishes loading once the real fresh-mount account read authenticates configured Cloud", async () => {
	settingsState.isLoading = false;
	settingsState.settings = { cloudControlPlaneUrl: "https://cloud.example" };
	cloudGateState.cloudEnabled = true;
	getSessionMock.mockResolvedValue({ user: { id: "user-1" } });

	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: true, ready: false });
	await waitFor(() => expect(result.current).toMatchObject({ isLoading: false, ready: true }));
	expect(getSessionMock).toHaveBeenCalledTimes(1);
});

it("finishes loading without enabling Cloud when the fresh-mount account read is signed out", async () => {
	settingsState.isLoading = false;
	settingsState.settings = { cloudControlPlaneUrl: "https://cloud.example" };
	cloudGateState.cloudEnabled = true;
	getSessionMock.mockResolvedValue(null);

	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: true, ready: false });
	await waitFor(() => expect(result.current).toMatchObject({ isLoading: false, ready: false }));
});
