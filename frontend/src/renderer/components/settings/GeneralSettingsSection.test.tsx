import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Settings } from "../../hooks/useSettings";
import { useUiStore } from "../../stores/ui-store";
import { aoBridge } from "../../lib/bridge";
import { THEMES } from "../../lib/theme-palettes";

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

describe("custom themes", () => {
	const [, vercel] = THEMES;
	const faint = { id: "custom:faint" as const, label: "Faint", file: "faint.json", dark: vercel.dark, light: vercel.light, lowContrast: ["dark.muted", "light.red"] };

	it("lists custom themes in the theme menu and says what is wrong with the files it could not load", async () => {
		settingsMock.mockReturnValue(settings({}));
		useUiStore.getState().setCustomThemes({
			directory: "/home/me/.ao/themes",
			themes: [faint],
			errors: [
				{ file: "broken.json", problem: { code: "json" } },
				{ file: "half.json", problem: { code: "color", mode: "light", role: "accent" } },
			],
		});
		const openFolder = vi.spyOn(aoBridge.themes, "openFolder").mockResolvedValue();
		renderSection();

		expect(screen.getByText(/Theme files in \/home\/me\/\.ao\/themes appear in the theme list\./)).toBeInTheDocument();
		expect(screen.getAllByRole("alert").map((alert) => alert.textContent)).toEqual([
			"broken.json: not a valid JSON object.",
			"half.json: light.accent must be a hex color such as #1a2b3c.",
		]);
		expect(screen.getByText("Faint: low contrast for dark.muted, light.red.")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Open folder" }));
		expect(openFolder).toHaveBeenCalledOnce();

		await userEvent.click(screen.getByRole("button", { name: "Color Theme" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /Faint/ }));
		expect(useUiStore.getState().themeStyle).toBe("custom:faint");
	});

	it("has no row where there is no themes folder, as in the web preview", () => {
		settingsMock.mockReturnValue(settings({}));
		useUiStore.getState().setCustomThemes({ directory: "", themes: [], errors: [] });
		renderSection();
		expect(screen.queryByText("Custom themes")).not.toBeInTheDocument();
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
