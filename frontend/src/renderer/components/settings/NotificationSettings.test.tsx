import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Settings } from "../../hooks/useSettings";
import { aoBridge } from "../../lib/bridge";
import { publishSavedRemoteHosts } from "../../lib/saved-remote-hosts";
import { useNotificationPreferencesStore } from "../../stores/notification-preferences-store";

const { settingsMock } = vi.hoisted(() => ({ settingsMock: vi.fn() }));

const idle = { update: vi.fn(), saving: false, error: undefined };
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: settingsMock(), isLoading: false, error: null }),
	useUpdateSessionInterface: () => idle,
	useUpdateVersionRefresh: () => idle,
	useUpdateSessionRetention: () => idle,
}));

vi.mock("../../hooks/useEditorHandoff", () => ({
	useEditorPreference: () => ({ data: { editors: [] }, isPending: false, isError: false }),
	useSetPreferredEditor: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

vi.mock("./DefaultCloudSandboxRow", () => ({ DefaultCloudSandboxSection: () => null }));
vi.mock("./CliLinkSettingsRow", () => ({ CliLinkSettings: () => null }));

import { GeneralSettingsSection } from "./GeneralSettingsSection";
import { TooltipProvider } from "../ui/tooltip";

function renderGeneral() {
	render(
		<QueryClientProvider client={new QueryClient()}>
			<TooltipProvider>
				<GeneralSettingsSection />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

function settings(overrides: Partial<Settings> = {}): Settings {
	return {
		defaultSessionMode: "tui",
		harnessDefaults: {},
		chatHarnesses: [],
		client: "",
		localEnabled: true,
		cloudEnabled: true,
		cloudControlPlaneUrl: "",
		trackerIntakeEnabled: false,
		...overrides,
	};
}

const setUiSettings = vi.spyOn(aoBridge.uiSettings, "set");

beforeEach(() => {
	settingsMock.mockReset().mockReturnValue(settings());
	setUiSettings.mockReset().mockImplementation(async (patch) => ({ ...(await aoBridge.uiSettings.get()), ...patch }));
	useNotificationPreferencesStore.setState({ enabled: true, filters: { mutedKinds: [], mutedSources: [] }, loaded: true, saving: false, saveError: false });
	publishSavedRemoteHosts([{ hostId: "host-a", label: "Studio Mac", status: "connected" }]);
});

describe("General › Notifications", () => {
	it("offers notifications, sound, and Manage notifications only while notifications are on", async () => {
		renderGeneral();

		expect(screen.getByRole("switch", { name: "Notifications" })).toBeChecked();
		expect(screen.getByRole("switch", { name: "Sound notifications" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Manage notifications/ })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("switch", { name: "Notifications" }));
		expect(setUiSettings).toHaveBeenCalledWith({
			notificationsEnabled: false,
			notificationFilters: { mutedKinds: [], mutedSources: [] },
		});
		expect(screen.queryByRole("button", { name: /Manage notifications/ })).not.toBeInTheDocument();
	});

	it("opens Manage notifications in place of General and comes back to its row", async () => {
		renderGeneral();
		await userEvent.click(screen.getByRole("button", { name: /Manage notifications/ }));

		const back = screen.getByRole("button", { name: "General" });
		expect(back).toHaveFocus();
		expect(screen.getByRole("heading", { name: "Manage notifications" })).toBeInTheDocument();
		expect(screen.queryByRole("switch", { name: "Sound notifications" })).not.toBeInTheDocument();
		for (const kind of ["Agent needs you", "Pull request opened", "CI failed", "Review complete", "Pull request merged", "Everything else"]) {
			expect(screen.getByRole("switch", { name: kind })).toBeChecked();
		}
		for (const machine of ["This computer", "Studio Mac", "Cloud"]) {
			expect(screen.getByRole("switch", { name: machine })).toBeChecked();
		}

		await userEvent.click(back);
		expect(screen.getByRole("button", { name: /Manage notifications/ })).toHaveFocus();
	});

	it("saves the kinds and machines that are turned off", async () => {
		renderGeneral();
		await userEvent.click(screen.getByRole("button", { name: /Manage notifications/ }));

		await userEvent.click(screen.getByRole("switch", { name: "Pull request merged" }));
		await userEvent.click(screen.getByRole("switch", { name: "Studio Mac" }));
		expect(setUiSettings).toHaveBeenLastCalledWith({
			notificationsEnabled: true,
			notificationFilters: { mutedKinds: ["pr_merged"], mutedSources: ["remote:host-a"] },
		});
		expect(screen.getByRole("switch", { name: "Pull request merged" })).not.toBeChecked();

		const row = screen.getByRole("switch", { name: "Pull request merged" }).closest<HTMLElement>("[data-notification-kind]")!;
		expect(within(row).getByText("A pull request was merged.")).toBeInTheDocument();
	});

	it("holds the switch until the saved preferences have loaded", () => {
		useNotificationPreferencesStore.setState({ loaded: false });
		renderGeneral();

		expect(screen.getByRole("switch", { name: "Notifications" })).toBeDisabled();
		act(() => useNotificationPreferencesStore.setState({ loaded: true }));
		expect(screen.getByRole("switch", { name: "Notifications" })).toBeEnabled();
	});

	it("takes one change at a time, so a failed one rolls back to what was saved", async () => {
		renderGeneral();
		await userEvent.click(screen.getByRole("button", { name: /Manage notifications/ }));
		let reject!: (error: Error) => void;
		setUiSettings.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));

		await userEvent.click(screen.getByRole("switch", { name: "Pull request merged" }));
		expect(screen.getByRole("switch", { name: "Pull request merged" })).not.toBeChecked();
		expect(screen.getByRole("switch", { name: "Studio Mac" })).toBeDisabled();
		expect(screen.getByRole("switch", { name: "CI failed" })).toBeDisabled();

		await act(async () => reject(new Error("disk full")));
		expect(screen.getByRole("switch", { name: "Pull request merged" })).toBeChecked();
		expect(screen.getByRole("switch", { name: "Studio Mac" })).toBeEnabled();
		expect(screen.getByRole("alert")).toBeInTheDocument();
	});

	it("leaves Cloud out of the machines without AO Cloud", async () => {
		settingsMock.mockReturnValue(settings({ cloudEnabled: false }));
		renderGeneral();
		await userEvent.click(screen.getByRole("button", { name: /Manage notifications/ }));

		expect(screen.queryByRole("switch", { name: "Cloud" })).not.toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "This computer" })).toBeInTheDocument();
	});
});
