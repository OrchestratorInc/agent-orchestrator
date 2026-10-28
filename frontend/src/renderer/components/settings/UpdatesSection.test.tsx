import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { UpdatesSection } from "./UpdatesSection";
import { useUiStore } from "../../stores/ui-store";
import type { UpdateStatus } from "../../../main/update-settings";

const {
	updGetStatus,
	updOnStatus,
	updDownload,
	settingsGet,
	settingsSet,
	getVersion,
	featureBuildsList,
	featureBuildsGetActive,
	telemetryCapture,
} = vi.hoisted(() => ({
	updGetStatus: vi.fn(),
	updOnStatus: vi.fn(),
	updDownload: vi.fn(),
	settingsGet: vi.fn(),
	settingsSet: vi.fn(),
	getVersion: vi.fn(),
	featureBuildsList: vi.fn(),
	featureBuildsGetActive: vi.fn(),
	telemetryCapture: vi.fn(),
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		app: { getVersion },
		updates: {
			getStatus: updGetStatus,
			onStatus: updOnStatus,
			install: vi.fn(),
			download: updDownload,
			check: vi.fn(),
			returnHome: vi.fn(),
		},
		updateSettings: { get: settingsGet, set: settingsSet },
		featureBuilds: { getActive: featureBuildsGetActive, list: featureBuildsList },
	},
}));

vi.mock("../../lib/telemetry", () => ({
	captureRendererEvent: telemetryCapture,
	releaseChannelFrom: vi.fn(),
	setReleaseChannelContext: vi.fn(),
}));

vi.mock("../../hooks/useRequestUpdateInstall", () => ({
	useRequestUpdateInstall: () => () => undefined,
}));

function renderUpdates() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	render(
		<QueryClientProvider client={queryClient}>
			<UpdatesSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	for (const m of [updGetStatus, updOnStatus, updDownload, settingsGet, settingsSet, getVersion, featureBuildsList, featureBuildsGetActive, telemetryCapture]) {
		m.mockReset();
	}
	updOnStatus.mockReturnValue(() => undefined);
	settingsGet.mockResolvedValue({ enabled: false, channel: "latest", nightlyAck: true, feature: null });
	settingsSet.mockResolvedValue({ enabled: false, channel: "latest", nightlyAck: true, feature: null });
	getVersion.mockResolvedValue("0.12.11-nightly.202609070300");
	featureBuildsList.mockResolvedValue([]);
	featureBuildsGetActive.mockResolvedValue(null);
	updGetStatus.mockResolvedValue({ state: "idle" } satisfies UpdateStatus);
	useUiStore.setState({ developerMode: false });
});

it("renders the installed nightly build time as the device-local instant", async () => {
	// The stamp 202609070300 encodes 03:00 UTC. The expected string is derived
	// from that absolute instant, so the assertion holds in every timezone —
	// and it can only hold at all when the stamp is parsed as UTC, never as
	// local wall time (issue #5059).
	renderUpdates();

	const builtAt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(
		new Date(Date.UTC(2026, 8, 7, 3, 0)),
	);
	expect(await screen.findByText(`Built ${builtAt}`)).toBeVisible();
});

it("links PRs and commits in downloaded notes from the renamed repository", async () => {
	const repo = "https://github.com/OrchestratorInc/agent-orchestrator";
	const commit = "7b7db96413e6a396a10afbcada0e2fd04a994ad8";
	updGetStatus.mockResolvedValue({
		state: "downloaded",
		version: "0.13.4-nightly.202610051230",
		releaseNotes: [
			"**Changes in this nightly**",
			"",
			`- Refresh startup readiness by @illegalcall in [#6228](${repo}/pull/6228)`,
			"",
			"**Build details**",
			"",
			`- Commit: [7b7db96](${repo}/commit/${commit})`,
		].join("\n"),
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByRole("link", { name: "#6228" })).toHaveAttribute("href", `${repo}/pull/6228`);
	expect(screen.getByRole("link", { name: "7b7db96" })).toHaveAttribute("href", `${repo}/commit/${commit}`);
	expect(screen.queryByText(/@illegalcall/)).toBeNull();
});

it("names the newer build when a full disk holds it back (#6607)", async () => {
	updGetStatus.mockResolvedValue({
		state: "error",
		message: "Not enough disk space to install the update. Free up space, then retry.",
		version: "0.13.8-nightly.202610121355",
		staged: { version: "0.13.7-nightly.202610101220", stagedAt: 1, escalated: false, ready: false },
	} satisfies UpdateStatus);
	renderUpdates();

	expect(await screen.findByText(/Not enough disk space to install the update/)).toBeVisible();
	expect(screen.getByText("v0.13.8-nightly.202610121355 is available")).toBeVisible();
});

it.each(["downloaded", "error"] as const)("shows updating feedback immediately until the update reaches %s", async (state) => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "available", version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();

	const update = await screen.findByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);
	const updating = screen.getByRole("button", { name: "Updating… Update to v9.9.9" });
	expect(updating).toBeVisible();
	expect(updating).toHaveAttribute("aria-disabled", "true");
	expect(updating).not.toBeDisabled();
	expect(updating).toHaveFocus();
	fireEvent.click(updating);
	expect(updDownload).toHaveBeenCalledTimes(1);
	expect(screen.getByTestId("update-status-line")).toHaveAttribute("aria-busy", "true");

	act(() => emit({ state: "downloading", version: "9.9.9", percent: 42 }));
	expect(screen.getByRole("button", { name: "Updating… Update to v9.9.9" })).toHaveFocus();
	expect(updating).toHaveTextContent(/^Updating…$/);
	expect(screen.getByRole("progressbar")).toHaveAttribute("value", "42");
	act(() => emit({ state: "preparing", version: "9.9.9" }));
	expect(screen.getByRole("button", { name: "Updating… Update to v9.9.9" })).toHaveFocus();

	act(() => emit({ state, version: "9.9.9", ...(state === "error" ? { message: "Download failed" } : {}) }));
	expect(updating).not.toBeInTheDocument();
	expect(screen.getByTestId("update-status-line")).toHaveAttribute("aria-busy", "false");
	if (state === "downloaded") expect(screen.getByRole("button", { name: "Restart & install" })).toHaveFocus();
	else {
		expect(screen.getByText("Download failed")).toBeVisible();
		expect(screen.getByRole("button", { name: "Check for updates" })).toHaveFocus();
	}
	expect(screen.getByRole("button", { name: "Check for updates" })).toBeEnabled();
});

it.each(["downloading", "preparing"] as const)("shows an existing %s update without a click", async (state) => {
	updGetStatus.mockResolvedValue({ state, version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();

	const updating = await screen.findByRole("button", { name: "Updating… Update to v9.9.9" });
	expect(updating).toBeVisible();
	expect(updating).toHaveAttribute("aria-disabled", "true");
	expect(updating).not.toBeDisabled();
	expect(updating).not.toHaveFocus();
	fireEvent.click(updating);
	expect(updDownload).not.toHaveBeenCalled();
	expect(screen.getByTestId("update-status-line")).toHaveAttribute("aria-busy", "true");
});

it.each(["downloaded", "error"] as const)("does not steal focus when an update reaches %s", async (state) => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	updGetStatus.mockResolvedValue({ state: "available", version: "9.9.9" } satisfies UpdateStatus);
	renderUpdates();
	const update = await screen.findByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);
	const automaticUpdates = screen.getByRole("switch");
	automaticUpdates.focus();

	act(() => emit({ state, version: "9.9.9" }));
	expect(automaticUpdates).toHaveFocus();
});

it("focuses the status after a fast failure while the manual check action is still busy", async () => {
	let emit: (status: UpdateStatus) => void = () => undefined;
	updOnStatus.mockImplementation((listener: typeof emit) => {
		emit = listener;
		return () => undefined;
	});
	renderUpdates();
	fireEvent.click(await screen.findByRole("button", { name: "Check for updates" }));
	act(() => emit({ state: "available", version: "9.9.9" }));
	const update = screen.getByRole("button", { name: "Update to v9.9.9" });
	update.focus();
	fireEvent.click(update);

	act(() => emit({ state: "error", message: "Download failed" }));
	expect(screen.getByRole("button", { name: "Checking for updates…" })).toBeDisabled();
	expect(screen.getByTestId("update-status-line")).toHaveFocus();
});
