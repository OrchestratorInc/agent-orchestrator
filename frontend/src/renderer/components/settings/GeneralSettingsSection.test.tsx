import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Settings } from "../../hooks/useSettings";
import * as platform from "../../lib/platform";
import { aoBridge } from "../../lib/bridge";
import { DEFAULT_UI_SETTINGS, type UiSettings } from "../../../shared/ui-locale";
import { useUiStore } from "../../stores/ui-store";

const { settingsMock, updateRetentionMock, updateCommitAttributionMock } = vi.hoisted(() => ({
	settingsMock: vi.fn(),
	updateRetentionMock: vi.fn(),
	updateCommitAttributionMock: vi.fn(),
}));

const idle = { update: vi.fn(), saving: false, error: undefined };
vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: settingsMock(), isLoading: false, error: null }),
	useUpdateSessionInterface: () => idle,
	useUpdateVersionRefresh: () => idle,
	useUpdateSessionRetention: () => ({ update: updateRetentionMock, saving: false, error: undefined }),
	useUpdateCommitAttribution: () => ({ update: updateCommitAttributionMock, saving: false, error: undefined }),
}));

vi.mock("../../hooks/useEditorHandoff", () => ({
	useEditorPreference: () => ({ data: { editors: [] }, isPending: false, isError: false }),
	useSetPreferredEditor: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

// The real rows read the cloud gate and the CLI link; this file covers retention.
vi.mock("./DefaultCloudSandboxRow", () => ({ DefaultCloudSandboxSection: () => null }));
vi.mock("./DataRetentionSection", () => ({ DataRetentionSection: () => null }));
vi.mock("./CliLinkSettingsRow", () => ({ CliLinkSettings: () => null }));

import { GeneralSettingsSection } from "./GeneralSettingsSection";
import { TooltipProvider } from "../ui/tooltip";

function renderSection() {
	render(
		<QueryClientProvider client={new QueryClient()}>
			<TooltipProvider>
				<GeneralSettingsSection />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

function settings(overrides: Partial<Settings>): Settings {
	return {
		defaultSessionMode: "tui",
		harnessDefaults: {},
		chatHarnesses: [],
		client: "",
		localEnabled: true,
		cloudEnabled: false,
		cloudControlPlaneUrl: "",
		trackerIntakeEnabled: false,
		...overrides,
	};
}

beforeEach(() => {
	settingsMock.mockReset();
	updateRetentionMock.mockReset();
});

describe("archived session retention", () => {
	it("saves the chosen number of days", async () => {
		settingsMock.mockReturnValue(settings({ sessionRetentionDays: 0 }));
		renderSection();

		const menu = screen.getByRole("button", { name: "Delete archived sessions" });
		expect(menu).toHaveTextContent("Never");
		await userEvent.click(menu);
		await userEvent.click(await screen.findByRole("menuitem", { name: "After 30 days" }));

		expect(updateRetentionMock).toHaveBeenCalledWith(30);
	});

	it("shows a value set outside the menu as itself", () => {
		settingsMock.mockReturnValue(settings({ sessionRetentionDays: 14 }));
		renderSection();

		expect(screen.getByRole("button", { name: "Delete archived sessions" })).toHaveTextContent("After 14 days");
	});

	it("hides the control on a daemon that cannot apply it", () => {
		settingsMock.mockReturnValue(settings({}));
		renderSection();

		expect(screen.queryByRole("button", { name: "Delete archived sessions" })).not.toBeInTheDocument();
	});
});
describe("Linux Alt menu setting", () => {
	it("persists the user's choice on Linux", async () => {
		vi.spyOn(platform, "isLinuxPlatform").mockReturnValue(true);
		const get = vi.spyOn(aoBridge.uiSettings, "get").mockResolvedValue({ ...DEFAULT_UI_SETTINGS });
		const set = vi.spyOn(aoBridge.uiSettings, "set").mockResolvedValue({ ...DEFAULT_UI_SETTINGS, altMenuEnabled: false });
		settingsMock.mockReturnValue(settings({}));
		renderSection();
		const toggle = await screen.findByRole("switch", { name: "Show menu bar with Alt" });
		expect(toggle).toBeChecked();
		await userEvent.click(toggle);
		expect(set).toHaveBeenCalledWith({ altMenuEnabled: false });
		get.mockRestore(); set.mockRestore(); vi.restoreAllMocks();
	});

	it("keeps a user's choice when the initial settings read completes late", async () => {
		vi.spyOn(platform, "isLinuxPlatform").mockReturnValue(true);
		let resolveSettings!: (settings: UiSettings) => void;
		const settingsPromise = new Promise<UiSettings>((resolve) => {
			resolveSettings = resolve;
		});
		vi.spyOn(aoBridge.uiSettings, "get").mockReturnValue(settingsPromise);
		const set = vi.spyOn(aoBridge.uiSettings, "set").mockResolvedValue({ ...DEFAULT_UI_SETTINGS, altMenuEnabled: false });
		settingsMock.mockReturnValue(settings({}));
		renderSection();

		const toggle = await screen.findByRole("switch", { name: "Show menu bar with Alt" });
		await userEvent.click(toggle);
		expect(set).toHaveBeenCalledWith({ altMenuEnabled: false });

		await act(async () => {
			resolveSettings({ ...DEFAULT_UI_SETTINGS, altMenuEnabled: true });
			await settingsPromise;
		});
		expect(toggle).not.toBeChecked();
		vi.restoreAllMocks();
	});

	it("disables the switch while saving and restores the last confirmed value on failure", async () => {
		vi.spyOn(platform, "isLinuxPlatform").mockReturnValue(true);
		vi.spyOn(aoBridge.uiSettings, "get").mockResolvedValue({ ...DEFAULT_UI_SETTINGS, altMenuEnabled: true });
		let rejectSave!: (error: Error) => void;
		const savePromise = new Promise<UiSettings>((_, reject) => {
			rejectSave = reject;
		});
		const set = vi.spyOn(aoBridge.uiSettings, "set").mockReturnValue(savePromise);
		settingsMock.mockReturnValue(settings({}));
		renderSection();

		const toggle = await screen.findByRole("switch", { name: "Show menu bar with Alt" });
		await userEvent.click(toggle);
		expect(toggle).toBeDisabled();
		expect(toggle).not.toBeChecked();

		await act(async () => {
			rejectSave(new Error("save failed"));
			await savePromise.catch(() => undefined);
		});
		expect(toggle).toBeEnabled();
		expect(toggle).toBeChecked();
		expect(set).toHaveBeenCalledTimes(1);
		vi.restoreAllMocks();
	});
});

describe("Orchestrator attribution beta switch", () => {
	it("only appears in Developer Mode", () => {
		settingsMock.mockReturnValue(settings({}));
		useUiStore.setState({ developerMode: false });
		renderSection();
		expect(screen.queryByRole("switch", { name: "Orchestrator attribution (beta)" })).not.toBeInTheDocument();
	});

	it("reflects the daemon setting and saves a change to it", async () => {
		updateCommitAttributionMock.mockReset();
		settingsMock.mockReturnValue(settings({ commitAttribution: false }));
		useUiStore.setState({ developerMode: true });
		renderSection();
		const toggle = screen.getByRole("switch", { name: "Orchestrator attribution (beta)" });
		expect(toggle).not.toBeChecked();
		await userEvent.click(toggle);
		expect(updateCommitAttributionMock).toHaveBeenCalledWith(true);
	});
});
