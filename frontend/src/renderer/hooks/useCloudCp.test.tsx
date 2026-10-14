import { renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

const { cloudGateState, cloudSessionState, settingsState } = vi.hoisted(() => ({
	cloudGateState: { cloudEnabled: false },
	cloudSessionState: { status: "loading" as "loading" | "authenticated" | "unauthenticated" },
	settingsState: {
		isLoading: true,
		settings: undefined as undefined | { cloudControlPlaneUrl: string },
	},
}));

vi.mock("./useSettings", () => ({ useSettings: () => settingsState }));
vi.mock("./useCloudGate", () => ({ useCloudGate: () => cloudGateState }));
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ session: null, status: cloudSessionState.status }),
}));
vi.mock("../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({}),
}));

import { useCloudCp } from "./useCloudCp";

beforeEach(() => {
	cloudGateState.cloudEnabled = false;
	cloudSessionState.status = "loading";
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

it("finishes loading once enabled Cloud is authenticated and configured", () => {
	settingsState.isLoading = false;
	settingsState.settings = { cloudControlPlaneUrl: "https://cloud.example" };
	cloudGateState.cloudEnabled = true;
	cloudSessionState.status = "authenticated";

	const { result } = renderHook(() => useCloudCp());

	expect(result.current).toMatchObject({ isLoading: false, ready: true });
});
